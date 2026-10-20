// dev is `make dev`: it runs the whole system locally against fakelab.
//
//  1. It builds every Component into dist/ in the Release layout, as
//     Release dev (internal/release).
//  2. It starts fakelab and seeds every Component's config to point at it.
//  3. It installs every Plugin in the Release.
//  4. It runs the core, built with -tags dev and signed in as operator:dev,
//     with DDASH_RELEASE_DIR=dist, until interrupted.
//
// Run it from the repo root:
//
//	go run ./cmd/dev [-listen 127.0.0.1:8080] [-data .dev/data]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/itsKontra/ddash/core"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/internal/release"
	corepb "github.com/itsKontra/ddash/proto/ddash/core"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "the address the core serves HTTP on")
	data := flag.String("data", ".dev/data", "the core's data dir; it is kept between runs")
	dist := flag.String("dist", "dist", "the Release dir to build")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *listen, *data, *dist); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "dev:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, listen, data, dist string) error {
	dist, err := filepath.Abs(dist)
	if err != nil {
		return err
	}
	slog.Info("building the Release", "dir", dist)
	if err := os.RemoveAll(dist); err != nil {
		return err
	}
	if err := release.Build(ctx, release.Options{Root: ".", Out: dist, Release: "dev"}); err != nil {
		return err
	}

	lab, err := fakelab.Start(".", "127.0.0.1")
	if err != nil {
		return err
	}
	defer lab.Close()
	for component, addr := range lab.Addrs {
		slog.Info("fakelab", "component", component, "addr", addr)
	}
	if err := seed(ctx, data, dist, lab); err != nil {
		return err
	}

	bin := filepath.Join(filepath.Dir(data), "ddash")
	build := exec.CommandContext(ctx, "go", "build", "-tags", "dev", "-o", bin, "./cmd/ddash")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("build the core: %w", err)
	}

	slog.Info("running the core", "url", "http://"+listen)
	// The core gets the same SIGINT from the terminal and shuts down on it;
	// wait for that instead of killing it.
	srv := exec.Command(bin)
	srv.Env = append(os.Environ(),
		"DDASH_LISTEN="+listen,
		"DDASH_DATA_DIR="+data,
		"DDASH_RELEASE_DIR="+dist,
		"DDASH_PUBLIC_URL=http://"+listen,
		"DDASH_DEV_AUTH=1",
	)
	srv.Stdout, srv.Stderr = os.Stdout, os.Stderr
	if err := srv.Start(); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		srv.Process.Signal(syscall.SIGTERM) //nolint:errcheck // it may have exited
	}()
	return srv.Wait()
}

// seed points every Component's config at the lab and installs every Plugin
// in the Release, in the core DB under data.
func seed(ctx context.Context, data, dist string, lab *fakelab.Lab) error {
	host, err := core.New(ctx, core.Options{
		DataDir:  data,
		Release:  runtime.Dir(dist),
		Launcher: runtime.Subprocess(dist, nil),
	})
	if err != nil {
		return err
	}
	defer host.Shutdown(context.WithoutCancel(ctx)) //nolint:errcheck // nothing ran
	for component, cfg := range lab.Config() {
		if err := host.Config().Put(ctx, component, cfg); err != nil {
			return err
		}
	}
	cat, err := runtime.Dir(dist).Catalogue(ctx)
	if err != nil {
		return err
	}
	for _, p := range cat.Plugins {
		if _, err := host.Install(ctx, &corepb.InstallRequest{Plugin: p.Name}); err != nil {
			return fmt.Errorf("install %s: %w", p.Name, err)
		}
		slog.Info("installed", "plugin", p.Name)
	}
	return nil
}
