// The nginx-config Component binary, launched by the ddash host.
package main

import (
	nginxconfig "github.com/itsKontra/ddash/components/nginx-config"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(nginxconfig.Definition())
}
