// Package runtime runs the installed Plugins' Components: it keeps the
// installed set, reads the Release's catalogue, resolves requires, and runs
// one reconcile loop that launches, starts, health-polls and stops
// Components. The router reaches them through Endpoint.
package runtime

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	// Crashed: the Component's process ended on its own. It is restarted
	// with exponential backoff, unless it crashed crashLimit times within
	// crashWindow: then it stays down until Restart.
	Crashed State = "crashed"
)

// Status is a Component's State with a one-line message.
type Status struct {
	State   State
	Message string
	// Held: the Component crashed too often and stays down until Restart.
	Held bool
	// Configurable: the Component takes config, so it has a form.
	Configurable bool
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

	crashLimit  = 5
	crashWindow = 10 * time.Minute
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
	ctx    context.Context // the loops' ctx, set by Start
	cancel context.CancelFunc
	loops  sync.WaitGroup

	// changing is held by each reconcile pass and by Uninstall, so a
	// Component is never launched and removed at once.
	changing sync.Mutex

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

	// crashes are the times of the crashes within the last crashWindow.
	crashes []time.Time
	// held keeps a Component that crashed too often down until Restart.
	held bool
	// notBefore delays the relaunch after a crash.
	notBefore time.Time

	// described: the Component answered a Describe once.
	described bool
	// config is the descriptor of the Component's config message, from its
	// last Describe; nil when it takes none.
	config protoreflect.MessageDescriptor
	// configErrors are the per-field errors its last Start failed with.
	configErrors sdk.FieldErrors
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
	r.mu.Lock()
	r.ctx, r.cancel = ctx, cancel
	r.mu.Unlock()
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

// Uninstall removes plugin from the installed set, then stops and removes
// every Component no installed Plugin still requires, transitively. Their
// DB files and config are kept unless deleteData is set. It returns the
// orphans left afterwards, see Orphans.
func (r *Runtime) Uninstall(ctx context.Context, plugin string, deleteData bool) (orphans []string, err error) {
	r.changing.Lock()
	defer r.changing.Unlock()
	r.mu.Lock()
	installed := r.installed[plugin]
	r.mu.Unlock()
	if !installed {
		return nil, status.Errorf(codes.NotFound, "plugin %s isn't installed", plugin)
	}
	if _, err := r.opts.DB.ExecContext(ctx, `DELETE FROM installed_plugin WHERE name = ?`, plugin); err != nil {
		return nil, fmt.Errorf("uninstall %s: %w", plugin, err)
	}

	r.mu.Lock()
	delete(r.installed, plugin)
	wanted := r.wanted()
	var removed []string
	var procs []Process
	for name, c := range r.components {
		if !wanted[name] {
			removed = append(removed, name)
			if c.proc != nil {
				procs = append(procs, c.proc)
			}
			delete(r.components, name)
		}
	}
	r.mu.Unlock()

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), StopTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, p := range procs {
		wg.Go(func() { p.Stop(stopCtx) })
	}
	wg.Wait()

	if deleteData {
		for _, name := range removed {
			if err := r.deleteData(ctx, name); err != nil {
				return nil, fmt.Errorf("uninstall %s: %w", plugin, err)
			}
		}
	}
	return r.Orphans(), nil
}

// deleteData deletes a Component's DB file and config.
func (r *Runtime) deleteData(ctx context.Context, name string) error {
	db := r.dbPath(name)
	for _, f := range []string{db, db + "-wal", db + "-shm"} {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return r.opts.Config.Delete(ctx, name)
}

// Orphans returns, sorted, the Components that have a DB file although no
// installed Plugin requires them.
func (r *Runtime) Orphans() []string {
	entries, err := os.ReadDir(filepath.Join(r.opts.DataDir, "db"))
	if err != nil {
		return nil
	}
	r.mu.Lock()
	wanted := r.wanted()
	r.mu.Unlock()
	var orphans []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".db")
		if ok && !e.IsDir() && !wanted[name] {
			orphans = append(orphans, name)
		}
	}
	slices.Sort(orphans)
	return orphans
}

// DeleteOrphan deletes an orphan's DB file and config. It refuses a
// Component an installed Plugin requires, and one without a DB file.
func (r *Runtime) DeleteOrphan(ctx context.Context, name string) error {
	r.changing.Lock()
	defer r.changing.Unlock()
	if !slices.Contains(r.Orphans(), name) {
		r.mu.Lock()
		wanted := r.wanted()[name]
		r.mu.Unlock()
		if wanted {
			return status.Errorf(codes.FailedPrecondition, "%s isn't an orphan: an installed Plugin requires it", name)
		}
		return status.Errorf(codes.NotFound, "%s has no DB file", name)
	}
	if err := r.deleteData(ctx, name); err != nil {
		return fmt.Errorf("delete orphan %s: %w", name, err)
	}
	return nil
}

