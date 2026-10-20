package sdk

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	componentpb "github.com/itsKontra/ddash/proto/ddash/component"
	"github.com/itsKontra/ddash/sdk/migrate"
)

// InstanceOptions configure one running Component.
type InstanceOptions struct {
	// DBPath is the Component's SQLite file. Unused without migrations.
	DBPath string
	// PublicURL is where the Operator reaches ddash, passed on in Env when
	// the Start request carries none.
	PublicURL string
	// Logger receives the Component's logs. Nil discards them.
	Logger *slog.Logger
	// Host is where Require sends calls to other Components. Nil makes
	// them fail with Unavailable.
	Host grpc.ClientConnInterface
	// Clients are values Require returns instead of dialing Host, matched
	// by the generated client interface they implement. Tests use them as
	// fakes of required Components.
	Clients []any
	// Release is the Release Describe reports. Default Release, the one
	// built in; the host's in-process launcher sets the one it runs.
	Release string
}

// Instance is one running Component: the lifecycle the host drives plus the
// Component's API. sdk.Serve runs one per process; the host's in-process
// launcher and sdktest run them over in-memory gRPC.
type Instance struct {
	componentpb.UnimplementedLifecycleServiceServer
	def  Definition
	opts InstanceOptions

	// ctx lives until Stop; it is the ctx the Component's Start gets.
	ctx    context.Context
	cancel context.CancelFunc
	loops  *loops

	mu       sync.Mutex
	starting bool  // Start was called
	startErr error // why Start failed, if it did
	db       *sql.DB

	ready atomic.Pointer[readyComponent] // set once Start succeeded
}

type readyComponent struct{ c component }

// NewInstance prepares def to serve. Nothing runs until the host calls Start.
func NewInstance(def Definition, opts InstanceOptions) *Instance {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	l := &loops{log: opts.Logger}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), loopsKey{}, l))
	return &Instance{def: def, opts: opts, ctx: ctx, cancel: cancel, loops: l}
}

// Register adds the lifecycle service and the Component's API to s. The
// API answers FailedPrecondition "not configured" until Start succeeded.
func (i *Instance) Register(s grpc.ServiceRegistrar) {
	componentpb.RegisterLifecycleServiceServer(s, i)

	api := *i.def.service
	api.HandlerType = (*any)(nil)
	api.Methods = make([]grpc.MethodDesc, len(i.def.service.Methods))
	for n, m := range i.def.service.Methods {
		handler := m.Handler
		api.Methods[n] = grpc.MethodDesc{
			MethodName: m.MethodName,
			Handler: func(_ any, ctx context.Context, dec func(any) error, ic grpc.UnaryServerInterceptor) (any, error) {
				r := i.ready.Load()
				if r == nil {
					return nil, status.Error(codes.FailedPrecondition, "not configured")
				}
				return handler(r.c.impl(), fromHost(ctx), dec, ic)
			},
		}
	}
	s.RegisterService(&api, struct{}{})
}

func (i *Instance) Describe(context.Context, *componentpb.DescribeRequest) (*componentpb.DescribeResponse, error) {
	d, err := i.def.describe()
	if err == nil && i.opts.Release != "" {
		d.Release = i.opts.Release
	}
	return d, err
}

func (i *Instance) Start(_ context.Context, req *componentpb.StartRequest) (*componentpb.StartResponse, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.starting {
		return nil, status.Error(codes.FailedPrecondition, "already started; restart the process to start again")
	}
	i.starting = true

	if err := i.start(req); err != nil {
		i.startErr = err
		return nil, err
	}
	return &componentpb.StartResponse{}, nil
}

func (i *Instance) start(req *componentpb.StartRequest) error {
	cfg := i.def.newConfig()
	if err := proto.Unmarshal(req.GetConfig(), cfg); err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid config: %v", err)
	}
	publicURL := req.GetPublicUrl()
	if publicURL == "" {
		publicURL = i.opts.PublicURL
	}
	env := &Env{
		Name:      i.def.Name(),
		PublicURL: publicURL,
		Log:       i.opts.Logger,
		host:      i.opts.Host,
		clients:   i.opts.Clients,
	}
	if i.def.migrations != nil {
		db, err := i.openDB()
		if err != nil {
			return err
		}
		env.DB = db
	}
	c := i.def.newImpl()
	if err := c.start(i.ctx, env, cfg); err != nil {
		return err
	}
	i.ready.Store(&readyComponent{c})
	return nil
}

func (i *Instance) Health(ctx context.Context, _ *componentpb.HealthRequest) (*componentpb.HealthResponse, error) {
	r := i.ready.Load()
	if r == nil {
		i.mu.Lock()
		startErr := i.startErr
		i.mu.Unlock()
		if startErr == nil || FieldErrorsOf(startErr) != nil {
			return nil, status.Error(codes.FailedPrecondition, "not configured")
		}
		return Health{State: Down, Message: status.Convert(startErr).Message()}.proto(), nil
	}
	return worst(r.c.Health(ctx), i.loops.health()).proto(), nil
}

// worst returns the worst of hs, the first of equals.
func worst(hs ...Health) Health {
	w := hs[0]
	for _, h := range hs[1:] {
		if h.State > w.State {
			w = h
		}
	}
	return w
}

// openDB opens the Component's SQLite file and applies its migrations. Any
// failure is a startup error: the Component is down until the Release changes.
func (i *Instance) openDB() (*sql.DB, error) {
	db, err := migrate.Open(i.opts.DBPath)
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(i.def.migrations, "migrations")
	if err == nil {
		err = migrate.Apply(i.ctx, db, sub)
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	i.db = db
	return db, nil
}

// Stop cancels the Component's ctx, waits for its background work until ctx
// ends, and closes its DB.
func (i *Instance) Stop(ctx context.Context) {
	i.cancel()
	done := make(chan struct{})
	go func() {
		i.loops.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		i.opts.Logger.Warn("stopped before background loops finished")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.db != nil {
		i.db.Close()
		i.db = nil
	}
}
