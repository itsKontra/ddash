// The energy-api Component binary, launched by the ddash host.
package main

import (
	energyapi "github.com/itsKontra/ddash/components/energy-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(energyapi.Definition())
}