// wanted returns every Component the installed Plugins require,
// transitively. r.mu must be held.
func (r *Runtime) wanted() map[string]bool {
	var roots []string
	for _, p := range r.cat.Plugins {
		if r.installed[p.Name] {
			roots = append(roots, p.Requires...)
		}
	}
	order, _ := r.cat.startOrder(roots) // the catalogue was checked on load
	wanted := make(map[string]bool, len(order))
	for _, name := range order {
		wanted[name] = true
	}
	return wanted
}

func (r *Runtime) dbPath(name string) string {
	return filepath.Join(r.opts.DataDir, "db", name+".db")
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

// Installed returns the installed Plugins' manifests, by name.
func (r *Runtime) Installed() []PluginEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []PluginEntry
	for _, p := range r.cat.Plugins {
		if r.installed[p.Name] {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b PluginEntry) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Catalogue is the catalogue of the Release the runtime runs.
func (r *Runtime) Catalogue() Catalogue { return r.cat }

// Release is the Release the runtime runs.
func (r *Runtime) Release() string { return r.cat.Release }

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
		st := c.status
		st.Held = c.held
		st.Configurable = c.config != nil
		out[name] = st
	}
	return out
}

// Config returns the descriptor of a wanted Component's config message (nil
// when it takes none) and the per-field errors its last Start failed with.
// It is Unavailable until the Component was first described.
func (r *Runtime) Config(name string) (protoreflect.MessageDescriptor, sdk.FieldErrors, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.components[name]
	switch {
	case !ok || !r.wanted()[name]:
		return nil, nil, status.Errorf(codes.NotFound, "no installed Plugin requires %s", name)
	case !c.described:
		return nil, nil, status.Errorf(codes.Unavailable, "%s is %s", name, c.status.State)
	}
	return c.config, c.configErrors, nil
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
	r.changing.Lock()
	defer r.changing.Unlock()
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
		skip := c.proc != nil || c.held || time.Now().Before(c.notBefore)
		r.mu.Unlock()
		if skip {
			continue
		}
		if ctx.Err() != nil {
			return false
		}
		if _, err := r.launch(ctx, name, nil); err != nil {
			r.log.Error("launch", "component", name, "err", err)
			r.setStatus(name, Status{State: Down, Message: err.Error()})
			retry = true
		}
	}
	return retry
}

// launch starts name's process, describes it and starts it with cfg, or
// with its stored config when cfg is nil. It fails only when the process
// couldn't be launched and described; a failed Start leaves the process
// running, unconfigured or down, and is returned as startErr.
func (r *Runtime) launch(ctx context.Context, name string, cfg proto.Message) (startErr, err error) {
	r.setStatus(name, Status{State: Starting})
	dbPath := r.dbPath(name)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		return nil, err
	}
	spec := LaunchSpec{Name: name, DBPath: dbPath}
	if r.opts.HostServer != nil {
		spec.HostServer = func(opts ...grpc.ServerOption) *grpc.Server { return r.opts.HostServer(name, opts...) }
	}
	proc, err := r.opts.Launcher.Launch(ctx, spec)
	if err != nil {
		return nil, err
	}
	lc := componentpb.NewLifecycleServiceClient(proc.Conn())
	endpoint, cfgDesc, err := describe(ctx, lc, name, r.cat.Release)
	if err != nil {
		proc.Stop(ctx)
		return nil, err
	}
	endpoint.Conn = proc.Conn()
	r.mu.Lock()
	c := r.components[name]
	c.proc = proc
	c.endpoint = endpoint
	c.config, c.described = cfgDesc, true
	r.mu.Unlock()
	r.loops.Go(func() { r.watch(ctx, name, proc) })

	if err := r.start(ctx, lc, name, cfgDesc, cfg); err != nil {
		st := Status{State: Down, Message: status.Convert(err).Message()}
		if sdk.FieldErrorsOf(err) != nil || status.Code(err) == codes.InvalidArgument {
			st.State = Unconfigured
		}
		r.log.Warn("start", "component", name, "state", st.State, "err", err)
		r.mu.Lock()
		c.status, c.configErrors = st, sdk.FieldErrorsOf(err)
		r.mu.Unlock()
		return err, nil
	}
	r.mu.Lock()
	c.started, c.configErrors = true, nil
	r.mu.Unlock()
	r.loops.Go(func() { r.pollHealth(ctx, name) })
	return nil, nil
}

// watch waits for proc to end and handles that as a crash, unless the
// runtime stopped it.
func (r *Runtime) watch(ctx context.Context, name string, proc Process) {
	select {
	case <-ctx.Done():
		return
	case <-proc.Done():
	}
	r.mu.Lock()
	c := r.components[name]
	if c == nil || c.proc != proc || ctx.Err() != nil {
		r.mu.Unlock()
		return // stopped on purpose
	}
	c.proc, c.started = nil, false
	now := time.Now()
	c.crashes = append(slices.DeleteFunc(c.crashes, func(t time.Time) bool {
		return now.Sub(t) >= crashWindow
	}), now)
	var delay time.Duration
	n := len(c.crashes)
	if n >= crashLimit {
		c.held = true
		c.status = Status{State: Crashed, Message: fmt.Sprintf("crashed %d times in %s; restart it from the Catalogue", n, crashWindow)}
	} else {
		delay = retryMin << (n - 1)
		c.notBefore = now.Add(delay)
		c.status = Status{State: Crashed, Message: fmt.Sprintf("exited unexpectedly; restarting in %s", delay)}
	}
	r.mu.Unlock()
	r.log.Error("crashed", "component", name, "crashes", n, "restart_in", delay)

	stopCtx, cancel := context.WithTimeout(ctx, StopTimeout)
	proc.Stop(stopCtx) // releases what is left of it
	cancel()
	if delay == 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(delay):
		r.poke()
	}
}

