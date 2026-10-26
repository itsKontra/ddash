package apps

import (
	"time"

	"github.com/itsKontra/ddash/sdk"
)

// DefinitionResyncingWithin is apps bounding the sync that ends an App
// Action to resync instead of ResyncTimeout, so a test can make it time
// out quickly.
func DefinitionResyncingWithin(resync time.Duration) sdk.Definition { return define(resync) }
