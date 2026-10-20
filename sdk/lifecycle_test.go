package sdk_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"

	componentpb "github.com/itsKontra/ddash/proto/ddash/component"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/internal/echo/echopb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

const echoManifest = `
name: echo
description: Echoes messages back, for the SDK's own tests.
requires: [upstream]
`

// echo is a Component built from the test's hooks.
type echo struct {
	echopb.UnimplementedEchoServiceServer
	start  func(ctx context.Context, env *sdk.Env, cfg *echopb.EchoConfig) error
	health func(ctx context.Context) sdk.Health
	prefix string
}

func (e *echo) Start(ctx context.Context, env *sdk.Env, cfg *echopb.EchoConfig) error {
	e.prefix = cfg.GetPrefix()
	if e.start != nil {
		return e.start(ctx, env, cfg)
	}
	return nil
}

func (e *echo) Health(ctx context.Context) sdk.Health {
	if e.health != nil {
		return e.health(ctx)
	}
	return sdk.Health{State: sdk.OK}
}

func (e *echo) Echo(_ context.Context, req *echopb.EchoRequest) (*echopb.EchoResponse, error) {
	return &echopb.EchoResponse{Message: e.prefix + req.GetMessage()}, nil
}

func define(e *echo) sdk.Definition {
	return sdk.Define(sdk.Spec[*echopb.EchoConfig]{
		Manifest: []byte(echoManifest),
		Service:  &echopb.EchoService_ServiceDesc,
		New:      func() sdk.Component[*echopb.EchoConfig] { return e },
	})
}

func TestDescribeSaysWhatTheComponentIs(t *testing.T) {
	h := sdktest.New(t, define(&echo{}))

	d, err := h.Lifecycle.Describe(t.Context(), &componentpb.DescribeRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if d.GetName() != "echo" || d.GetRelease() != "dev" {
		t.Errorf("name, release = %q, %q; want echo, dev", d.GetName(), d.GetRelease())
	}
	if !slices.Equal(d.GetRequires(), []string{"upstream"}) {
		t.Errorf("requires = %v, want [upstream]", d.GetRequires())
	}
	if d.GetService() != "ddash.echo.EchoService" {
		t.Errorf("service = %q", d.GetService())
	}
	if d.GetConfigMessage() != "ddash.echo.EchoConfig" {
		t.Errorf("config message = %q", d.GetConfigMessage())
	}
}

func TestDescribeCarriesDescriptorsWithTheirImports(t *testing.T) {
	h := sdktest.New(t, define(&echo{}))

	d, err := h.Lifecycle.Describe(t.Context(), &componentpb.DescribeRequest{})
	if err != nil {
		t.Fatal(err)
	}

	// NewFiles fails unless every import precedes the file importing it.
	files, err := protodesc.NewFiles(d.GetDescriptors())
	if err != nil {
		t.Fatalf("descriptors don't resolve on their own: %v", err)
	}
	for _, name := range []string{"ddash.echo.EchoService", "ddash.echo.EchoConfig", "ddash.Getter"} {
		if _, err := files.FindDescriptorByName(protoreflect.FullName(name)); err != nil {
			t.Errorf("%s missing from descriptors: %v", name, err)
		}
	}
}

type unmarked struct {
	echopb.UnimplementedUnmarkedServiceServer
}

func (unmarked) Start(context.Context, *sdk.Env, *echopb.EchoConfig) error { return nil }
func (unmarked) Health(context.Context) sdk.Health                         { return sdk.Health{State: sdk.OK} }

func TestAMethodThatIsNeitherGetterNorActionIsRefused(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(fmt.Sprint(r), "Unmarked") {
			t.Fatalf("Define recovered %v, want a panic naming the method", r)
		}
	}()

	sdk.Define(sdk.Spec[*echopb.EchoConfig]{
		Manifest: []byte(echoManifest),
		Service:  &echopb.UnmarkedService_ServiceDesc,
		New:      func() sdk.Component[*echopb.EchoConfig] { return unmarked{} },
	})
}

type hinted[C proto.Message] struct {
	echopb.UnimplementedEchoServiceServer
}

func (hinted[C]) Start(context.Context, *sdk.Env, C) error { return nil }
func (hinted[C]) Health(context.Context) sdk.Health        { return sdk.Health{State: sdk.OK} }

func TestASetupHintFromAnythingButAGetterAnsweringOneIsRefused(t *testing.T) {
	refused := func(t *testing.T, define func()) {
		t.Helper()
		defer func() {
			if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "setup_from") {
				t.Errorf("Define recovered %v, want a panic naming setup_from", r)
			}
		}()
		define()
	}

	refused(t, func() {
		sdk.Define(sdk.Spec[*echopb.HintFromActionConfig]{
			Manifest: []byte(echoManifest),
			Service:  &echopb.EchoService_ServiceDesc,
			New:      func() sdk.Component[*echopb.HintFromActionConfig] { return &hinted[*echopb.HintFromActionConfig]{} },
		})
	})
	refused(t, func() {
		sdk.Define(sdk.Spec[*echopb.HintFromNoHintConfig]{
			Manifest: []byte(echoManifest),
			Service:  &echopb.EchoService_ServiceDesc,
			New:      func() sdk.Component[*echopb.HintFromNoHintConfig] { return &hinted[*echopb.HintFromNoHintConfig]{} },
		})
	})
}

