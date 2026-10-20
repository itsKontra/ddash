// Package apps is the apps Component: every nginx Proxy Host becomes an App,
// the Operator adds manual Apps, and an overlay of display settings, keyed
// by a Proxy Host's first server_name, outlives the Proxy Host it was set on.
//
// Every PollInterval it reads nginx-config's snapshot and rediscovers the
// Apps when its revision changed, and reads the Workloads proxmox-api and
// docker-api list; ListApps joins them with the overlays and manual Apps in
// the DB at read time, linking each App to the Workload at its upstream.
// Every App gets an HTTP(S) Check: apps declares the whole set to checks
// whenever it changes.
package apps

import (
	"cmp"
	"context"
	"embed"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
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

	nginx   nginxconfigpb.NginxConfigServiceClient
	checks  checkspb.ChecksServiceClient
	proxmox proxmoxapipb.ProxmoxApiServiceClient
	docker  dockerapipb.DockerApiServiceClient
	store   store
	log     *slog.Logger

	// polled is closed once the first poll ended, so ListApps never
	// answers before Apps were first discovered and declared to checks.
	polled    chan struct{}
	firstPoll sync.Once

	// writeMu makes Actions take turns, so a check and the write it
	// guards see the same DB.
	writeMu sync.Mutex

	mu       sync.Mutex
	wasRead  bool // a snapshot was read at least once
	revision int64
	found    []discovered
	// hosts are the Proxy Hosts of the last snapshot read.
	hosts   []proxyHost
	readErr string
	// checksErr is why the last SetAppChecks failed; empty once one worked.
	checksErr string
	// src is the last read of the Workload sources.
	src sources
	// nginxDown is why nginx-config's last snapshot says the `nginx -T`
	// read failed, empty when it worked; unknown until a snapshot was read.
	nginxDown  string
	nginxKnown bool

	// syncMu makes declarations to checks take turns; sent is the set
	// checks last accepted, nil before the first since Start, and
	// reported the nginx config item as checks last accepted it.
	syncMu   sync.Mutex
	sent     []*checkspb.AppCheck
	reported *checkspb.ReportStateRequest
}

func (c *component) Start(ctx context.Context, env *sdk.Env, _ *appspb.AppsConfig) error {
	c.nginx = sdk.Require(env, nginxconfigpb.NewNginxConfigServiceClient)
	c.checks = sdk.Require(env, checkspb.NewChecksServiceClient)
	c.proxmox = sdk.Require(env, proxmoxapipb.NewProxmoxApiServiceClient)
	c.docker = sdk.Require(env, dockerapipb.NewDockerApiServiceClient)
	c.store = store{db: env.DB}
	c.log = env.Log
	c.polled = make(chan struct{})
	sdk.Every(ctx, PollInterval, c.poll)
	return nil
}

// Health is degraded, naming each, while a required Component can't be
// read: Apps are then those of the last nginx-config read, or listed
// without the Workloads of an unavailable source.
func (c *component) Health(context.Context) sdk.Health {
	c.mu.Lock()
	defer c.mu.Unlock()
	var problems []string
	if c.readErr != "" {
		problems = append(problems, "nginx-config unavailable: "+c.readErr)
	}
	if c.checksErr != "" {
		problems = append(problems, "checks unavailable: "+c.checksErr)
	}
	problems = append(problems, c.src.problems...)
	if len(problems) > 0 {
		return sdk.Health{State: sdk.Degraded, Message: strings.Join(problems, "; ")}
	}
	return sdk.Health{State: sdk.OK}
}

// poll reads nginx-config's snapshot and rediscovers the Apps when its
// revision changed, then declares the App Checks if they changed (or a
// declaration failed). A failed read keeps the last Apps.
func (c *component) poll(ctx context.Context) {
	defer c.firstPoll.Do(func() { close(c.polled) })
	c.read(ctx)
	src := c.readSources(ctx)
	c.mu.Lock()
	c.src = src
	c.mu.Unlock()
	c.syncChecks(ctx)
	c.reportNginx(ctx)
}

// read reads nginx-config's snapshot and rediscovers the Apps when its
// revision changed.
func (c *component) read(ctx context.Context) {
	resp, err := c.nginx.GetSnapshot(ctx, &nginxconfigpb.GetSnapshotRequest{})

	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.readErr = status.Convert(err).Message()
		return
	}
	c.readErr = ""
	snap := resp.GetSnapshot()
	c.nginxKnown, c.nginxDown = true, firstLine(snap.GetError())
	// "Changed", not "grew": a restarted nginx-config counts from 1 again.
	if c.wasRead && snap.GetRevision() == c.revision {
		return
	}
	c.wasRead, c.revision = true, snap.GetRevision()
	c.found = discover(snap)
	c.hosts = proxyHostsOf(snap)
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
	all, overlays, err := c.joined(ctx, found)
	if err != nil {
		return nil, err
	}

	resp := &appspb.ListAppsResponse{Apps: all, DiscoveryError: readErr}
	slices.SortFunc(resp.Apps, func(a, b *appspb.App) int {
		return cmp.Or(cmp.Compare(strings.ToLower(a.GetName()), strings.ToLower(b.GetName())), cmp.Compare(a.GetId(), b.GetId()))
	})
	for key, o := range overlays {
		if !slices.ContainsFunc(found, func(d discovered) bool { return d.serverName == key }) {
			resp.Unmatched = append(resp.Unmatched, o)
		}
	}
	slices.SortFunc(resp.Unmatched, func(a, b *appspb.Overlay) int { return cmp.Compare(a.GetServerName(), b.GetServerName()) })
	return resp, nil
}

// joined returns the discovered Apps with their overlays applied, then the
// manual Apps, each with its Workload, and every overlay by server_name.
func (c *component) joined(ctx context.Context, found []discovered) ([]*appspb.App, map[string]*appspb.Overlay, error) {
	overlays, err := c.store.overlays(ctx)
	if err != nil {
		return nil, nil, err
	}
	manual, err := c.store.manualApps(ctx)
	if err != nil {
		return nil, nil, err
	}
	c.mu.Lock()
	src := c.src
	c.mu.Unlock()
	var out []*appspb.App
	for _, d := range found {
		out = append(out, d.app(overlays[d.serverName], src))
	}
	for _, a := range manual {
		a.Workload = src.match(urlTarget(a.GetUrl()))
	}
	return append(out, manual...), overlays, nil
}
