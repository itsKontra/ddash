// The ddash core binary. `ddash` serves the UI and runs the installed
// Plugins' Components; `ddash healthcheck` asks a running one for /healthz.
//
// Environment:
//
//	DDASH_LISTEN       address to serve HTTP on (default :8080)
//	DDASH_DATA_DIR     the data dir (default /data)
//	DDASH_RELEASE_DIR  a local Release dir (catalogue.json, SHA256SUMS, the
//	                   Component binaries and Plugin bundles) to run instead of
//	                   the GitHub release of this core's Release; required for
//	                   the dev Release
//	DDASH_GITHUB_TOKEN authenticates the GitHub downloads (a private repo)
//	DDASH_PUBLIC_URL   where the Operator reaches ddash
//	DDASH_DEV_AUTH     1 signs every request in as operator:dev (dev builds only)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/itsKontra/ddash/core"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/web"
)

const defaultListen = ":8080"

// httpShutdownTimeout bounds how long in-flight requests may finish after
// SIGTERM. With runtime.StopTimeout it fits compose's 20 s grace period.
const httpShutdownTimeout = 5 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	var err error
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		err = healthcheck(ctx, "http://"+localAddr(env(os.Getenv, "DDASH_LISTEN", defaultListen)))
	} else {
		err = serve(ctx, os.Getenv, func(addr string) { slog.Info("serving HTTP", "addr", addr) })
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ddash:", err)
		os.Exit(1)
	}
}

// serve runs the host until ctx ends: it opens the core DB, serves HTTP,
// and only then starts reconciling Components. When ctx ends it stops
// accepting HTTP, stops every Component and closes the core DB.
func serve(ctx context.Context, getenv func(string) string, listening func(addr string)) error {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	dataDir := env(getenv, "DDASH_DATA_DIR", "/data")
	src, err := releaseSource(getenv, sdk.Release, dataDir, nil)
	if err != nil {
		return err
	}
	host, err := core.New(ctx, core.Options{
		DataDir:   dataDir,
		Release:   src,
		Launcher:  runtime.Subprocess(os.Stderr),
		Auth:      auth.FromEnv(getenv),
		PublicURL: getenv("DDASH_PUBLIC_URL"),
		Logger:    logger,
		Shell:     web.Shell(),
	})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", env(getenv, "DDASH_LISTEN", defaultListen))
	if err != nil {
		_ = host.Shutdown(context.Background()) // the listen error matters more
		return err
	}
	srv := &http.Server{Handler: host.Handler(), ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	listening(ln.Addr().String())
	host.Start()

	select {
	case <-ctx.Done():
	case err = <-served:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancel()
	if srv.Shutdown(shutdownCtx) != nil {
		srv.Close()
	}
	if shutdownErr := host.Shutdown(context.Background()); err == nil {
		err = shutdownErr
	}
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// repo publishes every Release as a GitHub release.
const repo = "itsKontra/ddash"

// releaseSource is where Release rel comes from: DDASH_RELEASE_DIR, or the
// GitHub release tagged rel, downloaded into dataDir with client (nil: the
// default one).
func releaseSource(getenv func(string) string, rel, dataDir string, client *http.Client) (runtime.ReleaseSource, error) {
	if dir := getenv("DDASH_RELEASE_DIR"); dir != "" {
		return runtime.Dir(dir), nil
	}
	if rel == "dev" {
		return nil, errors.New("the dev Release isn't on GitHub: set DDASH_RELEASE_DIR")
	}
	return runtime.GitHub(runtime.GitHubOptions{
		Repo:    repo,
		Release: rel,
		Token:   getenv("DDASH_GITHUB_TOKEN"),
		DataDir: dataDir,
		Client:  client,
	}), nil
}

// healthcheck fails unless the host at baseURL answers /healthz with 200.
func healthcheck(ctx context.Context, baseURL string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("/healthz: %s", res.Status)
	}
	return nil
}

// localAddr turns a listen address into one to dial on this machine.
func localAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || host == "" || host == "0.0.0.0" || host == "::" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return listen
}

func env(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}
