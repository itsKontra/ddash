// Command gzip compresses the lucide JSON that packages/ui/scripts/icons.ts
// writes into core/icons/lucide, replacing each <name>.json with
// <name>.json.gz. Node's zlib picks CPU-specific code paths, so its output
// differs between machines; Go's compress/gzip gives the same bytes for a
// given toolchain, which `make check-generated` relies on.
package main

import (
	"bytes"
	"compress/gzip"
	"log"
	"os"
	"path/filepath"
)

func main() {
	files, err := filepath.Glob("core/icons/lucide/*.json")
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			log.Fatal(err)
		}
		var out bytes.Buffer
		zw, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := zw.Write(b); err != nil {
			log.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(f+".gz", out.Bytes(), 0o644); err != nil {
			log.Fatal(err)
		}
		if err := os.Remove(f); err != nil {
			log.Fatal(err)
		}
	}
}
