// The proxmox Component binary, launched by the ddash host.
package main

import (
	"github.com/itsKontra/ddash/components/proxmox"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(proxmox.Definition())
}
