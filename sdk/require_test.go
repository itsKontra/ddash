package sdk_test

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/sdk"
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

func TestARequiredComponentWithoutAFakeIsUnavailable(t *testing.T) {
	h := sdktest.New(t, defineRelay())
	h.MustStart(&echopb.EchoConfig{})

	_, err := echopb.NewEchoServiceClient(h.Conn).Echo(t.Context(), &echopb.EchoRequest{Message: "hi"})

	if status.Code(err) != codes.Unavailable {
		t.Fatalf("err = %v, want Unavailable", err)
	}
}
