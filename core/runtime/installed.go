package runtime

// The installed set: installing and uninstalling Plugins, and the data
// Components leave behind.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Install adds plugin to the installed set and wakes the reconcile loop to

// start the Components it requires. It returns the Components that weren't

// wanted before, in start order.
func (r *Runtime) Install(ctx context.Context, plugin string) (added []string, err error) {
	r.mu.Lock()
	catOK, rel := r.catOK, r.release
	p, ok := r.cat.plugin(plugin)
	r.mu.Unlock()
	switch {
	case !catOK:
		return nil, status.Errorf(codes.Unavailable, "the catalogue of Release %s can't be downloaded yet; try again later", rel)
	case !ok:
		return nil, status.Errorf(codes.NotFound, "plugin %s isn't in Release %s", plugin, rel)
	}
	manifest, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	_, err = r.opts.DB.ExecContext(ctx, `
		INSERT INTO installed_plugin (name, release, installed_at, manifest) VALUES (?, ?, ?, ?)
		ON CONFLICT (name) DO NOTHING`,
		plugin, rel, time.Now().UTC().Format(time.RFC3339Nano), string(manifest))
	if err != nil {
		return nil, fmt.Errorf("install %s: %w", plugin, err)
	}
	r.mu.Lock()
	before := r.wanted()
	r.installed[plugin] = p
	pulls := r.cat.Pulls(plugin)
	r.mu.Unlock()
	for _, name := range pulls {
		if !before[name] {
			added = append(added, name)
		}
	}
	r.poke()
	return added, nil
}

// Uninstall removes plugin from the installed set, then stops and removes

// every Component no installed Plugin still requires, transitively. Their

// DB files and config are kept unless deleteData is set. It returns the

// orphans left afterwards, see Orphans.
func (r *Runtime) Uninstall(ctx context.Context, plugin string, deleteData bool) (orphans []string, err error) {
	r.changing.Lock()
	defer r.changing.Unlock()
	r.mu.Lock()
	_, installed := r.installed[plugin]
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

// deleteData deletes a Component's DB file, its snapshot and config. DBs

// rollbacks moved aside stay: they are never deleted.
func (r *Runtime) deleteData(ctx context.Context, name string) error {
	db := r.dbPath(name)
	for _, f := range []string{db, db + "-wal", db + "-shm"} {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if err := r.deleteSnapshot(ctx, name); err != nil {
		return err
	}
	if _, err := r.opts.DB.ExecContext(ctx, `DELETE FROM component_release WHERE name = ?`, name); err != nil {
		return err
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
	order := r.order()
	wanted := make(map[string]bool, len(order))
	for _, name := range order {
		wanted[name] = true
	}
	return wanted
}

// order returns every Component the installed Plugins require,

// transitively, each after the ones it requires. Without the catalogue it

// is only what their manifests require directly. r.mu must be held.
func (r *Runtime) order() []string {
	if !r.catOK {
		var order []string
		for _, name := range slices.Sorted(maps.Keys(r.installed)) {
			for _, req := range r.installed[name].Requires {
				if !slices.Contains(order, req) {
					order = append(order, req)
				}
			}
		}
		return order
	}
	var roots []string
	for _, p := range r.cat.Plugins {
		if _, ok := r.installed[p.Name]; ok {
			roots = append(roots, p.Requires...)
		}
	}
	order, _ := r.cat.startOrder(roots) // the catalogue was checked on load
	return order
}
