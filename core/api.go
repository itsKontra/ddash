package core

import (
	"context"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/core/appstate"

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

// connectError answers err, a gRPC status error, with its code and message.
// connect-go's codes are gRPC's. Any other error is Unknown.
func connectError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	return connect.NewError(connect.Code(st.Code()), st.Message())
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

func (a coreAPI) ListAuditLog(ctx context.Context, req *corepb.ListAuditLogRequest) (*corepb.ListAuditLogResponse, error) {
	page, err := a.host.gate.Chains(ctx, appstate.Query{
		ActorKind: req.GetActorKind(),
		Target:    req.GetTarget(),
		Outcome:   req.GetOutcome(),
		PageSize:  int(req.GetPageSize()),
		PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, connectError(err)
	}
	targets, err := a.host.gate.Targets(ctx)
	if err != nil {
		return nil, connectError(err)
	}
	resp := &corepb.ListAuditLogResponse{NextPageToken: page.Next, Targets: targets}
	for _, c := range page.Chains {
		chain := &corepb.AuditChain{Id: c.ID}
		for _, e := range c.Entries {
			entry := &corepb.AuditEntry{
				Time:       timestamppb.New(e.Time),
				ActorKind:  e.ActorKind,
				Username:   e.Username,
				Sub:        e.Sub,
				Target:     e.Target,
				Method:     e.Method,
				Disruptive: e.Disruptive,
				Request:    e.Request,
				Outcome:    e.Outcome,
				Message:    e.Message,
			}
			if e.Outcome != appstate.Running {
				entry.Duration = durationpb.New(e.Duration)
			}
			chain.Entries = append(chain.Entries, entry)
		}
		resp.Chains = append(resp.Chains, chain)
	}
	return resp, nil
}
