// The adguard-api Component binary, launched by the ddash host.
package main

import (
	adguardapi "github.com/itsKontra/ddash/components/adguard-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(adguardapi.Definition())
}
