package sdk_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	componentpb "github.com/itsKontra/ddash/proto/ddash/component"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/inmem"
	"github.com/itsKontra/ddash/sdk/internal/echo/echopb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// relay is an echo that answers through the Component it requires.
type relay struct {
	echo
	upstream echopb.EchoServiceClient
}

func (r *relay) Start(ctx context.Context, env *sdk.Env, cfg *echopb.EchoConfig) error {
	r.upstream = sdk.Require(env, echopb.NewEchoServiceClient)
	return r.echo.Start(ctx, env, cfg)
}

func (r *relay) Echo(ctx context.Context, req *echopb.EchoRequest) (*echopb.EchoResponse, error) {
	return r.upstream.Echo(ctx, req)
}

// Watch answers with what the Component it requires answered.
func (r *relay) Watch(req *echopb.WatchRequest, stream grpc.ServerStreamingServer[echopb.WatchResponse]) error {
	resp, err := r.upstream.Echo(stream.Context(), &echopb.EchoRequest{Message: req.GetMessage()})
	if err != nil {
		return err
	}
	return stream.Send(&echopb.WatchResponse{Message: resp.GetMessage()})
}

func defineRelay() sdk.Definition {
	return sdk.Define(sdk.Spec[*echopb.EchoConfig]{
		Manifest: []byte(echoManifest),
		Service:  &echopb.EchoService_ServiceDesc,
		New:      func() sdk.Component[*echopb.EchoConfig] { return &relay{} },
	})
}

// fakeUpstream fakes the generated client of the required Component.
type fakeUpstream struct{ echopb.EchoServiceClient }

func (fakeUpstream) Echo(_ context.Context, req *echopb.EchoRequest, _ ...grpc.CallOption) (*echopb.EchoResponse, error) {
	return &echopb.EchoResponse{Message: "upstream says " + req.GetMessage()}, nil
}

func TestRequireReturnsTheHarnessFakeOfARequiredComponent(t *testing.T) {
	h := sdktest.New(t, defineRelay(), sdktest.WithClient[echopb.EchoServiceClient](fakeUpstream{}))
	h.MustStart(&echopb.EchoConfig{})

	resp, err := echopb.NewEchoServiceClient(h.Conn).Echo(t.Context(), &echopb.EchoRequest{Message: "hi"})

	if err != nil || resp.GetMessage() != "upstream says hi" {
		t.Fatalf("Echo = %q, %v", resp.GetMessage(), err)
	}
}

// hostEcho stands in for the host's proxy: it answers Echo and says who the
// call ran for.
type hostEcho struct {
	echopb.UnimplementedEchoServiceServer
}

func (hostEcho) Echo(ctx context.Context, req *echopb.EchoRequest) (*echopb.EchoResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	return &echopb.EchoResponse{Message: fmt.Sprintf("host says %s for %v", req.GetMessage(), md.Get(sdk.ActorMetadata))}, nil
}

// split is one go-plugin plugin made of the Component's side of one set and
// the host's side of another, as a subprocess and its host would be.
type split struct {
	plugin.Plugin
	component, host plugin.GRPCPlugin
}

func (s split) GRPCServer(b *plugin.GRPCBroker, srv *grpc.Server) error {
	return s.component.GRPCServer(b, srv)
}

func (s split) GRPCClient(ctx context.Context, b *plugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return s.host.GRPCClient(ctx, b, c)
}

// A Component run as a subprocess reaches the host over go-plugin's
// GRPCBroker, which the host serves when it dispenses the Component.
func TestASubprocessRequiresThroughTheHostOverGoPlugin(t *testing.T) {
	inst := sdk.NewInstance(defineRelay(), sdk.InstanceOptions{})
	t.Cleanup(func() { inst.Stop(context.Background()) })
	hostServer := func(opts ...grpc.ServerOption) *grpc.Server {
		s := grpc.NewServer(opts...)
		echopb.RegisterEchoServiceServer(s, hostEcho{})
		return s
	}
	ps := plugin.PluginSet{sdk.PluginName: split{
		component: sdk.Plugins(inst)[sdk.PluginName].(plugin.GRPCPlugin),
		host:      sdk.HostPlugins(hostServer)[sdk.PluginName].(plugin.GRPCPlugin),
	}}
	client, _ := plugin.TestPluginGRPCConn(t, true, ps)
	t.Cleanup(func() { client.Close() })
	raw, err := client.Dispense(sdk.PluginName)
	if err != nil {
		t.Fatal(err)
	}
	conn := raw.(grpc.ClientConnInterface)
	if _, err := componentpb.NewLifecycleServiceClient(conn).Start(t.Context(), &componentpb.StartRequest{}); err != nil {
		t.Fatal(err)
	}

	asAda := metadata.AppendToOutgoingContext(t.Context(), sdk.ActorMetadata, "operator:ada")
	resp, err := echopb.NewEchoServiceClient(conn).Echo(asAda, &echopb.EchoRequest{Message: "hi"})

	if want := "host says hi for [operator:ada]"; err != nil || resp.GetMessage() != want {
		t.Fatalf("Echo = %q, %v; want %q", resp.GetMessage(), err, want)
	}
}

// carried stands in for the host: it answers Echo with the actor and chain
// the call carried.
type carried struct {
	echopb.UnimplementedEchoServiceServer
}

func (carried) Echo(ctx context.Context, req *echopb.EchoRequest) (*echopb.EchoResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	return &echopb.EchoResponse{Message: fmt.Sprintf("%s for %v on %v", req.GetMessage(), md.Get(sdk.ActorMetadata), md.Get(sdk.ChainMetadata))}, nil
}

func TestAStreamedMethodCarriesItsActorAndChainOnward(t *testing.T) {
	host, stop, err := inmem.Serve("host", func(s grpc.ServiceRegistrar) { echopb.RegisterEchoServiceServer(s, carried{}) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	h := sdktest.New(t, defineRelay(), func(o *sdk.InstanceOptions) { o.Host = host })
	h.MustStart(&echopb.EchoConfig{})

	asAda := metadata.AppendToOutgoingContext(t.Context(), sdk.ActorMetadata, "operator:ada", sdk.ChainMetadata, "chain-1")
	stream, err := echopb.NewEchoServiceClient(h.Conn).Watch(asAda, &echopb.WatchRequest{Message: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := stream.Recv()

	if want := "hi for [operator:ada] on [chain-1]"; err != nil || resp.GetMessage() != want {
		t.Fatalf("Watch = %q, %v; want %q", resp.GetMessage(), err, want)
	}
}

func TestARequiredComponentWithoutAFakeIsUnavailable(t *testing.T) {
	h := sdktest.New(t, defineRelay())
	h.MustStart(&echopb.EchoConfig{})

	_, err := echopb.NewEchoServiceClient(h.Conn).Echo(t.Context(), &echopb.EchoRequest{Message: "hi"})

	if status.Code(err) != codes.Unavailable {
		t.Fatalf("err = %v, want Unavailable", err)
	}
}
