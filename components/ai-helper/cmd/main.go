// The ai-helper Component binary, launched by the ddash host.
package main

import (
	aihelper "github.com/itsKontra/ddash/components/ai-helper"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(aihelper.Definition())
}
