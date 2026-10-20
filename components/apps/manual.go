package apps

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps/appspb"
)

// manualPrefix starts every manual App's id; discovered ones start "proxy:".
const manualPrefix = "manual:"

func (c *component) CreateApp(ctx context.Context, req *appspb.CreateAppRequest) (*appspb.CreateAppResponse, error) {
	a, err := manualApp(req.GetApp())
	if err != nil {
		return nil, err
	}
	a.Id = manualPrefix + newID()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.store.insertManual(ctx, a); err != nil {
		return nil, err
	}
	c.syncChecks(ctx)
	return &appspb.CreateAppResponse{App: a}, nil
}

func (c *component) UpdateApp(ctx context.Context, req *appspb.UpdateAppRequest) (*appspb.UpdateAppResponse, error) {
	a, err := manualApp(req.GetApp())
	if err != nil {
		return nil, err
	}
	a.Id = req.GetId()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if ok, err := c.store.updateManual(ctx, a); err != nil {
		return nil, err
	} else if !ok {
		return nil, notManual(a.GetId())
	}
	c.syncChecks(ctx)
	return &appspb.UpdateAppResponse{App: a}, nil
}

func (c *component) DeleteApp(ctx context.Context, req *appspb.DeleteAppRequest) (*appspb.DeleteAppResponse, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if ok, err := c.store.deleteManual(ctx, req.GetId()); err != nil {
		return nil, err
	} else if !ok {
		return nil, notManual(req.GetId())
	}
	c.syncChecks(ctx)
	return &appspb.DeleteAppResponse{}, nil
}

func notManual(id string) error {
	return status.Errorf(codes.NotFound, "no manual App has the id %q", id)
}

// manualApp checks what the Operator gave a manual App.
func manualApp(m *appspb.ManualApp) (*appspb.App, error) {
	a := &appspb.App{
		Manual: true,
		Name:   strings.TrimSpace(m.GetName()),
		Url:    strings.TrimSpace(m.GetUrl()),
		Icon:   strings.ToLower(strings.TrimSpace(m.GetIcon())),
		Group:  strings.TrimSpace(m.GetGroup()),
	}
	if a.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	u, err := url.Parse(a.GetUrl())
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, status.Error(codes.InvalidArgument, "url must be an http or https URL, e.g. https://198.18.0.1")
	}
	if err := checkIcon(a.GetIcon()); err != nil {
		return nil, err
	}
	return a, nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // never fails (crypto/rand)
	return hex.EncodeToString(b)
}
