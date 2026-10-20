// A sample Component binary for the host's subprocess tests. It is named
// after the file it runs from, unless built with -ldflags "-X main.name=…".
package main

import (
	"os"
	"path/filepath"

	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/sdk"
)

// name is the name the sample says it has in Describe.
var name string

func main() {
	if name == "" {
		name = filepath.Base(os.Args[0])
	}
	sdk.Serve(sample.Define(name, nil, sample.Hooks{}))
}