// Restart stops a wanted Component and starts it again, forgetting its
// crashes. The Components that require it keep running.
func (r *Runtime) Restart(ctx context.Context, name string) error {
	r.changing.Lock()
	defer r.changing.Unlock()
	r.mu.Lock()
	c, ok := r.components[name]
	if !ok || !r.wanted()[name] {
		r.mu.Unlock()
		return status.Errorf(codes.NotFound, "no installed Plugin requires %s", name)
	}
	proc := c.proc
	c.proc, c.started = nil, false
	c.crashes, c.held, c.notBefore = nil, false, time.Time{}
	c.status = Status{State: Starting}
	r.mu.Unlock()
	if proc != nil {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), StopTimeout)
		proc.Stop(stopCtx)
		cancel()
	}
	r.poke()
	return nil
}

// Reconfigure restarts a wanted Component's process with cfg, a candidate
// config with its defaults applied, and returns once it has polled the
// health after Start. When Start rejects cfg, the error is returned and
// the Component restarts on its stored config. The Components that require
// it see Unavailable meanwhile.
func (r *Runtime) Reconfigure(ctx context.Context, name string, cfg proto.Message) error {
	r.changing.Lock()
	defer r.changing.Unlock()
	r.mu.Lock()
	c, ok := r.components[name]
	loops := r.ctx
	if !ok || !r.wanted()[name] || loops == nil {
		r.mu.Unlock()
		return status.Errorf(codes.NotFound, "no installed Plugin requires %s", name)
	}
	proc := c.proc
	c.proc, c.started = nil, false
	c.crashes, c.held, c.notBefore = nil, false, time.Time{}
	c.status = Status{State: Starting}
	r.mu.Unlock()
	r.stop(ctx, proc)

	startErr, err := r.launch(loops, name, cfg)
	if err == nil && startErr == nil {
		r.pollHealth(loops, name)
		return nil
	}
	if err != nil {
		r.setStatus(name, Status{State: Down, Message: err.Error()})
	}
	// Back to the stored config.
	r.mu.Lock()
	proc = c.proc
	c.proc, c.started = nil, false
	r.mu.Unlock()
	r.stop(ctx, proc)
	if _, err := r.launch(loops, name, nil); err != nil {
		r.log.Error("relaunch on the stored config", "component", name, "err", err)
		r.setStatus(name, Status{State: Down, Message: err.Error()})
		r.poke() // the reconcile loop retries
	}
	return cmp.Or(startErr, err)
}

// stop stops proc, if any, waiting at most StopTimeout.
func (r *Runtime) stop(ctx context.Context, proc Process) {
	if proc == nil {
		return
	}
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), StopTimeout)
	defer cancel()
	proc.Stop(stopCtx)
}

// describe asks the Component what it is and builds its Endpoint (without
// Conn) and the descriptor of its config message (nil when it takes none).
// It refuses a Component that says it is another one or of another Release.
func describe(ctx context.Context, lc componentpb.LifecycleServiceClient, name, release string) (Endpoint, protoreflect.MessageDescriptor, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	d, err := lc.Describe(ctx, &componentpb.DescribeRequest{})
	if err != nil {
		return Endpoint{}, nil, fmt.Errorf("describe: %w", err)
	}
	if d.GetName() != name {
		return Endpoint{}, nil, fmt.Errorf("describe: launched %s, but it says it is %s", name, d.GetName())
	}
	if d.GetRelease() != release {
		return Endpoint{}, nil, fmt.Errorf("describe: %s is of Release %s, but this is Release %s", name, d.GetRelease(), release)
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

// start starts the Component with cfg, or else with its stored config.
func (r *Runtime) start(ctx context.Context, lc componentpb.LifecycleServiceClient, name string, cfgDesc protoreflect.MessageDescriptor, cfg proto.Message) error {
	var b []byte
	if cfgDesc != nil {
		if cfg == nil {
			var err error
			if cfg, err = r.opts.Config.Load(ctx, name, cfgDesc); err != nil {
				return err
			}
		}
		var err error
		if b, err = proto.Marshal(cfg); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	_, err := lc.Start(ctx, &componentpb.StartRequest{Config: b, PublicUrl: r.opts.PublicURL})
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
	proc := c.proc
	lc := componentpb.NewLifecycleServiceClient(proc.Conn())
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.components[name]; c != nil && c.proc == proc {
		c.status = st // unless it crashed or restarted meanwhile
	}
}

func (r *Runtime) setStatus(name string, st Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.components[name]; ok {
		c.status = st
	}
}
