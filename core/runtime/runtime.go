// Package runtime runs the installed Plugins' Components: it keeps the
// installed set, reads the Release's catalogue, resolves requires, and runs
// one reconcile loop that launches, starts, health-polls and stops
// Components. The router reaches them through Endpoint.
package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/itsKontra/ddash/core/config"
	componentpb "github.com/itsKontra/ddash/proto/ddash/component"
	"github.com/itsKontra/ddash/sdk"
)

// State is the host's view of a Component: the Component's own ok, degraded
// or down, plus the states only the host knows.
type State string

const (
	// Starting: the Component is wanted and is being launched or started,
	// or it started and hasn't answered a Health poll yet.
	Starting State = "starting"
	OK       State = "ok"
	Degraded State = "degraded"
	Down     State = "down"
	// Unconfigured: the Component has no valid config. Its methods return
	// FailedPrecondition "not configured".
	Unconfigured State = "unconfigured"
	// Stopped: the Component no longer runs.
	Stopped State = "stopped"
)

// Status is a Component's State with a one-line message.
type Status struct {
	State   State
	Message string
}

const (
	// HealthInterval is how often every running Component is asked for its
	// health. Each one is also asked right after its Start.
	HealthInterval = 30 * time.Second
	// StopTimeout is how long Stop waits for Components to finish.
	StopTimeout = 10 * time.Second

	callTimeout  = 10 * time.Second // Describe and Health
	startTimeout = 60 * time.Second
	retryMin     = time.Second
	retryMax     = 5 * time.Minute
)

// Options configure a Runtime.
type Options struct {
	// DB is the core DB, holding the installed set.
	DB *sql.DB
	// Config holds each Component's config.
	Config *config.Store
	// Release provides the Release's catalogue.
	Release ReleaseSource
	// Launcher starts Component processes.
	Launcher Launcher
	// DataDir is /data: Component DBs go to <DataDir>/db/<name>.db.
	DataDir string
	// PublicURL is where the Operator reaches ddash, passed to every Start.
	PublicURL string
	// HostServer returns the server through which caller reaches the
	// Components it requires (router.HostServer). Nil leaves Components
	// without a host to call.
	HostServer func(caller string, opts ...grpc.ServerOption) *grpc.Server
	// Logger receives the runtime's logs. Nil discards them.
	Logger *slog.Logger
}

// Runtime runs the installed Plugins' Components.
type Runtime struct {
	opts Options
	log  *slog.Logger
	cat  Catalogue

	wake   chan struct{}
	cancel context.CancelFunc
	loops  sync.WaitGroup

	mu         sync.Mutex
	installed  map[string]bool
	components map[string]*component
}

// component is one wanted Component.
type component struct {
	proc     Process // nil until launched
	endpoint Endpoint
	started  bool // Start succeeded
	status   Status
}

// Endpoint is how the router reaches one running Component.
type Endpoint struct {
	// Conn reaches the Component's API.
	Conn grpc.ClientConnInterface
	// Service is the Component's API, from its Describe.
	Service protoreflect.ServiceDescriptor
	// Types resolves the message types in the Component's descriptors.
	Types *dynamicpb.Types
}

// New reads the Release's catalogue and the installed set. Nothing runs
// until Start.
func New(ctx context.Context, opts Options) (*Runtime, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	cat, err := opts.Release.Catalogue(ctx)
	if err != nil {
		return nil, fmt.Errorf("read the Release's catalogue: %w", err)
	}
	r := &Runtime{
		opts:       opts,
		log:        opts.Logger,
		cat:        cat,
		wake:       make(chan struct{}, 1),
		installed:  map[string]bool{},
		components: map[string]*component{},
	}
	rows, err := opts.DB.QueryContext(ctx, `SELECT name FROM installed_plugin`)
	if err != nil {
		return nil, fmt.Errorf("read installed Plugins: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		r.installed[name] = true
	}
	return r, rows.Err()
}

// Start runs the reconcile loop in the background until Stop.
func (r *Runtime) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.loops.Go(func() { r.reconcileLoop(ctx) })
	r.loops.Go(func() { r.healthLoop(ctx) })
	r.poke()
}

// healthLoop polls every launched Component's health each HealthInterval.
func (r *Runtime) healthLoop(ctx context.Context) {
	ticker := time.NewTicker(HealthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		r.mu.Lock()
		var names []string
		for name, c := range r.components {
			if c.proc != nil {
				names = append(names, name)
			}
		}
		r.mu.Unlock()
		for _, name := range names {
			r.loops.Go(func() { r.pollHealth(ctx, name) })
		}
	}
}

