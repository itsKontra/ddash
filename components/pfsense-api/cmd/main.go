// The pfsense-api Component binary, launched by the ddash host.
package main

import (
	pfsenseapi "github.com/itsKontra/ddash/components/pfsense-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(pfsenseapi.Definition())
}
