package router

import (
	"cmp"
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	grpcproto "google.golang.org/grpc/encoding/proto"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// HostServer returns the gRPC server through which caller's sdk.Require
// clients reach the Components caller requires. It is a transparent proxy:
// it finds the Component serving the called service among caller's
// requires, refusing a call outside them with PermissionDenied and a
// streaming method with Unimplemented, applies the deadline, passes Actions
// through the gate, and forwards the request and response bytes unchanged.
// It also serves the core's own services (Offer) to every caller, whatever
// it requires. A launcher serves it to caller, with opts.
func (rt *Router) HostServer(caller string, opts ...grpc.ServerOption) *grpc.Server {
	opts = append(opts,
		grpc.UnknownServiceHandler(rt.proxy(caller)),
		grpc.ForceServerCodecV2(rawCodec{}),
	)
	s := grpc.NewServer(opts...)
	for _, register := range rt.offered {
		register(s, caller)
	}
	return s
}

// Offer adds a service of the core's own to every Component's host
// server: register registers it on s for caller. A core service is no
// Component's, so calling it needs no requires, and it audits its own
// Actions. Offer services before the runtime starts.
func (rt *Router) Offer(register func(s grpc.ServiceRegistrar, caller string)) {
	rt.offered = append(rt.offered, register)
}

func (rt *Router) proxy(caller string) grpc.StreamHandler {
	return func(_ any, stream grpc.ServerStream) error {
		fullMethod, _ := grpc.MethodFromServerStream(stream)
		service, method, _ := strings.Cut(strings.TrimPrefix(fullMethod, "/"), "/")
		target, ep, refused := rt.route(caller, service)
		if status.Code(refused) == codes.PermissionDenied {
			// Out of caller's requires: still audited when the method is
			// an Action some Component serves.
			target, ep = rt.serving(service)
		}
		if target == "" {
			return refused
		}
		md := ep.Service.Methods().ByName(protoreflect.Name(method))
		if md == nil {
			return cmp.Or(refused, status.Errorf(codes.Unimplemented, "%s has no method %s", target, method))
		}
		if streams(md) {
			refused = cmp.Or(refused, status.Errorf(codes.Unimplemented, "%s streams; Components call each other unary only", md.FullName()))
		}
		var req frame
		if err := stream.RecvMsg(&req); err != nil {
			return err
		}
		ctx := stream.Context()
		actor, chain := Forwarded(ctx, caller)
		action := appstate.Action{Chain: chain, Target: target, Method: md}
		if appstate.IsAction(md) {
			msg := dynamicpb.NewMessage(md.Input())
			if err := (proto.UnmarshalOptions{Resolver: ep.Types}).Unmarshal(req, msg); err != nil {
				refused = cmp.Or(refused, status.Errorf(codes.InvalidArgument, "decode %s: %v", md.Input().FullName(), err))
				msg = dynamicpb.NewMessage(md.Input())
			}
			action.Request = msg
		}
		done, err := rt.gate.Admit(ctx, actor, action)
		if err != nil {
			return err
		}
		if refused != nil {
			done(refused)
			return refused
		}
		ctx, cancel := callContext(onward(ctx, actor, chain), md)
		defer cancel()
		var resp frame
		err = ep.Conn.Invoke(ctx, fullMethod, &req, &resp, grpc.ForceCodecV2(rawCodec{}))
		done(err)
		if err != nil {
			return err
		}
		return stream.SendMsg(&resp)
	}
}

// route finds the Component among caller's requires that serves service.
// A required Component that isn't up yet can't say what it serves, so a
// service no up one serves gets that Component's error; only when all are
// up is it PermissionDenied. Several Components may serve one service (the
// Notification channels), but for a caller requiring two of them no call
// could say which it means: that is refused (FailedPrecondition).
func (rt *Router) route(caller, service string) (string, runtime.Endpoint, error) {
	var notUp error
	var found []string
	var ep runtime.Endpoint
	for _, name := range rt.components.ComponentRequires(caller) {
		e, err := rt.components.Endpoint(name)
		if err != nil {
			if notUp == nil {
				notUp = err
			}
			continue
		}
		if string(e.Service.FullName()) == service {
			found, ep = append(found, name), e
		}
	}
	switch {
	case len(found) == 1:
		return found[0], ep, nil
	case len(found) > 1:
		return "", runtime.Endpoint{}, status.Errorf(codes.FailedPrecondition, "%s requires several Components serving %s (%s), so a call to it can't be routed", caller, service, strings.Join(found, ", "))
	case notUp != nil:
		return "", runtime.Endpoint{}, notUp
	}
	return "", runtime.Endpoint{}, status.Errorf(codes.PermissionDenied, "%s doesn't require a Component serving %s", caller, service)
}

// serving finds a started Component serving service, whichever requires
// it, the first by name when several do; "" when none does.
func (rt *Router) serving(service string) (string, runtime.Endpoint) {
	for _, name := range rt.components.Serving(protoreflect.FullName(service)) {
		if ep, err := rt.components.Endpoint(name); err == nil {
			return name, ep
		}
	}
	return "", runtime.Endpoint{}
}

// Forwarded returns the actor and chain the caller's SDK carried onto a
// call from caller, a Component, to the host. Components are first-party,
// so they are trusted. A call without them is caller's own work on a new
// chain.
func Forwarded(ctx context.Context, caller string) (auth.Actor, string) {
	md, _ := metadata.FromIncomingContext(ctx)
	first := func(key string) string {
		if v := md.Get(key); len(v) > 0 {
			return v[0]
		}
		return ""
	}
	actor := auth.Actor{Kind: auth.Component, Name: caller}
	if a := first(sdk.ActorMetadata); a != "" {
		actor = auth.ParseActor(a)
		actor.Sub = first(sdk.SubMetadata)
	}
	chain := first(sdk.ChainMetadata)
	if chain == "" {
		chain = NewChain()
	}
	return actor, chain
}

// frame is a message's encoded bytes, passed through unchanged.
type frame []byte

// rawCodec moves frames without decoding them. Any other message, that of
// a service the core serves itself (Offer), is protobuf as usual.
type rawCodec struct{}

func (rawCodec) Marshal(v any) (mem.BufferSlice, error) {
	f, ok := v.(*frame)
	if !ok {
		return encoding.GetCodecV2(grpcproto.Name).Marshal(v)
	}
	return mem.BufferSlice{mem.SliceBuffer(*f)}, nil
}

func (rawCodec) Unmarshal(data mem.BufferSlice, v any) error {
	f, ok := v.(*frame)
	if !ok {
		return encoding.GetCodecV2(grpcproto.Name).Unmarshal(data, v)
	}
	*f = data.Materialize()
	return nil
}

func (rawCodec) Name() string { return "proto" }
