package apps

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps/appspb"
)

// iconName is a homarr-labs/dashboard-icons name, e.g. "home-assistant".
var iconName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func (c *component) SetOverlay(ctx context.Context, req *appspb.SetOverlayRequest) (*appspb.SetOverlayResponse, error) {
	o := req.GetOverlay()
	o = &appspb.Overlay{
		ServerName: strings.TrimSpace(o.GetServerName()),
		Name:       strings.TrimSpace(o.GetName()),
		Icon:       strings.ToLower(strings.TrimSpace(o.GetIcon())),
		Group:      strings.TrimSpace(o.GetGroup()),
		Hidden:     o.GetHidden(),
	}
	if o.GetServerName() == "" {
		return nil, status.Error(codes.InvalidArgument, "server_name is required")
	}
	if err := checkIcon(o.GetIcon()); err != nil {
		return nil, err
	}
	w, err := checkWorkloadRef(req.GetOverlay().GetWorkload())
	if err != nil {
		return nil, err
	}
	o.Workload = w

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	found, err := c.isDiscovered(ctx, o.GetServerName())
	if err != nil {
		return nil, err
	}
	if !found {
		exists, err := c.store.hasOverlay(ctx, o.GetServerName())
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, status.Errorf(codes.NotFound, "no App has the server_name %s", o.GetServerName())
		}
	}
	if o.GetName() == "" && o.GetIcon() == "" && o.GetGroup() == "" && !o.GetHidden() && o.GetWorkload() == nil {
		err = c.store.deleteOverlay(ctx, o.GetServerName())
	} else {
		err = c.store.putOverlay(ctx, o)
	}
	if err != nil {
		return nil, err
	}
	c.syncChecks(ctx)
	return &appspb.SetOverlayResponse{}, nil
}

func (c *component) AttachOverlay(ctx context.Context, req *appspb.AttachOverlayRequest) (*appspb.AttachOverlayResponse, error) {
	from, to := req.GetServerName(), req.GetTo()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	exists, err := c.store.hasOverlay(ctx, from)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "no settings are kept for %s", from)
	}
	if found, err := c.isDiscovered(ctx, to); err != nil {
		return nil, err
	} else if !found {
		return nil, status.Errorf(codes.NotFound, "no App has the server_name %s", to)
	}
	if taken, err := c.store.hasOverlay(ctx, to); err != nil {
		return nil, err
	} else if taken && from != to {
		return nil, status.Errorf(codes.FailedPrecondition, "%s already has settings: delete them first", to)
	}
	if err := c.store.moveOverlay(ctx, from, to); err != nil {
		return nil, err
	}
	c.syncChecks(ctx)
	return &appspb.AttachOverlayResponse{}, nil
}

func (c *component) DeleteOverlay(ctx context.Context, req *appspb.DeleteOverlayRequest) (*appspb.DeleteOverlayResponse, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	exists, err := c.store.hasOverlay(ctx, req.GetServerName())
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "no settings are kept for %s", req.GetServerName())
	}
	if err := c.store.deleteOverlay(ctx, req.GetServerName()); err != nil {
		return nil, err
	}
	c.syncChecks(ctx)
	return &appspb.DeleteOverlayResponse{}, nil
}

func checkIcon(icon string) error {
	if icon != "" && !iconName.MatchString(icon) {
		return status.Error(codes.InvalidArgument, "icon must be a dashboard-icons name, e.g. jellyfin or home-assistant")
	}
	return nil
}

// isDiscovered reports whether a discovered App has this first server_name.
func (c *component) isDiscovered(ctx context.Context, serverName string) (bool, error) {
	found, _, err := c.discovered(ctx)
	return slices.ContainsFunc(found, func(d discovered) bool { return d.serverName == serverName }), err
}
