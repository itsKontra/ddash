// The nginx-proxy-manager-api Component binary, launched by the ddash host.
package main

import (
	nginxproxymanagerapi "github.com/itsKontra/ddash/components/nginx-proxy-manager-api"
	"github.com/itsKontra/ddash/sdk"
)

func main() {
	sdk.Serve(nginxproxymanagerapi.Definition())
}
