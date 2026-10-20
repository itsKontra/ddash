// Package router carries every UI call to a Component: it serves
// /api/c/<component>/<Method> as Connect unary JSON, transcoded to and from
// the Component's protobuf API with dynamicpb and protojson from the
// descriptors the Component's Describe returned.
package router

import (
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/itsKontra/ddash/core/runtime"
)

// Prefix is where the router is mounted.
const Prefix = "/api/c/"

// PluginHeader names the calling Plugin. The shell gives each Plugin its own
// transport, which sets it on every call.
const PluginHeader = "Ddash-Plugin"

// Components is the runtime as the router sees it.
type Components interface {
	// PluginRequires returns the Components an installed Plugin lists
	// directly in requires; ok is false when it isn't installed.
	PluginRequires(plugin string) (requires []string, ok bool)
	// Endpoint reaches a started Component, or says why it can't.
	Endpoint(component string) (runtime.Endpoint, error)
}

// Router is the http.Handler for Prefix.
type Router struct {
	components Components
}

// New returns a Router reaching components.
func New(components Components) *Router {
	return &Router{components: components}
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Connect unary calls are POST", http.StatusMethodNotAllowed)
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		http.Error(w, "Connect unary calls are application/json", http.StatusUnsupportedMediaType)
		return
	}
	component, method, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, Prefix), "/")
	if !ok {
		WriteError(w, status.Error(codes.NotFound, "want /api/c/<component>/<Method>"))
		return
	}
	if err := rt.inScope(r.Header.Get(PluginHeader), component); err != nil {
		WriteError(w, err)
		return
	}
	ep, err := rt.components.Endpoint(component)
	if err != nil {
		WriteError(w, err)
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
		WriteError(w, status.Errorf(codes.Unimplemented, "%s has no method %s", component, method))
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, status.Errorf(codes.InvalidArgument, "read request: %v", err))
		return
	}
	req := dynamicpb.NewMessage(md.Input())
	if len(body) > 0 {
		opts := protojson.UnmarshalOptions{DiscardUnknown: true, Resolver: ep.Types}
		if err := opts.Unmarshal(body, req); err != nil {
			WriteError(w, status.Errorf(codes.InvalidArgument, "decode %s: %v", md.Input().FullName(), err))
			return
		}
	}
	resp := dynamicpb.NewMessage(md.Output())
	fullMethod := "/" + string(ep.Service.FullName()) + "/" + string(md.Name())
	if err := ep.Conn.Invoke(r.Context(), fullMethod, req, resp); err != nil {
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
