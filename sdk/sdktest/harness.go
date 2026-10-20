// Package sdktest runs one Component's proto API in a test: a real
// sdk.Instance over in-memory gRPC, with a temp DB and fakes of the
// generated clients of the Components it requires.
//
//	h := sdktest.New(t, mycomponent.Definition(), sdktest.WithClient[checkspb.ChecksServiceClient](fake))
//	h.MustStart(&mypb.MyConfig{...})
//	client := mypb.NewMyServiceClient(h.Conn)
//
// It works inside a testing/synctest bubble.
package sdktest

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	componentpb "github.com/itsKontra/ddash/proto/ddash/component"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/inmem"
)

// Harness is one running Component.
type Harness struct {
	// Conn reaches the Component's API, e.g. mypb.NewMyServiceClient(h.Conn).
	Conn *grpc.ClientConn
	// Lifecycle is the host's view: Describe, Start and Health.
	Lifecycle componentpb.LifecycleServiceClient
	// DBPath is the Component's SQLite file.
	DBPath string

	t    testing.TB
	inst *sdk.Instance
}

// PublicURL is the public URL every harnessed Component starts with.
const PublicURL = "https://ddash.home.example"

// Option configures a Harness.
type Option func(*sdk.InstanceOptions)

// WithDB makes the Component use the SQLite file at path instead of a fresh one.
func WithDB(path string) Option {
	return func(o *sdk.InstanceOptions) { o.DBPath = path }
}

// WithClient makes sdk.Require return fake for the generated client
// interface T, e.g. WithClient[checkspb.ChecksServiceClient](fake).
func WithClient[T any](fake T) Option {
	return func(o *sdk.InstanceOptions) { o.Clients = append(o.Clients, fake) }
}

// New serves def over in-memory gRPC until the test ends. The Component is
// described but not started; call Start or MustStart.
func New(t testing.TB, def sdk.Definition, opts ...Option) *Harness {
	t.Helper()
	o := sdk.InstanceOptions{
		DBPath: filepath.Join(t.TempDir(), def.Name()+".db"),
		Logger: slog.New(slog.NewTextHandler(t.Output(), nil)).With("component", def.Name()),
	}
	for _, opt := range opts {
		opt(&o)
	}
	inst := sdk.NewInstance(def, o)

	conn, stop, err := inmem.Serve(def.Name(), inst.Register)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop()
		inst.Stop(context.Background())
	})
	return &Harness{
		Conn:      conn,
		Lifecycle: componentpb.NewLifecycleServiceClient(conn),
		DBPath:    o.DBPath,
		t:         t,
		inst:      inst,
	}
}

// Start starts the Component with cfg, as the host would.
func (h *Harness) Start(cfg proto.Message) error {
	h.t.Helper()
	b, err := proto.Marshal(cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	_, err = h.Lifecycle.Start(h.t.Context(), &componentpb.StartRequest{Config: b, PublicUrl: PublicURL})
	return err
}

// MustStart starts the Component with cfg and fails the test if it doesn't start.
func (h *Harness) MustStart(cfg proto.Message) {
	h.t.Helper()
	if err := h.Start(cfg); err != nil {
		h.t.Fatalf("Start: %v", err)
	}
}

// Health asks the Component for its health, failing the test if it can't answer.
func (h *Harness) Health() sdk.Health {
	h.t.Helper()
	r, err := h.Lifecycle.Health(h.t.Context(), &componentpb.HealthRequest{})
	if err != nil {
		h.t.Fatalf("Health: %v", err)
	}
	return sdk.HealthFromProto(r)
}

// Stop stops the Component as the host would: its ctx is cancelled and its
// background work ends. The test's cleanup does this too.
func (h *Harness) Stop() {
	h.inst.Stop(context.Background())
}
