// The compose Component binary, launched by the ddash host.
package main

import (
	"github.com/itsKontra/ddash/components/compose"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(compose.Definition())
}
