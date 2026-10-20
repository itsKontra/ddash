// Package apps is the apps Component: every nginx Proxy Host becomes an App,
// the Operator adds manual Apps, and an overlay of display settings, keyed
// by a Proxy Host's first server_name, outlives the Proxy Host it was set on.
//
// Every PollInterval it reads nginx-config's snapshot and rediscovers the
// Apps when its revision changed; ListApps joins them with the overlays and
// manual Apps in the DB at read time.
package apps

import (
	"cmp"
	"context"
	"embed"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

//go:embed migrations/*.sql
var migrations embed.FS

// PollInterval is how often apps reads nginx-config's snapshot revision, so
// a Refresh there reaches Apps within a minute.
const PollInterval = 60 * time.Second

// Definition is apps, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*appspb.AppsConfig]{
		Manifest:   manifest,
		Service:    &appspb.AppsService_ServiceDesc,
		Migrations: migrations,
		New:        func() sdk.Component[*appspb.AppsConfig] { return &component{} },
	})
}

type component struct {
	appspb.UnimplementedAppsServiceServer

	nginx nginxconfigpb.NginxConfigServiceClient
	store store

	// polled is closed once the first poll ended, so ListApps never
	// answers before Apps were first discovered.
	polled chan struct{}

	// writeMu makes Actions take turns, so a check and the write it
	// guards see the same DB.
	writeMu sync.Mutex

	mu       sync.Mutex
	polls    int
	read     bool // a snapshot was read at least once
	revision int64
	found    []discovered
	readErr  string
}

func (c *component) Start(ctx context.Context, env *sdk.Env, _ *appspb.AppsConfig) error {
	c.nginx = sdk.Require(env, nginxconfigpb.NewNginxConfigServiceClient)
	c.store = store{db: env.DB}
	c.polled = make(chan struct{})
	sdk.Every(ctx, PollInterval, c.poll)
	return nil
}

func (c *component) Health(context.Context) sdk.Health {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != "" {
		return sdk.Health{State: sdk.Degraded, Message: "nginx-config unavailable: " + c.readErr}
	}
	return sdk.Health{State: sdk.OK}
}

// poll reads nginx-config's snapshot and rediscovers the Apps when its
// revision changed. A failed read keeps the last Apps.
func (c *component) poll(ctx context.Context) {
	resp, err := c.nginx.GetSnapshot(ctx, &nginxconfigpb.GetSnapshotRequest{})

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.polls++; c.polls == 1 {
		defer close(c.polled)
	}
	if err != nil {
		c.readErr = status.Convert(err).Message()
		return
	}
	c.readErr = ""
	snap := resp.GetSnapshot()
	// "Changed", not "grew": a restarted nginx-config counts from 1 again.
	if c.read && snap.GetRevision() == c.revision {
		return
	}
	c.read, c.revision = true, snap.GetRevision()
	c.found = discover(snap.GetServers())
}

// discovered returns the discovered Apps and why the last read failed,
// waiting for the first poll.
func (c *component) discovered(ctx context.Context) ([]discovered, string, error) {
	select {
	case <-c.polled:
	case <-ctx.Done():
		return nil, "", status.FromContextError(ctx.Err()).Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.found, c.readErr, nil
}

func (c *component) ListApps(ctx context.Context, _ *appspb.ListAppsRequest) (*appspb.ListAppsResponse, error) {
	found, readErr, err := c.discovered(ctx)
	if err != nil {
		return nil, err
	}

	overlays, err := c.store.overlays(ctx)
	if err != nil {
		return nil, err
	}
	manual, err := c.store.manualApps(ctx)
	if err != nil {
		return nil, err
	}

	resp := &appspb.ListAppsResponse{DiscoveryError: readErr}
	matched := map[string]bool{}
	for _, d := range found {
		o := overlays[d.serverName]
		matched[d.serverName] = o != nil
		resp.Apps = append(resp.Apps, d.app(o))
	}
	resp.Apps = append(resp.Apps, manual...)
	slices.SortFunc(resp.Apps, func(a, b *appspb.App) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.GetName()), strings.ToLower(b.GetName())), cmp.Compare(a.GetId(), b.GetId()))
	})
	for key, o := range overlays {
		if !matched[key] {
			resp.Unmatched = append(resp.Unmatched, o)
		}
	}
	slices.SortFunc(resp.Unmatched, func(a, b *appspb.Overlay) int { return cmp.Compare(a.GetServerName(), b.GetServerName()) })
	return resp, nil
}
