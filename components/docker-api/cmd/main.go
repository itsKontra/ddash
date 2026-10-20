// The docker-api Component binary, launched by the ddash host.
package main

import (
	dockerapi "github.com/itsKontra/ddash/components/docker-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(dockerapi.Definition())
}
