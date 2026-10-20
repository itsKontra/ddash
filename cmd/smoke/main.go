// smoke runs every system Component's getters against the real homelab
// systems in the gitignored smoke.env and checks that they decode and that
// their invariants hold (`make smoke`). With -record it also writes what
// they answered as fixtures, scrubbed with smoke.env's SCRUB_* and the
// gitignored scrub.map (`make record`). Run it by hand before a Release,
// never in CI or on a schedule; smoke.env.example says what to fill in.
//
//	go run ./cmd/smoke [-env smoke.env] [-data .smoke]
//	go run ./cmd/smoke -record [-scenario recorded] [-map scrub.map] [-root .]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/itsKontra/ddash/internal/smoke"
)

func main() {
	envPath := flag.String("env", "smoke.env", "the systems to run against (gitignored)")
	data := flag.String("data", ".smoke", "where the Components keep their DBs between runs (nginx-config's key)")
	record := flag.Bool("record", false, "also write each answer as a scrubbed fixture")
	scenario := flag.String("scenario", "recorded", "the scenario the fixtures go to: components/<name>/testdata/<scenario>/")
	mapPath := flag.String("map", "scrub.map", "the names to scrub, one \"real = replacement\" per line (gitignored)")
	root := flag.String("root", ".", "the repo")
	flag.Parse()

	if err := run(*envPath, *data, *record, *scenario, *mapPath, *root); err != nil {
		fmt.Fprintln(os.Stderr, "smoke:", err)
		os.Exit(1)
	}
}

func run(envPath, data string, record bool, scenario, mapPath, root string) error {
	env, err := smoke.ReadEnv(envPath)
	if err != nil {
		return fmt.Errorf("%w (copy smoke.env.example to smoke.env and fill it in)", err)
	}
	opts := smoke.Options{Env: env, DataDir: data, Log: os.Stdout}
	if record {
		s, err := smoke.Scrubber(env, mapPath)
		if err != nil {
			return err
		}
		opts.Record = &smoke.Record{Root: root, Scenario: scenario, Scrubber: s}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return smoke.Run(ctx, opts)
}
