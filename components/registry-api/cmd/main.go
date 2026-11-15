// The registry-api Component binary, launched by the ddash host.
package main

import (
	registryapi "github.com/itsKontra/ddash/components/registry-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(registryapi.Definition())
}
