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
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/config"
	"github.com/itsKontra/ddash/core/coredb"
	"github.com/itsKontra/ddash/core/router"
	"github.com/itsKontra/ddash/core/runtime"
	corepb "github.com/itsKontra/ddash/proto/ddash/core"
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
	gate    *appstate.Gate
	config  *config.Store
	runtime *runtime.Runtime
	router  *router.Router
	handler http.Handler

	stopLoops context.CancelFunc
	loops     sync.WaitGroup
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
	gate, err := appstate.Open(ctx, db)
	if err != nil {
		db.Close()
		return nil, err
	}
	cfg := config.New(db)
	var rtr *router.Router // routes Component calls once the runtime exists
	rt, err := runtime.New(ctx, runtime.Options{
		HostServer: func(caller string, opts ...grpc.ServerOption) *grpc.Server {
			return rtr.HostServer(caller, opts...)
		},
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
	rtr = router.New(rt, gate)
	h := &Host{db: db, gate: gate, config: cfg, runtime: rt, router: rtr}
	h.handler = h.routes(opts.Auth)
	return h, nil
}

func (h *Host) routes(guard auth.Middleware) http.Handler {
	api := http.NewServeMux()
	api.Handle(router.Prefix, h.router)

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

// Install is the core Action that installs a Plugin: it adds it to the
// installed set and starts the Components it requires in the background.
// It runs for the actor in ctx.
func (h *Host) Install(ctx context.Context, req *corepb.InstallRequest) (*corepb.InstallResponse, error) {
	err := h.coreAction(ctx, "Install", req, func() error {
		return h.runtime.Install(ctx, req.GetPlugin())
	})
	if err != nil {
		return nil, err
	}
	return &corepb.InstallResponse{}, nil
}

// Uninstall is the core Action that uninstalls a Plugin: it stops and
// removes every Component no installed Plugin still requires, keeping their
// DB files (as orphans) and config unless delete_data is set. It runs for
// the actor in ctx.
func (h *Host) Uninstall(ctx context.Context, req *corepb.UninstallRequest) (*corepb.UninstallResponse, error) {
	var orphans []string
	err := h.coreAction(ctx, "Uninstall", req, func() (err error) {
		orphans, err = h.runtime.Uninstall(ctx, req.GetPlugin(), req.GetDeleteData())
		return err
	})
	if err != nil {
		return nil, err
	}
	return &corepb.UninstallResponse{Orphans: orphans}, nil
}

// Restart is the core Action that stops a Component and starts it again,
// forgetting its crashes, so a Component held down after repeated crashes
// runs again. It runs for the actor in ctx.
func (h *Host) Restart(ctx context.Context, req *corepb.RestartRequest) (*corepb.RestartResponse, error) {
	err := h.coreAction(ctx, "Restart", req, func() error {
		return h.runtime.Restart(ctx, req.GetComponent())
	})
	if err != nil {
		return nil, err
	}
	return &corepb.RestartResponse{}, nil
}

// coreService is the core API, whose Actions pass the gate like any other.
var coreService = corepb.File_ddash_core_core_proto.Services().ByName("CoreService")

// coreAction runs the core Action method through the gate, on a new chain,
// for the actor in ctx, or for the core when there is none.
func (h *Host) coreAction(ctx context.Context, method string, req proto.Message, run func() error) error {
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		actor = auth.Actor{Kind: auth.Core}
	}
	done, err := h.gate.Admit(ctx, actor, appstate.Action{
		Chain:   router.NewChain(),
		Target:  "core",
		Method:  coreService.Methods().ByName(protoreflect.Name(method)),
		Request: req,
	})
	if err != nil {
		return err
	}
	err = run()
	done(err)
	return err
}

// Handler is the host's one http.Handler.
func (h *Host) Handler() http.Handler { return h.handler }

// Runtime runs the installed Plugins' Components.
func (h *Host) Runtime() *runtime.Runtime { return h.runtime }

// AuditLog is the gate every Action passes and the Audit Log it keeps.
func (h *Host) AuditLog() *appstate.Gate { return h.gate }

// Config holds every owner's config.
func (h *Host) Config() *config.Store { return h.config }

// Start runs the housekeeping in the background: it reconciles the
// installed Plugins' Components and sweeps the Audit Log daily.
func (h *Host) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.stopLoops = cancel
	h.loops.Go(func() { h.gate.SweepDaily(ctx) })
	h.runtime.Start()
}

// Shutdown stops every Component in parallel, waiting at most
// runtime.StopTimeout, then closes the core DB. Stop serving HTTP first.
func (h *Host) Shutdown(ctx context.Context) error {
	if h.stopLoops != nil {
		h.stopLoops()
	}
	h.loops.Wait()
	h.runtime.Stop(ctx)
	return h.db.Close()
}
