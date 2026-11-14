// ddash-host-agent is the Host Agent: a root service on an LXC or VM that
// lets ddash update its packages and set up SSH. install.sh (served by
// ddash at /agent/host/install.sh) puts it at /usr/local/bin and registers
// it with systemd, or OpenRC on Alpine.
//
//	ddash-host-agent [serve]   serve
//	ddash-host-agent version
//
// Configuration, from /etc/ddash-host-agent/env (read by the service) or
// the environment:
//
//	DDASH_ENROLMENT_CODE  the one-time code ddash showed (ignored once paired)
//	DDASH_AGENT_LISTEN    default :7444
//	DDASH_AGENT_STATE     its certificate and pairing, default /var/lib/ddash-host-agent
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/hostagent"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := serve(ctx, log, stop); err != nil {
			log.Error("ddash-host-agent", "err", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println(agent.Release)
	default:
		log.Error("unknown command: serve or version", "command", cmd)
		os.Exit(2)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envFile is install.sh's config; the service manager may pass it too, but
// OpenRC's supervise-daemon doesn't, so the Agent reads it itself.
const envFile = "/etc/ddash-host-agent/env"

// loadEnv sets each KEY=value of envFile not already set.
func loadEnv() {
	b, err := os.ReadFile(envFile)
	if err != nil {
		return
	}
	for line := range strings.Lines(string(b)) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && !strings.HasPrefix(k, "#") && os.Getenv(k) == "" {
			os.Setenv(k, strings.Trim(v, `"'`))
		}
	}
}

func serve(ctx context.Context, log *slog.Logger, stop func()) error {
	loadEnv()
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	ops, err := hostagent.New(hostagent.Options{Binary: bin, Exit: stop})
	if err != nil {
		return err
	}
	srv, err := agent.NewServer(agent.ServerOptions{
		Kind:          agent.KindHost,
		Release:       agent.Release,
		StateDir:      env("DDASH_AGENT_STATE", "/var/lib/ddash-host-agent"),
		EnrolmentCode: os.Getenv("DDASH_ENROLMENT_CODE"),
		Log:           log,
	})
	if err != nil {
		return err
	}
	srv.Jobs().SetContext(ctx)
	host, _ := os.Hostname()
	ops.Register(srv, func(context.Context) agent.Info {
		return agent.Info{Kind: agent.KindHost, Release: agent.Release, Hostname: host, Arch: runtime.GOARCH}
	})
	go ops.Refresh(ctx)
	return srv.ListenAndServe(ctx, env("DDASH_AGENT_LISTEN", fmt.Sprintf(":%d", agent.HostPort)))
}
