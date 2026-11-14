package apps

import (
	"time"

	"github.com/itsKontra/ddash/sdk"
)

// DefinitionResyncingWithin is apps bounding the sync that ends an App
// Action to resync instead of ResyncTimeout, so a test can make it time
// out quickly.
func DefinitionResyncingWithin(resync time.Duration) sdk.Definition {
	return define(resync, ComposeWait)
}

// DefinitionWaitingWithin is apps following an Add App's new Compose
// Project for wait instead of ComposeWait, so a test can make it run out.
func DefinitionWaitingWithin(wait time.Duration) sdk.Definition {
	return define(ResyncTimeout, wait)
}
