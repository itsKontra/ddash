// The network Component binary, launched by the ddash host.
package main

import (
	"github.com/itsKontra/ddash/components/network"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(network.Definition())
}
