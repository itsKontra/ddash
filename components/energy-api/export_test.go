package energyapi

import (
	"time"

	"github.com/itsKontra/ddash/sdk"
)

// DefineAt is energy-api with its clock stopped at now.
func DefineAt(now time.Time) sdk.Definition {
	return define(func() time.Time { return now })
}
