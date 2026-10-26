// Package runtime runs the installed Plugins' Components: it keeps the
// installed set, reads the Release's catalogue, resolves requires, and runs
// one reconcile loop that launches, starts, health-polls and stops
// Components. The router reaches them through Endpoint.
package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/itsKontra/ddash/core/config"
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
	// UpdatePending: the Component's binary of the running Release couldn't
	// be downloaded. It stays stopped, retried with backoff.
	UpdatePending State = "update pending"
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

	wake   chan struct{}
	ctx    context.Context // the loops' ctx, set by Start
	cancel context.CancelFunc
	loops  sync.WaitGroup

	// changing is held by each reconcile pass and by Uninstall, so a
	// Component is never launched and removed at once.
	changing sync.Mutex

	mu sync.Mutex
	// release is the Release the runtime runs.
	release string
	// cat is release's catalogue, once the release source had it (catOK).
	cat   Catalogue
	catOK bool
	// installed holds each installed Plugin's manifest, as the core DB
	// keeps it, so it shows while the catalogue can't be had.
	installed  map[string]PluginEntry
	components map[string]*component
	// bundles holds, by installed Plugin, why its bundles of the running
	// Release couldn't be downloaded; "" once they were.
	bundles map[string]string
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

// New reads the installed set and the Release's catalogue. A catalogue the
// release source can't download yet (ErrDownload) doesn't stop it: the
// installed Plugins show from the core DB, and Start retries. Nothing runs
// until Start.
func New(ctx context.Context, opts Options) (*Runtime, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	r := &Runtime{
		opts:       opts,
		log:        opts.Logger,
		release:    opts.Release.Release(),
		wake:       make(chan struct{}, 1),
		installed:  map[string]PluginEntry{},
		components: map[string]*component{},
		bundles:    map[string]string{},
	}
	if err := r.readInstalled(ctx); err != nil {
		return nil, fmt.Errorf("read installed Plugins: %w", err)
	}
	cat, err := opts.Release.Catalogue(ctx)
	switch {
	case err == nil:
		if err := r.useCatalogue(ctx, cat); err != nil {
			return nil, err
		}
		r.dropRetired(ctx)
	case errors.Is(err, ErrDownload):
		r.log.Warn("the Release's catalogue can't be downloaded yet", "release", r.release, "err", err)
		r.cat = Catalogue{Release: r.release}
	default:
		return nil, fmt.Errorf("read the Release's catalogue: %w", err)
	}
	return r, nil
}

func (r *Runtime) readInstalled(ctx context.Context) error {
	rows, err := r.opts.DB.QueryContext(ctx, `SELECT name, manifest FROM installed_plugin`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name, manifest string
		if err := rows.Scan(&name, &manifest); err != nil {
			return err
		}
		p := PluginEntry{Name: name, Title: name}
		if manifest != "" {
			if err := json.Unmarshal([]byte(manifest), &p); err != nil {
				return fmt.Errorf("%s's manifest: %w", name, err)
			}
		}
		r.installed[name] = p
	}
	return rows.Err()
}

// useCatalogue makes cat the running catalogue and keeps the installed
// Plugins' manifests from it.
func (r *Runtime) useCatalogue(ctx context.Context, cat Catalogue) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name := range r.installed {
		p, ok := cat.plugin(name)
		if !ok {
			continue // the Release dropped it; it shows from the core DB
		}
		b, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if _, err := r.opts.DB.ExecContext(ctx, `UPDATE installed_plugin SET manifest = ? WHERE name = ?`, string(b), name); err != nil {
			return fmt.Errorf("keep %s's manifest: %w", name, err)
		}
		r.installed[name] = p
	}
	r.cat, r.catOK, r.release = cat, true, cat.Release
	return nil
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

func (r *Runtime) dbPath(name string) string {
	return filepath.Join(r.opts.DataDir, "db", name+".db")
}

// PluginRequires returns the Components an installed Plugin lists directly
// in requires. ok is false when the Plugin isn't installed.
func (r *Runtime) PluginRequires(plugin string) (requires []string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, installed := r.installed[plugin]
	return p.Requires, installed
}

// Installed returns the installed Plugins' manifests, by name: from the
// running catalogue, or the core DB while it can't be had.
func (r *Runtime) Installed() []PluginEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := slices.Collect(maps.Values(r.installed))
	slices.SortFunc(out, func(a, b PluginEntry) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// PendingBundles returns, by installed Plugin, why its bundles of the
// running Release are still missing. Plugins that have them aren't listed.
func (r *Runtime) PendingBundles() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]string{}
	for name, msg := range r.bundles {
		if _, installed := r.installed[name]; installed && msg != "" {
			out[name] = msg
		}
	}
	return out
}

// Catalogue is the catalogue of the Release the runtime runs. It lists
// nothing while the release source can't provide it.
func (r *Runtime) Catalogue() Catalogue {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cat
}

// Release is the Release the runtime runs.
func (r *Runtime) Release() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.release
}

// ComponentRequires returns the Components a Component lists in requires.
func (r *Runtime) ComponentRequires(component string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
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
