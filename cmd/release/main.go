// release builds a Release dir from the repo: catalogue.json merged from
// every plugin.yaml and component.yaml, one binary per Component, each
// Plugin's bundle and SHA256SUMS. It fails on a requires that doesn't
// resolve. With -core it builds the core binary (the image's) instead.
//
//	go run ./cmd/release -out dist -release dev
//	go run ./cmd/release -out dist -release v1.2.3 -platform linux/amd64
//	go run ./cmd/release -core bin/image/ddash -release v1.2.3 -platform linux/amd64
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
	rel := flag.String("release", "dev", "the Release to stamp: dev or a tag vMAJOR.MINOR.PATCH")
	platform := flag.String("platform", "", "GOOS/GOARCH to build for (default: this machine's)")
	core := flag.String("core", "", "build the core binary to this path instead of the Release dir")
	flag.Parse()
	var err error
	if *core != "" {
		err = release.Core(context.Background(), ".", *core, *rel, *platform)
	} else {
		err = release.Build(context.Background(), release.Options{Root: ".", Out: *out, Release: *rel, Platform: *platform})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}
