// The ddash core binary. `ddash` serves the UI and runs the installed
// Plugins' Components; `ddash healthcheck` asks a running one for /healthz
// (the image's and compose's healthcheck); `ddash version` prints the Release.
//
// Environment:
//
//	DDASH_LISTEN       address to serve HTTP on (default :8080; make dev sets it)
//	DDASH_DATA_DIR     the data dir (default /data; make dev sets it)
//	DDASH_RELEASE_DIR         a local Release dir (catalogue.json, SHA256SUMS,
//	                          the Component binaries and Plugin bundles) to run
//	                          instead of the GitHub release of this core's
//	                          Release; required for the dev Release
//	DDASH_GITHUB_TOKEN        authenticates the GitHub downloads (a private repo)
//	DDASH_PUBLIC_URL          where the Operator reaches ddash (https)
//	DDASH_OIDC_ISSUER         the Keycloak realm's public URL
//	DDASH_OIDC_CLIENT_ID      ddash's confidential Keycloak client
//	DDASH_OIDC_CLIENT_SECRET  its secret
//	DDASH_TRUSTED_PROXY       the reverse proxy's IP or CIDR, the only peer
//	                          whose X-Forwarded-For/-Proto are trusted
//	DDASH_DEV_AUTH            1 signs every request in as operator:dev
//	                          instead (dev builds only)
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
	"strings"
	"syscall"
	"time"

	"github.com/itsKontra/ddash/core"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/router"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/web"
)

const defaultListen = ":8080"

// httpShutdownTimeout bounds how long in-flight requests may finish after
// SIGTERM: long enough for an admitted Action to run to its end. With the
// Components' runtime.StopTimeout after it, it fits compose's 90 s grace
// period.
const httpShutdownTimeout = router.ActionDeadline + 5*time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	// The first signal starts the drain; a second one kills ddash.
	context.AfterFunc(ctx, stop)
	var err error
	switch {
	case len(os.Args) > 1 && os.Args[1] == "healthcheck":
		err = healthcheck(ctx, "http://"+localAddr(env(os.Getenv, "DDASH_LISTEN", defaultListen)))
	case len(os.Args) > 1 && os.Args[1] == "version":
		fmt.Println(sdk.Release)
	default:
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
	opts := core.Options{
		DataDir:      dataDir,
		Release:      src,
		Launcher:     runtime.Subprocess(os.Stderr),
		Auth:         auth.DevBypass(getenv),
		PublicURL:    getenv("DDASH_PUBLIC_URL"),
		TrustedProxy: getenv("DDASH_TRUSTED_PROXY"),
		Logger:       logger,
		Shell:        web.Shell(),
	}
	if opts.Auth == nil {
		oidc, err := signInConfig(getenv)
		if err != nil {
			return err
		}
		opts.OIDC = &oidc
	}
	host, err := core.New(ctx, opts)
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
	if closeErr := drain(srv, host); err == nil {
		err = closeErr
	}
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// stoppable is the host as shutdown sees it.
type stoppable interface {
	StopComponents(ctx context.Context)
	Close() error
}

// drain shuts down one step after another: stop accepting HTTP and wait
// up to httpShutdownTimeout for in-flight requests, and with them every
// admitted Action (ADR-0004); then stop every Component in parallel
// (runtime.StopTimeout, then kill); the core DB closes last.
func drain(srv *http.Server, host stoppable) error {
	ctx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancel()
	if srv.Shutdown(ctx) != nil {
		srv.Close()
	}
	host.StopComponents(context.Background())
	return host.Close()
}

// signInConfig reads the Keycloak client from the environment, naming
// every variable that is missing.
func signInConfig(getenv func(string) string) (auth.OIDC, error) {
	var missing []string
	for _, k := range []string{"DDASH_PUBLIC_URL", "DDASH_OIDC_ISSUER", "DDASH_OIDC_CLIENT_ID", "DDASH_OIDC_CLIENT_SECRET"} {
		if getenv(k) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return auth.OIDC{}, fmt.Errorf("sign-in needs %s", strings.Join(missing, ", "))
	}
	return auth.OIDC{
		Issuer:       getenv("DDASH_OIDC_ISSUER"),
		ClientID:     getenv("DDASH_OIDC_CLIENT_ID"),
		ClientSecret: getenv("DDASH_OIDC_CLIENT_SECRET"),
	}, nil
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
