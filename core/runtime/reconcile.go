package runtime

// The reconcile loop: it launches what the installed Plugins want.

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	componentpb "github.com/itsKontra/ddash/proto/ddash/component"
	"github.com/itsKontra/ddash/sdk"
)

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
	if !r.haveCatalogue(ctx) {
		return true
	}
	r.mu.Lock()
	var plugins []string
	for _, name := range slices.Sorted(maps.Keys(r.installed)) {
		if msg, ok := r.bundles[name]; !ok || msg != "" {
			plugins = append(plugins, name)
		}
	}
	order := r.order()
	r.mu.Unlock()
	for _, p := range plugins {
		if r.fetchBundles(ctx, p) != nil {
			retry = true
		}
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
			st := launchFailed(err)
			r.log.Error("launch", "component", name, "state", st.State, "err", err)
			r.setStatus(name, st)
			retry = true
		}
	}
	return retry
}

// haveCatalogue reports whether the runtime has its Release's catalogue,

// asking the release source again if not. Until it has it, every

// Component the installed Plugins require directly is update pending.
func (r *Runtime) haveCatalogue(ctx context.Context) bool {
	r.mu.Lock()
	catOK := r.catOK
	r.mu.Unlock()
	if catOK {
		return true
	}
	cat, err := r.opts.Release.Catalogue(ctx)
	if err == nil {
		if err = r.useCatalogue(ctx, cat); err == nil {
			return true
		}
	}
	r.log.Error("read the Release's catalogue", "err", err)
	r.mu.Lock()
	defer r.mu.Unlock()
	msg := err.Error() + "; retrying"
	for _, name := range r.order() {
		c, ok := r.components[name]
		if !ok {
			c = &component{}
			r.components[name] = c
		}
		c.status = Status{State: UpdatePending, Message: msg}
	}
	for name := range r.installed {
		r.bundles[name] = msg
	}
	return false
}

// fetchBundles has the release source download plugin's bundles, and

// records the Plugin as on the running Release once it has them.
func (r *Runtime) fetchBundles(ctx context.Context, plugin string) error {
	err := r.opts.Release.Plugin(ctx, plugin)
	if err == nil {
		_, err = r.opts.DB.ExecContext(ctx, `UPDATE installed_plugin SET release = ? WHERE name = ?`, r.Release(), plugin)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.log.Error("download bundles", "plugin", plugin, "err", err)
		r.bundles[plugin] = err.Error() + "; retrying"
		return err
	}
	r.bundles[plugin] = ""
	return nil
}

// launch starts name's process, describes it and starts it with cfg, or

// with its stored config when cfg is nil. It fails only when the process

// couldn't be launched and described; a failed Start leaves the process

// running, unconfigured or down, and is returned as startErr.
func (r *Runtime) launch(ctx context.Context, name string, cfg proto.Message) (startErr, err error) {
	r.setStatus(name, Status{State: Starting})
	rel := r.Release()
	bin, err := r.opts.Release.Component(ctx, name)
	if err != nil {
		return nil, pending{err}
	}
	dbPath := r.dbPath(name)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		return nil, err
	}
	if err := r.switchRelease(ctx, name, rel); err != nil {
		return nil, err
	}
	spec := LaunchSpec{Name: name, DBPath: dbPath, Release: rel, Binary: bin}
	if r.opts.HostServer != nil {
		spec.HostServer = func(opts ...grpc.ServerOption) *grpc.Server { return r.opts.HostServer(name, opts...) }
	}
	proc, err := r.opts.Launcher.Launch(ctx, spec)
	if err != nil {
		return nil, err
	}
	lc := componentpb.NewLifecycleServiceClient(proc.Conn())
	endpoint, cfgDesc, err := describe(ctx, lc, name, rel)
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

// pending is a launch that failed because the Component's binary couldn't

// be had from the release source.
type pending struct{ err error }

func (p pending) Error() string { return p.err.Error() }

// launchFailed is the status of a Component whose launch failed with err:

// update pending when its binary couldn't be had, else down.
func launchFailed(err error) Status {
	if p := (pending{}); errors.As(err, &p) {
		return Status{State: UpdatePending, Message: p.err.Error() + "; retrying"}
	}
	return Status{State: Down, Message: err.Error()}
}
