// Package core is the ddash host: it opens the core DB, runs the installed
// Plugins' Components and serves the one http.Handler.
package core

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/config"
	"github.com/itsKontra/ddash/core/coredb"
	"github.com/itsKontra/ddash/core/router"
	"github.com/itsKontra/ddash/core/runtime"
)

// Options configure a Host.
type Options struct {
	// DataDir is /data: the core DB is <DataDir>/core.db, Component DBs are
	// under <DataDir>/db/.
	DataDir string
	// Release provides the Release's catalogue.
	Release runtime.ReleaseSource
	// Launcher starts Component processes.
	Launcher runtime.Launcher
	// Auth guards everything behind sign-in. Nil refuses every request.
	Auth auth.Middleware
	// PublicURL is where the Operator reaches ddash.
	PublicURL string
	// Logger receives the host's logs. Nil discards them.
	Logger *slog.Logger
}

// Host is the ddash core.
type Host struct {
	db      *sql.DB
	config  *config.Store
	runtime *runtime.Runtime
	handler http.Handler
}

// New opens and migrates the core DB and reads the installed set. The
// Handler serves at once; Components start only after Start.
func New(ctx context.Context, opts Options) (*Host, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Auth == nil {
		opts.Auth = auth.Refuse
	}
	if err := os.MkdirAll(opts.DataDir, 0o750); err != nil {
		return nil, err
	}
	db, err := coredb.Open(ctx, filepath.Join(opts.DataDir, "core.db"))
	if err != nil {
		return nil, err
	}
	cfg := config.New(db)
	rt, err := runtime.New(ctx, runtime.Options{
		DB:        db,
		Config:    cfg,
		Release:   opts.Release,
		Launcher:  opts.Launcher,
		DataDir:   opts.DataDir,
		PublicURL: opts.PublicURL,
		Logger:    opts.Logger,
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	h := &Host{db: db, config: cfg, runtime: rt}
	h.handler = h.routes(opts.Auth)
	return h, nil
}

func (h *Host) routes(guard auth.Middleware) http.Handler {
	api := http.NewServeMux()
	api.Handle(router.Prefix, router.New(h.runtime))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.Handle("/api/", requireConnectHeader(guard(api)))
	return mux
}

// healthz reports that HTTP serves and the core DB answers. It says
// nothing about Components.
func (h *Host) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := h.db.PingContext(ctx); err != nil {
		http.Error(w, "core DB: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok\n"))
}

// requireConnectHeader refuses an /api call without Connect-Protocol-Version: 1.
// A browser sends that header cross-site only after a CORS preflight, which
// ddash never grants, so it stops cross-site requests.
func requireConnectHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Connect-Protocol-Version") != "1" {
			router.WriteError(w, status.Error(codes.InvalidArgument, `missing required header: set Connect-Protocol-Version to "1"`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Handler is the host's one http.Handler.
func (h *Host) Handler() http.Handler { return h.handler }

// Runtime runs the installed Plugins' Components.
func (h *Host) Runtime() *runtime.Runtime { return h.runtime }

// Config holds every owner's config.
func (h *Host) Config() *config.Store { return h.config }

// Start reconciles the installed Plugins' Components in the background.
func (h *Host) Start() { h.runtime.Start() }

// Shutdown stops every Component in parallel, waiting at most
// runtime.StopTimeout, then closes the core DB. Stop serving HTTP first.
func (h *Host) Shutdown(ctx context.Context) error {
	h.runtime.Stop(ctx)
	return h.db.Close()
}
