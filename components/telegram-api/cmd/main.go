// The telegram-api Component binary, launched by the ddash host.
package main

import (
	telegramapi "github.com/itsKontra/ddash/components/telegram-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(telegramapi.Definition())
}
