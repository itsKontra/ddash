// dev is `make dev`: it runs the whole system locally against fakelab.
//
//  1. It builds every Component into dist/ in the Release layout, as
//     Release dev (internal/release), linking each Plugin's bundle from
//     plugins/<name>/dist/ (`make dev` runs `make build` first).
//  2. It starts fakelab and seeds every Component's config to point at it.
//  3. It installs every Plugin in the Release.
//  4. It runs the core, built with -tags dev and signed in as operator:dev,
//     with DDASH_RELEASE_DIR=dist, until interrupted.
//  5. It rebuilds every Plugin's bundle on change (`vite build --watch`;
//     reload the page to see it, there is no HMR) and serves the shell from
//     the Vite dev server, which proxies /api, /plugins and /vendor to the
//     core: open http://127.0.0.1:5173.
//
// Run it from the repo root:
//
//	go run ./cmd/dev [-listen 127.0.0.1:8080] [-data .dev/data]
package main

import (
	"context"
	"encoding/json"
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
	if err := release.Build(ctx, release.Options{Root: ".", Out: dist, Release: "dev", LinkBundles: true}); err != nil {
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

	pnpm, err := pnpmCommand()
	if err != nil {
		return err
	}
	watch, err := background(ctx, append(pnpm, "-r", "--parallel", "--filter", "./plugins/*", "dev")...)
	if err != nil {
		return err
	}
	defer watch()
	vite, err := background(ctx, append(pnpm, "--filter", "@ddash/shell", "dev", "--host", "127.0.0.1", "--strictPort")...)
	if err != nil {
		return err
	}
	defer vite()

	slog.Info("running the core", "core", "http://"+listen, "open", "http://127.0.0.1:5173")
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

// pnpmCommand is how to run the repo's pinned pnpm: npx with the version
// package.json's packageManager names.
func pnpmCommand() ([]string, error) {
	b, err := os.ReadFile("package.json")
	if err != nil {
		return nil, err
	}
	var pkg struct {
		PackageManager string `json:"packageManager"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil || pkg.PackageManager == "" {
		return nil, fmt.Errorf("package.json names no packageManager: %v", err)
	}
	return []string{"npx", "-y", pkg.PackageManager}, nil
}

// background runs args in its own process group until ctx ends or the
// returned stop is called, which ends the whole group (pnpm's Vite children
// too) and waits for it.
func background(ctx context.Context, args ...string) (stop func(), err error) {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait() // it ends with the group
		close(done)
	}()
	kill := func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	go func() {
		select {
		case <-ctx.Done():
			kill()
		case <-done:
		}
	}()
	return func() { kill(); <-done }, nil
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
