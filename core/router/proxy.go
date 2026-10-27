package router

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
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
// A launcher serves it to caller, with opts.
func (rt *Router) HostServer(caller string, opts ...grpc.ServerOption) *grpc.Server {
	opts = append(opts,
		grpc.UnknownServiceHandler(rt.proxy(caller)),
		grpc.ForceServerCodecV2(rawCodec{}),
	)
	return grpc.NewServer(opts...)
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
		if md.IsStreamingClient() || md.IsStreamingServer() {
			refused = cmp.Or(refused, status.Errorf(codes.Unimplemented, "%s streams; Components call each other unary only", md.FullName()))
		}
		var req frame
		if err := stream.RecvMsg(&req); err != nil {
			return err
		}
		ctx := stream.Context()
		actor, chain := forwarded(ctx, caller)
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
// up is it PermissionDenied.
func (rt *Router) route(caller, service string) (string, runtime.Endpoint, error) {
	var notUp error
	for _, name := range rt.components.ComponentRequires(caller) {
		ep, err := rt.components.Endpoint(name)
		if err != nil {
			if notUp == nil {
				notUp = err
			}
			continue
		}
		if string(ep.Service.FullName()) == service {
			return name, ep, nil
		}
	}
	if notUp != nil {
		return "", runtime.Endpoint{}, notUp
	}
	return "", runtime.Endpoint{}, status.Errorf(codes.PermissionDenied, "%s doesn't require a Component serving %s", caller, service)
}

// serving finds a started Component serving service, whichever requires
// it; "" when none does.
func (rt *Router) serving(service string) (string, runtime.Endpoint) {
	names := slices.Sorted(maps.Keys(rt.components.Status()))
	for _, name := range names {
		if ep, err := rt.components.Endpoint(name); err == nil && string(ep.Service.FullName()) == service {
			return name, ep
		}
	}
	return "", runtime.Endpoint{}
}

// forwarded returns the actor and chain the caller's SDK carried onto the
// call. Components are first-party, so they are trusted. A call without
// them is caller's own work on a new chain.
func forwarded(ctx context.Context, caller string) (auth.Actor, string) {
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

// rawCodec moves frames without decoding them.
type rawCodec struct{}

func (rawCodec) Marshal(v any) (mem.BufferSlice, error) {
	f, ok := v.(*frame)
	if !ok {
		return nil, fmt.Errorf("raw codec: can't marshal %T", v)
	}
	return mem.BufferSlice{mem.SliceBuffer(*f)}, nil
}

func (rawCodec) Unmarshal(data mem.BufferSlice, v any) error {
	f, ok := v.(*frame)
	if !ok {
		return fmt.Errorf("raw codec: can't unmarshal into %T", v)
	}
	*f = data.Materialize()
	return nil
}

func (rawCodec) Name() string { return "proto" }
