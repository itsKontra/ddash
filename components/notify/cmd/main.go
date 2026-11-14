// The notify Component binary, launched by the ddash host.
package main

import (
	"github.com/itsKontra/ddash/components/notify"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(notify.Definition())
}
