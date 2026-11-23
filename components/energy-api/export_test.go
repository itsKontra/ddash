package energyapi

import (
	"time"

	"github.com/itsKontra/ddash/sdk"
)

// DefineAt is energy-api with its clock stopped at now, without the
// background warm-up of its cache.
func DefineAt(now time.Time) sdk.Definition {
	return define(func() time.Time { return now }, false)
}

// DefineOn is energy-api on the clock now, without the background
// warm-up of its cache.
func DefineOn(now func() time.Time) sdk.Definition {
	return define(now, false)
}

// DefineWarmingAt is DefineAt warming its cache in the background.
func DefineWarmingAt(now time.Time) sdk.Definition {
	return define(func() time.Time { return now }, true)
}

// AlgoVersion is the version of the hours' attribution.
const AlgoVersion = algoVersion
