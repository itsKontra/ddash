package nginxproxymanagerapi

import (
	"time"

	"github.com/itsKontra/ddash/sdk"
)

// DefinitionAt is nginx-proxy-manager-api telling the time by now, so a
// test can fix the certificates' states.
func DefinitionAt(now func() time.Time) sdk.Definition { return define(now) }
