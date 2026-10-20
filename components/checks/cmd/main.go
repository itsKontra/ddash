// The checks Component binary, launched by the ddash host.
package main

import (
	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(checks.Definition())
}
