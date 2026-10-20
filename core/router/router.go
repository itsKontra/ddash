// Package router carries every UI call to a Component: it serves
// /api/c/<component>/<Method> as Connect unary JSON, transcoded to and from
// the Component's protobuf API with dynamicpb and protojson from the
// descriptors the Component's Describe returned.
package router

import (
	"io"
	"net/http"
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

// Components is the runtime as the router sees it.
type Components interface {
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
	component, method, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, Prefix), "/")
	if !ok {
		WriteError(w, status.Error(codes.NotFound, "want /api/c/<component>/<Method>"))
		return
	}
	ep, err := rt.components.Endpoint(component)
	if err != nil {
		WriteError(w, err)
		return
	}
	md := ep.Service.Methods().ByName(protoreflect.Name(method))
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
	fullMethod := "/" + string(ep.Service.FullName()) + "/" + method
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
