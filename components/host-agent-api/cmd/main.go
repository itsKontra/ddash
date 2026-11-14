// The host-agent-api Component binary, launched by the ddash host.
package main

import (
	hostagentapi "github.com/itsKontra/ddash/components/host-agent-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(hostagentapi.Definition())
}
