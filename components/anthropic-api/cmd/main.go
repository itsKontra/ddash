// The anthropic-api Component binary, launched by the ddash host.
package main

import (
	anthropicapi "github.com/itsKontra/ddash/components/anthropic-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(anthropicapi.Definition())
}
