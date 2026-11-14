// The calendar-api Component binary, launched by the ddash host.
package main

import (
	calendarapi "github.com/itsKontra/ddash/components/calendar-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(calendarapi.Definition())
}
