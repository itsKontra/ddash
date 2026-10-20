// release builds a Release dir from the repo: catalogue.json merged from
// every plugin.yaml and component.yaml, one binary per Component and
// SHA256SUMS. It fails on a requires that doesn't resolve.
//
//	go run ./cmd/release -out dist -release dev
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/itsKontra/ddash/internal/release"
)

func main() {
	out := flag.String("out", "dist", "the Release dir to fill")
	rel := flag.String("release", "dev", "the Release to stamp")
	flag.Parse()
	if err := release.Build(context.Background(), release.Options{Root: ".", Out: *out, Release: *rel}); err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}
