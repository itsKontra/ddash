// The apps Component binary, launched by the ddash host.
package main

import (
	"github.com/itsKontra/ddash/components/apps"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(apps.Definition())
}
