// The proxmox-api Component binary, launched by the ddash host.
package main

import (
	proxmoxapi "github.com/itsKontra/ddash/components/proxmox-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(proxmoxapi.Definition())
}
