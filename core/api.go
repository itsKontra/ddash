package core

import (
	"context"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	corepb "github.com/itsKontra/ddash/proto/ddash/core"
	"github.com/itsKontra/ddash/proto/ddash/core/corepbconnect"
)

// apiPrefix is where the core API is mounted, as connect-go handlers.
const apiPrefix = "/api/core"

// coreAPI serves CoreService at apiPrefix. Methods it doesn't implement yet
// answer unimplemented.
type coreAPI struct {
	corepbconnect.UnimplementedCoreServiceHandler
	host *Host
}

// apiHandler is the core API's http.Handler, to mount at apiPrefix+"/".
func (h *Host) apiHandler() http.Handler {
	server := connect.NewServer()
	corepbconnect.RegisterCoreServiceHandler(server, coreAPI{host: h})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	return http.StripPrefix(apiPrefix, mux)
}

func (a coreAPI) ListInstalledPlugins(context.Context, *corepb.ListInstalledPluginsRequest) (*corepb.ListInstalledPluginsResponse, error) {
	rel := a.host.runtime.Release()
	resp := &corepb.ListInstalledPluginsResponse{}
	for _, p := range a.host.runtime.Installed() {
		base := bundlePrefix + p.Name + "/" + rel + "/"
		resp.Plugins = append(resp.Plugins, &corepb.InstalledPlugin{
			Name:        p.Name,
			Title:       p.Title,
			Description: p.Description,
			Icon:        p.Icon,
			Author:      p.Author,
			Requires:    p.Requires,
			Script:      base + bundleScript,
			Style:       base + bundleStyle,
		})
	}
	return resp, nil
}