// Stop ends the reconcile loop, then stops every Component in parallel,
// waiting at most StopTimeout for them.
func (r *Runtime) Stop(ctx context.Context) {
	if r.cancel != nil {
		r.cancel()
	}
	r.loops.Wait()

	ctx, cancel := context.WithTimeout(ctx, StopTimeout)
	defer cancel()
	r.mu.Lock()
	var wg sync.WaitGroup
	for _, c := range r.components {
		if c.proc != nil {
			wg.Go(func() { c.proc.Stop(ctx) })
		}
		c.started = false
		c.status = Status{State: Stopped}
	}
	r.mu.Unlock()
	wg.Wait()
}

// Install adds plugin to the installed set and wakes the reconcile loop to
// start the Components it requires.
func (r *Runtime) Install(ctx context.Context, plugin string) error {
	if _, ok := r.cat.plugin(plugin); !ok {
		return status.Errorf(codes.NotFound, "plugin %s isn't in Release %s", plugin, r.cat.Release)
	}
	_, err := r.opts.DB.ExecContext(ctx, `
		INSERT INTO installed_plugin (name, release, installed_at) VALUES (?, ?, ?)
		ON CONFLICT (name) DO NOTHING`,
		plugin, r.cat.Release, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("install %s: %w", plugin, err)
	}
	r.mu.Lock()
	r.installed[plugin] = true
	r.mu.Unlock()
	r.poke()
	return nil
}

// PluginRequires returns the Components an installed Plugin lists directly
// in requires. ok is false when the Plugin isn't installed.
func (r *Runtime) PluginRequires(plugin string) (requires []string, ok bool) {
	r.mu.Lock()
	installed := r.installed[plugin]
	r.mu.Unlock()
	p, inCatalogue := r.cat.plugin(plugin)
	if !installed || !inCatalogue {
		return nil, false
	}
	return p.Requires, true
}

// ComponentRequires returns the Components a Component lists in requires.
func (r *Runtime) ComponentRequires(component string) []string {
	c, _ := r.cat.component(component)
	return c.Requires
}

// Status returns the state of every wanted Component, by name.
func (r *Runtime) Status() map[string]Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]Status, len(r.components))
	for name, c := range r.components {
		out[name] = c.status
	}
	return out
}

// Endpoint returns how to reach a started Component. Otherwise the error
// says why calls can't reach it: FailedPrecondition "not configured" for an
// unconfigured Component, and Unavailable for any other that isn't up.
func (r *Runtime) Endpoint(name string) (Endpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.components[name]
	switch {
	case !ok:
		return Endpoint{}, status.Errorf(codes.Unavailable, "%s is not running", name)
	case c.started:
		return c.endpoint, nil
	case c.status.State == Unconfigured:
		return Endpoint{}, status.Error(codes.FailedPrecondition, "not configured")
	case c.status.State == Starting:
		return Endpoint{}, status.Errorf(codes.Unavailable, "%s is starting", name)
	case c.status.Message != "":
		return Endpoint{}, status.Errorf(codes.Unavailable, "%s is %s: %s", name, c.status.State, c.status.Message)
	default:
		return Endpoint{}, status.Errorf(codes.Unavailable, "%s is %s", name, c.status.State)
	}
}

// poke wakes the reconcile loop.
func (r *Runtime) poke() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runtime) reconcileLoop(ctx context.Context) {
	backoff := retryMin
	for {
		var retry <-chan time.Time
		timer := time.NewTimer(backoff)
		if r.reconcile(ctx) {
			retry = timer.C
			backoff = min(backoff*2, retryMax)
		} else {
			backoff = retryMin
		}
		select {
		case <-ctx.Done():
		case <-r.wake:
		case <-retry:
		}
		timer.Stop()
		if ctx.Err() != nil {
			return
		}
	}
}

// reconcile launches every wanted Component that isn't running, in
// dependency order, without waiting on health. It reports whether a launch
// failed and should be retried.
func (r *Runtime) reconcile(ctx context.Context) (retry bool) {
	r.mu.Lock()
	var roots []string
	for _, p := range r.cat.Plugins {
		if r.installed[p.Name] {
			roots = append(roots, p.Requires...)
		}
	}
	r.mu.Unlock()
	order, err := r.cat.startOrder(roots)
	if err != nil {
		r.log.Error("resolve requires", "err", err)
		return false
	}
	for _, name := range order {
		r.mu.Lock()
		c, ok := r.components[name]
		if !ok {
			c = &component{status: Status{State: Starting}}
			r.components[name] = c
		}
		running := c.proc != nil
		r.mu.Unlock()
		if running {
			continue
		}
		if ctx.Err() != nil {
			return false
		}
		if err := r.launch(ctx, name); err != nil {
			r.log.Error("launch", "component", name, "err", err)
			r.setStatus(name, Status{State: Down, Message: err.Error()})
			retry = true
		}
	}
	return retry
}

