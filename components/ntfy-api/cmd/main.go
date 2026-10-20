// The ntfy-api Component binary, launched by the ddash host.
package main

import (
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(ntfyapi.Definition())
}
