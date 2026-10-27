package router_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	_ "google.golang.org/protobuf/types/known/emptypb"

	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/router"
	"github.com/itsKontra/ddash/core/runtime"
	ddashpb "github.com/itsKontra/ddash/proto/ddash"
)

// streamer is a Component the SDK would refuse: it streams from the client,
// both ways, and from a getter.
type streamer struct {
	service protoreflect.ServiceDescriptor
}

func (s streamer) PluginRequires(string) ([]string, bool) { return []string{"streamer"}, true }
func (s streamer) ComponentRequires(string) []string      { return nil }
func (s streamer) Status() map[string]runtime.Status      { return nil }
func (s streamer) Endpoint(string) (runtime.Endpoint, error) {
	return runtime.Endpoint{Service: s.service, Types: dynamicpb.NewTypes(protoregistry.GlobalFiles)}, nil
}

func newStreamer(t *testing.T) streamer {
	t.Helper()
	action := &descriptorpb.MethodOptions{}
	proto.SetExtension(action, ddashpb.E_Action, &ddashpb.Action{})
	getter := &descriptorpb.MethodOptions{}
	proto.SetExtension(getter, ddashpb.E_Getter, &ddashpb.Getter{})
	empty := proto.String(".google.protobuf.Empty")
	file := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("streamer.proto"),
		Package:    proto.String("ddash.streamer"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/empty.proto"},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("StreamerService"),
			Method: []*descriptorpb.MethodDescriptorProto{
				{Name: proto.String("Upload"), InputType: empty, OutputType: empty, ClientStreaming: proto.Bool(true), Options: action},
				{Name: proto.String("Chat"), InputType: empty, OutputType: empty, ClientStreaming: proto.Bool(true), ServerStreaming: proto.Bool(true), Options: action},
				{Name: proto.String("Tail"), InputType: empty, OutputType: empty, ServerStreaming: proto.Bool(true), Options: getter},
			},
		}},
	}
	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	return streamer{fd.Services().Get(0)}
}

// Whichever content type it comes with, a call to a method streaming in a
// way the SDK refuses is Unimplemented, answered in that content type's form.
func TestAMethodStreamingAsTheSDKRefusesIsUnimplementedInEitherForm(t *testing.T) {
	rt := router.New(newStreamer(t), nil)
	for _, method := range []string{"Upload", "Chat", "Tail"} {
		unary := coretest.ConnectRequest("streamer", method, `{}`)
		unary.Header.Set(coretest.PluginHeader, "streamer")
		res := httptest.NewRecorder()
		rt.ServeHTTP(res, unary)
		var e struct{ Code string }
		if json.Unmarshal(res.Body.Bytes(), &e); res.Code != http.StatusNotImplemented || e.Code != "unimplemented" {
			t.Errorf("%s as unary: status %d, %s; want 501 unimplemented", method, res.Code, res.Body)
		}

		streamed := coretest.StreamRequest("streamer", method, `{}`)
		streamed.Header.Set(coretest.PluginHeader, "streamer")
		res = httptest.NewRecorder()
		rt.ServeHTTP(res, streamed)
		s, err := coretest.ReadStream(res.Body)
		if res.Code != http.StatusOK || err != nil || s.Error == nil || s.Error.Code != "unimplemented" {
			t.Errorf("%s as a stream: status %d, %+v (%v); want 200 and an unimplemented end-of-stream", method, res.Code, s, err)
		}
	}
}