// launch starts name's process, describes it and starts it with its config.
// It fails only when the process couldn't be launched and described; a
// failed Start leaves the process running, unconfigured or down.
func (r *Runtime) launch(ctx context.Context, name string) error {
	r.setStatus(name, Status{State: Starting})
	dbPath := filepath.Join(r.opts.DataDir, "db", name+".db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		return err
	}
	spec := LaunchSpec{Name: name, DBPath: dbPath}
	if r.opts.HostServer != nil {
		spec.HostServer = func(opts ...grpc.ServerOption) *grpc.Server { return r.opts.HostServer(name, opts...) }
	}
	proc, err := r.opts.Launcher.Launch(ctx, spec)
	if err != nil {
		return err
	}
	lc := componentpb.NewLifecycleServiceClient(proc.Conn())
	endpoint, cfgDesc, err := describe(ctx, lc, name)
	if err != nil {
		proc.Stop(ctx)
		return err
	}
	endpoint.Conn = proc.Conn()
	r.mu.Lock()
	c := r.components[name]
	c.proc = proc
	c.endpoint = endpoint
	r.mu.Unlock()

	if err := r.start(ctx, lc, name, cfgDesc); err != nil {
		st := Status{State: Down, Message: status.Convert(err).Message()}
		if sdk.FieldErrorsOf(err) != nil || status.Code(err) == codes.InvalidArgument {
			st.State = Unconfigured
		}
		r.log.Warn("start", "component", name, "state", st.State, "err", err)
		r.setStatus(name, st)
		return nil
	}
	r.mu.Lock()
	c.started = true
	r.mu.Unlock()
	r.loops.Go(func() { r.pollHealth(ctx, name) })
	return nil
}

// describe asks the Component what it is and builds its Endpoint (without
// Conn) and the descriptor of its config message (nil when it takes none).
func describe(ctx context.Context, lc componentpb.LifecycleServiceClient, name string) (Endpoint, protoreflect.MessageDescriptor, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	d, err := lc.Describe(ctx, &componentpb.DescribeRequest{})
	if err != nil {
		return Endpoint{}, nil, fmt.Errorf("describe: %w", err)
	}
	if d.GetName() != name {
		return Endpoint{}, nil, fmt.Errorf("describe: launched %s, but it says it is %s", name, d.GetName())
	}
	files, err := protodesc.NewFiles(d.GetDescriptors())
	if err != nil {
		return Endpoint{}, nil, fmt.Errorf("describe: descriptors: %w", err)
	}
	svc, err := findDescriptor[protoreflect.ServiceDescriptor](files, d.GetService())
	if err != nil {
		return Endpoint{}, nil, err
	}
	var cfg protoreflect.MessageDescriptor
	if d.GetConfigMessage() != "" {
		if cfg, err = findDescriptor[protoreflect.MessageDescriptor](files, d.GetConfigMessage()); err != nil {
			return Endpoint{}, nil, err
		}
	}
	return Endpoint{Service: svc, Types: dynamicpb.NewTypes(files)}, cfg, nil
}

func findDescriptor[D protoreflect.Descriptor](files *protoregistry.Files, name string) (D, error) {
	var zero D
	desc, err := files.FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		return zero, fmt.Errorf("describe: %s: %w", name, err)
	}
	d, ok := desc.(D)
	if !ok {
		return zero, fmt.Errorf("describe: %s is a %T", name, desc)
	}
	return d, nil
}

// start loads name's config and starts the Component with it.
func (r *Runtime) start(ctx context.Context, lc componentpb.LifecycleServiceClient, name string, cfgDesc protoreflect.MessageDescriptor) error {
	var cfg []byte
	if cfgDesc != nil {
		msg, err := r.opts.Config.Load(ctx, name, cfgDesc)
		if err != nil {
			return err
		}
		if cfg, err = proto.Marshal(msg); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	_, err := lc.Start(ctx, &componentpb.StartRequest{Config: cfg, PublicUrl: r.opts.PublicURL})
	return err
}

// pollHealth asks one Component for its health and records the answer.
func (r *Runtime) pollHealth(ctx context.Context, name string) {
	r.mu.Lock()
	c := r.components[name]
	if c == nil || c.proc == nil {
		r.mu.Unlock()
		return
	}
	lc := componentpb.NewLifecycleServiceClient(c.proc.Conn())
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	resp, err := lc.Health(ctx, &componentpb.HealthRequest{})
	if ctx.Err() != nil && err != nil {
		return // stopping
	}
	var st Status
	switch {
	case status.Code(err) == codes.FailedPrecondition:
		st = Status{State: Unconfigured, Message: status.Convert(err).Message()}
	case err != nil:
		st = Status{State: Down, Message: status.Convert(err).Message()}
	default:
		h := sdk.HealthFromProto(resp)
		st = Status{State: State(h.State.String()), Message: h.Message}
	}
	r.setStatus(name, st)
}

func (r *Runtime) setStatus(name string, st Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.components[name]; ok {
		c.status = st
	}
}
