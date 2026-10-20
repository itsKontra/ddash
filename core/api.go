package core

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/core/runtime"
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

func (a coreAPI) ListCatalogue(context.Context, *corepb.ListCatalogueRequest) (*corepb.ListCatalogueResponse, error) {
	rt := a.host.runtime
	cat := rt.Catalogue()
	installed := map[string]runtime.PluginEntry{}
	for _, p := range rt.Installed() {
		installed[p.Name] = p
	}
	running := rt.Status()
	resp := &corepb.ListCatalogueResponse{Release: rt.Release(), Orphans: rt.Orphans()}
	// The installed Plugins the catalogue lacks show from the core DB: it
	// can't be downloaded yet, or their Release dropped them.
	plugins := cat.Plugins
	for _, p := range installed {
		if !slices.ContainsFunc(plugins, func(c runtime.PluginEntry) bool { return c.Name == p.Name }) {
			plugins = append(plugins, p)
		}
	}
	for _, p := range plugins {
		_, isInstalled := installed[p.Name]
		cp := &corepb.CataloguePlugin{
			Name:        p.Name,
			Title:       p.Title,
			Description: p.Description,
			Icon:        p.Icon,
			Author:      p.Author,
			Installed:   isInstalled,
		}
		pulls := cat.Pulls(p.Name)
		if pulls == nil {
			pulls = p.Requires
		}
		for _, name := range pulls {
			st := running[name]
			cp.Components = append(cp.Components, &corepb.ComponentStatus{
				Name: name, State: string(st.State), Message: st.Message, Held: st.Held,
			})
		}
		resp.Plugins = append(resp.Plugins, cp)
	}
	slices.SortFunc(resp.Plugins, func(a, b *corepb.CataloguePlugin) int { return strings.Compare(a.Name, b.Name) })
	return resp, nil
}

func (a coreAPI) Install(ctx context.Context, req *corepb.InstallRequest) (*corepb.InstallResponse, error) {
	resp, err := a.host.Install(ctx, req)
	return resp, connectError(err)
}

func (a coreAPI) Uninstall(ctx context.Context, req *corepb.UninstallRequest) (*corepb.UninstallResponse, error) {
	resp, err := a.host.Uninstall(ctx, req)
	return resp, connectError(err)
}

func (a coreAPI) Restart(ctx context.Context, req *corepb.RestartRequest) (*corepb.RestartResponse, error) {
	resp, err := a.host.Restart(ctx, req)
	return resp, connectError(err)
}

func (a coreAPI) DeleteOrphan(ctx context.Context, req *corepb.DeleteOrphanRequest) (*corepb.DeleteOrphanResponse, error) {
	resp, err := a.host.DeleteOrphan(ctx, req)
	return resp, connectError(err)
}

// connectError carries a gRPC status error's code and message to the
// shell. Any other error is Unknown.
func connectError(err error) error {
	if err == nil {
		return nil
	}
	s := status.Convert(err)
	return connect.NewError(connect.Code(s.Code()), s.Message())
}
