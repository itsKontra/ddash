// The cloudflare-api Component binary, launched by the ddash host.
package main

import (
	cloudflareapi "github.com/itsKontra/ddash/components/cloudflare-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(cloudflareapi.Definition())
}