func TestATestActionThatIsNoPlainActionIsRefused(t *testing.T) {
	refused := func(t *testing.T, define func()) {
		t.Helper()
		defer func() {
			if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "test_action") {
				t.Errorf("Define recovered %v, want a panic naming test_action", r)
			}
		}()
		define()
	}

	refused(t, func() {
		sdk.Define(sdk.Spec[*echopb.TestFromGetterConfig]{
			Manifest: []byte(echoManifest),
			Service:  &echopb.EchoService_ServiceDesc,
			New:      func() sdk.Component[*echopb.TestFromGetterConfig] { return &hinted[*echopb.TestFromGetterConfig]{} },
		})
	})
	refused(t, func() {
		sdk.Define(sdk.Spec[*echopb.TestFromDisruptiveConfig]{
			Manifest: []byte(echoManifest),
			Service:  &echopb.EchoService_ServiceDesc,
			New:      func() sdk.Component[*echopb.TestFromDisruptiveConfig] { return &hinted[*echopb.TestFromDisruptiveConfig]{} },
		})
	})
}

func TestMethodsAreNotConfiguredBeforeStart(t *testing.T) {
	h := sdktest.New(t, define(&echo{}))

	_, err := echopb.NewEchoServiceClient(h.Conn).Echo(t.Context(), &echopb.EchoRequest{Message: "hi"})

	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "not configured" {
		t.Fatalf("err = %v, want FailedPrecondition: not configured", err)
	}
}

func TestStartHandsTheComponentItsConfig(t *testing.T) {
	h := sdktest.New(t, define(&echo{}))

	h.MustStart(&echopb.EchoConfig{Prefix: "> "})

	resp, err := echopb.NewEchoServiceClient(h.Conn).Echo(t.Context(), &echopb.EchoRequest{Message: "hi"})
	if err != nil || resp.GetMessage() != "> hi" {
		t.Fatalf("Echo = %q, %v; want \"> hi\"", resp.GetMessage(), err)
	}
}

func TestStartGivesTheComponentItsPublicURLAndAStopCancelledContext(t *testing.T) {
	var gotEnv *sdk.Env
	var lifetime context.Context
	inst := &echo{start: func(ctx context.Context, env *sdk.Env, _ *echopb.EchoConfig) error {
		gotEnv, lifetime = env, ctx
		return nil
	}}
	h := sdktest.New(t, define(inst))

	h.MustStart(&echopb.EchoConfig{})

	if gotEnv.Name != "echo" || gotEnv.PublicURL != sdktest.PublicURL {
		t.Errorf("env name, public URL = %q, %q", gotEnv.Name, gotEnv.PublicURL)
	}
	if lifetime.Err() != nil {
		t.Fatal("ctx ended when the Start call returned; it must live until Stop")
	}
	h.Stop()
	if lifetime.Err() == nil {
		t.Error("ctx still live after Stop")
	}
}

func TestStartReturnsInvalidConfigWithErrorsKeyedByPath(t *testing.T) {
	inst := &echo{start: func(_ context.Context, _ *sdk.Env, cfg *echopb.EchoConfig) error {
		var errs sdk.FieldErrors
		if cfg.GetPrefix() == "" {
			errs.Add("prefix", "is required")
		}
		errs.Add("other[1].url", "is not a %s", "URL")
		return errs.Err()
	}}
	h := sdktest.New(t, define(inst))

	err := h.Start(&echopb.EchoConfig{})

	if status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != "invalid config" {
		t.Fatalf("err = %v, want InvalidArgument: invalid config", err)
	}
	want := sdk.FieldErrors{{Path: "prefix", Message: "is required"}, {Path: "other[1].url", Message: "is not a URL"}}
	if got := sdk.FieldErrorsOf(err); !slices.Equal(got, want) {
		t.Errorf("field errors = %v, want %v", got, want)
	}
	if _, err := echopb.NewEchoServiceClient(h.Conn).Echo(t.Context(), &echopb.EchoRequest{}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("after an invalid config, Echo err = %v, want not configured", err)
	}
}

func TestAStartupErrorIsDownWithItsReason(t *testing.T) {
	inst := &echo{start: func(context.Context, *sdk.Env, *echopb.EchoConfig) error {
		return errors.New("generate key: no entropy")
	}}
	h := sdktest.New(t, define(inst))

	if err := h.Start(&echopb.EchoConfig{}); err == nil {
		t.Fatal("Start succeeded")
	}

	if got := h.Health(); got.State != sdk.Down || got.Message != "generate key: no entropy" {
		t.Errorf("health = %v, want down: generate key: no entropy", got)
	}
}

func TestHealthIsNotConfiguredBeforeStart(t *testing.T) {
	h := sdktest.New(t, define(&echo{}))

	_, err := h.Lifecycle.Health(t.Context(), &componentpb.HealthRequest{})

	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("err = %v, want FailedPrecondition", err)
	}
}

func TestHealthIsTheComponentsOwn(t *testing.T) {
	inst := &echo{health: func(context.Context) sdk.Health {
		return sdk.Health{State: sdk.Degraded, Message: "media unreachable"}
	}}
	h := sdktest.New(t, define(inst))
	h.MustStart(&echopb.EchoConfig{})

	if got := h.Health(); got != (sdk.Health{State: sdk.Degraded, Message: "media unreachable"}) {
		t.Errorf("health = %v", got)
	}
}

func TestStartRunsOnlyOnce(t *testing.T) {
	h := sdktest.New(t, define(&echo{}))
	h.MustStart(&echopb.EchoConfig{})

	err := h.Start(&echopb.EchoConfig{})

	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("second Start: err = %v, want FailedPrecondition", err)
	}
}
