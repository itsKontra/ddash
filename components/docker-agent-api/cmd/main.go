// The docker-agent-api Component binary, launched by the ddash host.
package main

import (
	dockeragentapi "github.com/itsKontra/ddash/components/docker-agent-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(dockeragentapi.Definition())
}
