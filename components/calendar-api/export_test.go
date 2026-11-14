package calendarapi

import (
	"net/http"
	"time"

	"github.com/itsKontra/ddash/sdk"
)

// DefineAt is calendar-api with its clock stopped at now.
func DefineAt(now time.Time) sdk.Definition {
	return define(func() time.Time { return now }, &http.Client{Timeout: readTimeout})
}
