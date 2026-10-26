// Package apps is the apps Component: it keeps a record per App, under an
// opaque id that outlives URL changes, discovered from Nginx Proxy
// Manager's Proxy Hosts or added as Link only, with the Operator's
// settings and Ignores.
//
// Every SyncInterval it reads nginx-proxy-manager-api's Proxy Hosts
// (cached there for sdk.TTLSlow) and the Workloads proxmox-api and
// docker-api list, attaches every Proxy Host to a record (attach), and
// declares every App's Check to checks when the set changed. ListApps
// derives each App's Reach, Workload and Warnings from the records and
// the last good reads (derive); only the records and Ignores are stored.
package apps

import (
	"context"
	"embed"
	"log/slog"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	npmpb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

//go:embed migrations/*.sql
var migrations embed.FS

// SyncInterval is how often apps re-reads its sources, so a change made in
// Nginx Proxy Manager reaches Apps within a minute.
const SyncInterval = 60 * time.Second

// npmName names nginx-proxy-manager-api in Health and errors.
const npmName = "nginx-proxy-manager-api"

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

	npm     npmpb.NginxProxyManagerApiServiceClient
	checks  checkspb.ChecksServiceClient
	proxmox proxmoxapipb.ProxmoxApiServiceClient
	docker  dockerapipb.DockerApiServiceClient
	store   store
	log     *slog.Logger

	// synced is closed once the first sync ended, so ListApps and Health
	// never answer before the sources were first read.
	synced    chan struct{}
	firstSync sync.Once

	// turn makes syncs and Actions take turns: each reads the records,
	// and writes them and the App Checks, alone.
	turn sync.Mutex

	mu sync.Mutex
	// hosts is the last good read of the Proxy Hosts, by id; nil before
	// the first since Start.
	hosts map[int64]*npmpb.ProxyHost
	// hostsAt is when that read was; hostsErr why the reads since failed,
	// since failingSince.
	hostsAt, failingSince time.Time
	hostsErr              error
	// src is the last read of the Workload sources.
	src sources
	// checksErr is why the last SetAppChecks failed; empty once one worked.
	checksErr string
	// sent is the set checks last accepted, nil before the first since
	// Start.
	sent []*checkspb.AppCheck
}

func (c *component) Start(ctx context.Context, env *sdk.Env, _ *appspb.AppsConfig) error {
	c.npm = sdk.Require(env, npmpb.NewNginxProxyManagerApiServiceClient)
	c.checks = sdk.Require(env, checkspb.NewChecksServiceClient)
	c.proxmox = sdk.Require(env, proxmoxapipb.NewProxmoxApiServiceClient)
	c.docker = sdk.Require(env, dockerapipb.NewDockerApiServiceClient)
	c.store = store{db: env.DB}
	c.log = env.Log
	c.synced = make(chan struct{})
	sdk.Every(ctx, SyncInterval, func(ctx context.Context) {
		defer c.firstSync.Do(func() { close(c.synced) })
		_ = c.sync(ctx, false)
	})
	return nil
}

// Health is degraded, naming each, while a required Component can't be
// read: Apps are then those of its last good read, or listed without the
// Workloads of an unavailable source. It waits for the first sync.
func (c *component) Health(ctx context.Context) sdk.Health {
	if err := c.waitSynced(ctx); err != nil {
		return sdk.Health{State: sdk.Degraded, Message: "apps hasn't read its sources yet"}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var problems []string
	if c.hostsErr != nil {
		problems = append(problems, sourceProblem(npmName, c.hostsErr))
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

func (c *component) waitSynced(ctx context.Context) error {
	select {
	case <-c.synced:
		return nil
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	}
}

// sync reads the sources (Nginx Proxy Manager's fresh, bypassing its
// cache, when fresh), attaches the Proxy Hosts of a good read to the
// records, and declares the App Checks. It returns why Nginx Proxy
// Manager couldn't be read; its last good read then stays.
func (c *component) sync(ctx context.Context, fresh bool) error {
	c.turn.Lock()
	defer c.turn.Unlock()
	resp, err := c.npm.ListProxyHosts(ctx, &npmpb.ListProxyHostsRequest{Fresh: fresh})
	src := c.readSources(ctx)

	c.mu.Lock()
	c.src = src
	if err != nil {
		if c.hostsErr == nil {
			c.failingSince = time.Now()
		}
		c.hostsErr = err
	} else {
		c.hostsErr, c.hostsAt = nil, time.Now()
		c.hosts = map[int64]*npmpb.ProxyHost{}
		for _, h := range resp.GetProxyHosts() {
			c.hosts[h.GetId()] = h
		}
	}
	c.mu.Unlock()

	if err == nil {
		if werr := c.attach(ctx, resp.GetProxyHosts()); werr != nil {
			c.log.Error("attach the Proxy Hosts to App records", "err", werr)
		}
	}
	c.syncChecks(ctx)
	return err
}

// attach attaches a good read's Proxy Hosts to the records and saves the
// result.
func (c *component) attach(ctx context.Context, hosts []*npmpb.ProxyHost) error {
	recs, err := c.store.records(ctx)
	if err != nil {
		return err
	}
	ch := attach(recs, hosts)
	return c.store.save(ctx, ch.put, ch.drop)
}

// view is a copy of what the Apps are derived from besides their records.
func (c *component) view() view {
	c.mu.Lock()
	defer c.mu.Unlock()
	return view{hosts: c.hosts, src: c.src}
}

// apps derives every App from the records now.
func (c *component) apps(ctx context.Context) ([]*appspb.App, error) {
	recs, err := c.store.records(ctx)
	if err != nil {
		return nil, err
	}
	return derive(recs, c.view()), nil
}

func (c *component) ListApps(ctx context.Context, _ *appspb.ListAppsRequest) (*appspb.ListAppsResponse, error) {
	if err := c.waitSynced(ctx); err != nil {
		return nil, err
	}
	all, err := c.apps(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	read := &appspb.SourceRead{}
	if !c.hostsAt.IsZero() {
		read.ReadAt = timestamppb.New(c.hostsAt)
	}
	if c.hostsErr != nil {
		read.Error = status.Convert(c.hostsErr).Message()
		read.FailingSince = timestamppb.New(c.failingSince)
	}
	return &appspb.ListAppsResponse{Apps: all, ProxyHosts: read}, nil
}

func (c *component) Refresh(ctx context.Context, _ *appspb.RefreshRequest) (*appspb.RefreshResponse, error) {
	if _, err := c.npm.Refresh(ctx, &npmpb.RefreshRequest{}); err != nil {
		return nil, err
	}
	if err := c.sync(ctx, true); err != nil {
		return nil, status.Errorf(codes.Unavailable, "couldn't read %s: %s", npmName, status.Convert(err).Message())
	}
	return &appspb.RefreshResponse{}, nil
}
