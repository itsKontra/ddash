// ddash-docker-agent is the Docker Agent: the container, started from the
// Compose file ddash generates, that lets ddash list and change the
// host's Compose Projects. It mounts the Docker socket and each Compose
// root at the same path as on the host, and serves ddash over HTTPS.
//
//	ddash-docker-agent [serve]    serve (the image's default)
//	ddash-docker-agent recreate   the Update Agent helper (see dockeragent.Recreate)
//	ddash-docker-agent version
//
// Configuration, from the environment:
//
//	DDASH_ENROLMENT_CODE  the one-time code ddash showed (ignored once paired)
//	DDASH_COMPOSE_ROOTS   the Compose roots, comma-separated, e.g. /opt/stacks
//	DDASH_AGENT_LISTEN    default :7443
//	DDASH_AGENT_STATE     its certificate and pairing, default /data
//	DOCKER_HOST           default unix:///var/run/docker.sock
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/dockeragent"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch cmd {
	case "serve":
		err = serve(ctx, log)
	case "recreate":
		fs := flag.NewFlagSet("recreate", flag.ExitOnError)
		file := fs.String("file", "", "the Docker Agent's Compose file")
		dir := fs.String("dir", "", "its project directory")
		project := fs.String("project", "", "its Compose project")
		image := fs.String("image", "", "the image to recreate it on")
		_ = fs.Parse(os.Args[2:])
		err = dockeragent.Recreate(ctx, *file, *dir, *project, *image, dockeragent.CLI{})
	case "version":
		fmt.Println(agent.Release)
	default:
		err = fmt.Errorf("unknown command %q: serve, recreate or version", cmd)
	}
	if err != nil {
		log.Error("ddash-docker-agent", "err", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func serve(ctx context.Context, log *slog.Logger) error {
	var roots []string
	for r := range strings.SplitSeq(os.Getenv("DDASH_COMPOSE_ROOTS"), ",") {
		if r = strings.TrimSpace(r); r != "" {
			roots = append(roots, r)
		}
	}
	engine, err := dockeragent.NewEngine(env("DOCKER_HOST", "unix:///var/run/docker.sock"))
	if err != nil {
		return err
	}
	self, _ := os.Hostname() // a container's hostname is its short id
	ops, err := dockeragent.New(dockeragent.Options{Roots: roots, Docker: engine, Compose: dockeragent.CLI{}, Self: self})
	if err != nil {
		return err
	}
	srv, err := agent.NewServer(agent.ServerOptions{
		Kind:          agent.KindDocker,
		Release:       agent.Release,
		StateDir:      env("DDASH_AGENT_STATE", "/data"),
		EnrolmentCode: os.Getenv("DDASH_ENROLMENT_CODE"),
		Log:           log,
	})
	if err != nil {
		return err
	}
	srv.Jobs().SetContext(ctx)
	host := env("DDASH_HOST_NAME", self)
	ops.Register(srv, func(context.Context) agent.Info {
		return agent.Info{Kind: agent.KindDocker, Release: agent.Release, Hostname: host, Arch: runtime.GOARCH}
	})
	return srv.ListenAndServe(ctx, env("DDASH_AGENT_LISTEN", fmt.Sprintf(":%d", agent.DockerPort)))
}
