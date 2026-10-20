// fakelab serves the homelab's recorded baseline fixtures on localhost, one
// port per external system, until interrupted. It prints each Component's
// config pointing at it. `make dev` runs the same lab in-process. -scenario
// serves named scenarios (comma-separated, e.g. vpn-on-fallback) in place of
// baseline for the systems that have them.
//
//	go run ./cmd/fakelab [-root .] [-host 127.0.0.1] [-scenario vpn-on-fallback]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/itsKontra/ddash/internal/fakelab"
)

func main() {
	root := flag.String("root", ".", "the repo, holding every Component's fixtures")
	host := flag.String("host", "127.0.0.1", "the address to serve on, each system on a free port")
	scenario := flag.String("scenario", "", "comma-separated scenarios to serve in place of baseline, e.g. vpn-on-fallback")
	flag.Parse()

	lab, err := fakelab.Start(*root, *host, fakelab.Scenarios(*scenario)...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fakelab:", err)
		os.Exit(1)
	}
	defer lab.Close()
	for component, cfg := range lab.Config() {
		b, _ := protojson.Marshal(cfg)
		fmt.Printf("%s: %s\n", component, b)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}
