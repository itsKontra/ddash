// Package router carries every call that reaches a Component. UI calls
// arrive at /api/c/<component>/<Method> as Connect unary JSON, or Connect
// server-streaming JSON for an Action that streams, transcoded to and from
// the Component's protobuf API with dynamicpb and protojson from the
// descriptors the Component's Describe returned. Calls between
// Components arrive through HostServer, a transparent gRPC proxy. Either
// way the router enforces requires, sets the deadline, carries the actor
// and chain id onward, and passes Actions through the gate.
package router

import (
	"cmp"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// Prefix is where the router is mounted.
const Prefix = "/api/c/"

// PluginHeader names the calling Plugin. The shell gives each Plugin its own
// transport, which sets it on every call.
const PluginHeader = "Ddash-Plugin"

// maxRequestBytes bounds a request body; Component requests are small.
const maxRequestBytes = 1 << 20

// Components is the runtime as the router sees it.
type Components interface {
	// PluginRequires returns the Components an installed Plugin lists
	// directly in requires; ok is false when it isn't installed.
	PluginRequires(plugin string) (requires []string, ok bool)
	// Endpoint reaches a started Component, or says why it can't.
	Endpoint(component string) (runtime.Endpoint, error)
	// ComponentRequires returns the Components a Component lists in requires.
	ComponentRequires(component string) []string
	// Status returns every Component the runtime knows, by name.
	Status() map[string]runtime.Status
}

// Router is the http.Handler for Prefix.
type Router struct {
	components Components
	gate       *appstate.Gate
}

// New returns a Router reaching components, passing every Action through
// gate.
func New(components Components, gate *appstate.Gate) *Router {
	return &Router{components: components, gate: gate}
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Connect calls are POST", http.StatusMethodNotAllowed)
		return
	}
	// The content type picks the form of the answer, refusals included:
	// connect-web reads a stream's error only from its end-of-stream.
	var streaming bool
	switch mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt {
	case "application/json":
	case streamJSON:
		streaming = true
	default:
		http.Error(w, "Connect calls are application/json, or "+streamJSON+" to stream", http.StatusUnsupportedMediaType)
		return
	}
	fail := func(err error) { WriteError(w, err) }
	if streaming {
		fail = func(err error) { newStream(w).end(err) }
	}
	component, method, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, Prefix), "/")
	if !ok {
		fail(status.Error(codes.NotFound, "want /api/c/<component>/<Method>"))
		return
	}
	// A refusal for scope or an undecodable request still passes an Action
	// through the gate, as a failed entry; so the method is resolved first.
	refused := rt.inScope(r.Header.Get(PluginHeader), component)
	ep, err := rt.components.Endpoint(component)
	if err != nil {
		fail(cmp.Or(refused, err))
		return
	}
	// Stock connect-web clients with base URL /api/c/<component> ask for
	// <package.Service>/<Method>.
	var md protoreflect.MethodDescriptor
	if svc, name, long := strings.Cut(method, "/"); !long {
		md = ep.Service.Methods().ByName(protoreflect.Name(method))
	} else if svc == string(ep.Service.FullName()) {
		md = ep.Service.Methods().ByName(protoreflect.Name(name))
	}
	if md == nil {
		fail(cmp.Or(refused, status.Errorf(codes.Unimplemented, "%s has no method %s", component, method)))
		return
	}
	// As connect-go does, a method is called in its own kind's form only.
	if streams := md.IsStreamingClient() || md.IsStreamingServer(); streams != streaming {
		want := "application/json"
		if streams {
			want = streamJSON
		}
		http.Error(w, fmt.Sprintf("%s: call it with %s", md.FullName(), want), http.StatusUnsupportedMediaType)
		return
	}
	// The SDK lets only Actions stream, and only from the server.
	if md.IsStreamingClient() || (streaming && !appstate.IsAction(md)) {
		fail(status.Errorf(codes.Unimplemented, "%s streams, but only Actions stream, and only from the server", md.FullName()))
		return
	}
	limit := int64(maxRequestBytes)
	if streaming {
		limit += 5 // the envelope's header
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err == nil && streaming {
		body, err = readEnvelope(body)
	}
	if err != nil {
		fail(status.Errorf(codes.InvalidArgument, "read request: %v", err))
		return
	}
	req := dynamicpb.NewMessage(md.Input())
	if len(body) > 0 {
		opts := protojson.UnmarshalOptions{DiscardUnknown: true, Resolver: ep.Types}
		if err := opts.Unmarshal(body, req); err != nil {
			// Audited with an empty request.
			refused = cmp.Or(refused, status.Errorf(codes.InvalidArgument, "decode %s: %v", md.Input().FullName(), err))
			req = dynamicpb.NewMessage(md.Input())
		}
	}
	actor, ok := auth.ActorFrom(r.Context())
	if !ok {
		fail(status.Error(codes.Unauthenticated, "sign in first"))
		return
	}
	chain := NewChain()
	done, err := rt.gate.Admit(r.Context(), actor, appstate.Action{Chain: chain, Target: component, Method: md, Request: req})
	if err != nil {
		fail(err)
		return
	}
	if refused != nil {
		done(refused)
		fail(refused)
		return
	}
	fullMethod := "/" + string(ep.Service.FullName()) + "/" + string(md.Name())
	ctx, cancel := callContext(onward(r.Context(), actor, chain), md)
	defer cancel()
	if streaming {
		// The whole stream runs under the Action's deadline, and the
		// handler returns only once it ended, so shutdown waits for it.
		s := newStream(w)
		err = relay(ctx, s, ep, fullMethod, md, req)
		done(err)
		s.end(err)
		return
	}
	resp := dynamicpb.NewMessage(md.Output())
	err = ep.Conn.Invoke(ctx, fullMethod, req, resp)
	done(err)
	if err != nil {
		WriteError(w, err)
		return
	}
	out, err := protojson.MarshalOptions{Resolver: ep.Types}.Marshal(resp)
	if err != nil {
		WriteError(w, status.Errorf(codes.Internal, "encode %s: %v", md.Output().FullName(), err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

// NewChain returns a new chain id, for an Action that starts a chain.
func NewChain() string { return rand.Text() }

// onward returns ctx carrying actor and chain as outgoing metadata, for the
// Component the call goes to.
func onward(ctx context.Context, actor auth.Actor, chain string) context.Context {
	kv := []string{sdk.ActorMetadata, actor.String(), sdk.ChainMetadata, chain}
	if actor.Sub != "" {
		kv = append(kv, sdk.SubMetadata, actor.Sub)
	}
	return metadata.AppendToOutgoingContext(ctx, kv...)
}

const (
	// GetterDeadline bounds every call to a getter.
	GetterDeadline = 10 * time.Second
	// ActionDeadline bounds every call to an Action.
	ActionDeadline = 60 * time.Second
)

// Detach returns the context an admitted Action runs under: ctx's values
// (actor, chain) without its cancellation, ending after ActionDeadline. An
// Action runs to its end whatever its caller does (ADR-0004).
func Detach(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), ActionDeadline)
}

// callContext returns the context a call to md runs under: detached for an
// Action, and for a getter ctx bounded by GetterDeadline.
func callContext(ctx context.Context, md protoreflect.MethodDescriptor) (context.Context, context.CancelFunc) {
	if appstate.IsAction(md) {
		return Detach(ctx)
	}
	return context.WithTimeout(ctx, GetterDeadline)
}

// inScope refuses a call unless plugin is installed and lists component
// directly in its requires. Plugins are first-party, so this guards against
// mistakes, not attacks.
func (rt *Router) inScope(plugin, component string) error {
	if plugin == "" {
		return status.Errorf(codes.PermissionDenied, "no %s header names the calling Plugin", PluginHeader)
	}
	requires, ok := rt.components.PluginRequires(plugin)
	if !ok {
		return status.Errorf(codes.PermissionDenied, "plugin %s isn't installed", plugin)
	}
	if !slices.Contains(requires, component) {
		return status.Errorf(codes.PermissionDenied, "plugin %s doesn't require %s", plugin, component)
	}
	return nil
}
