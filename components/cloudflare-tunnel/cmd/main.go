// The cloudflare-tunnel Component binary, launched by the ddash host.
package main

import (
	cloudflaretunnel "github.com/itsKontra/ddash/components/cloudflare-tunnel"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(cloudflaretunnel.Definition())
}
