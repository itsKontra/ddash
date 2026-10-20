package pfsenseapi

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// The static mapping writes. pfSense's object ids are positions in an
// array, so every write runs alone in the queue and reads the static
// mappings again right before it is sent; no id is ever cached. Each write
// carries "apply": true, so it takes effect without a separate apply call
// (which restarts the DHCP server for a few seconds).

const staticMappingPath = "/api/v2/services/dhcp_server/static_mapping"

// write runs fn once every write queued before it has finished, then drops
// the cached reads it changed.
func (c *component) write(ctx context.Context, fn func() error) error {
	select {
	case c.queue <- struct{}{}:
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	}
	defer func() { <-c.queue }()
	defer c.servers.Invalidate()
	defer c.leases.Invalidate()
	defer c.aliases.Invalidate()
	return fn()
}

// lookup reads the DHCP server on iface as it is now, and the position of
// mac's static mapping on it (-1 when it has none).
func (c *component) lookup(ctx context.Context, iface, mac string) (*dhcpServer, int, error) {
	servers, err := c.readServers(ctx)
	if err != nil {
		return nil, -1, err
	}
	for i := range servers {
		s := &servers[i]
		if s.ID != iface {
			continue
		}
		for id, m := range s.Staticmap {
			if strings.EqualFold(str(m.MAC), mac) {
				return s, id, nil
			}
		}
		return s, -1, nil
	}
	return nil, -1, status.Errorf(codes.NotFound, "pfSense has no DHCP server on %q", iface)
}

func (c *component) CreateStaticMapping(ctx context.Context, req *pfsenseapipb.CreateStaticMappingRequest) (*pfsenseapipb.CreateStaticMappingResponse, error) {
	m := req.GetMapping()
	hw, err := net.ParseMAC(m.GetMac())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%q is no MAC address", m.GetMac())
	}
	mac := hw.String()
	if ip, err := netip.ParseAddr(m.GetIp()); err != nil || !ip.Is4() {
		return nil, status.Errorf(codes.InvalidArgument, "%q is no IPv4 address", m.GetIp())
	}
	var out *pfsenseapipb.StaticMapping
	err = c.write(ctx, func() error {
		server, id, err := c.lookup(ctx, m.GetInterface(), mac)
		if err != nil {
			return err
		}
		if id >= 0 {
			return status.Errorf(codes.AlreadyExists, "%s already has a static mapping on %s, at %s", mac, server.ID, str(server.Staticmap[id].IPAddr))
		}
		for _, other := range server.Staticmap {
			if str(other.IPAddr) == m.GetIp() {
				return status.Errorf(codes.AlreadyExists, "%s is already reserved for %s", m.GetIp(), strings.ToLower(str(other.MAC)))
			}
		}
		var created staticMapping
		body := map[string]any{
			"parent_id": server.ID,
			"mac":       mac,
			"ipaddr":    m.GetIp(),
			"hostname":  m.GetHostname(),
			"descr":     m.GetDescription(),
			"apply":     true,
		}
		if err := c.api.send(ctx, http.MethodPost, staticMappingPath, body, &created); err != nil {
			return err
		}
		out = created.proto(server.ID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &pfsenseapipb.CreateStaticMappingResponse{Mapping: out}, nil
}

func (c *component) RenameStaticMapping(ctx context.Context, req *pfsenseapipb.RenameStaticMappingRequest) (*pfsenseapipb.RenameStaticMappingResponse, error) {
	var out *pfsenseapipb.StaticMapping
	err := c.write(ctx, func() error {
		server, id, err := c.lookup(ctx, req.GetInterface(), req.GetMac())
		if err != nil {
			return err
		}
		if id < 0 {
			return status.Errorf(codes.NotFound, "%s has no static mapping on %s", strings.ToLower(req.GetMac()), server.ID)
		}
		var renamed staticMapping
		body := map[string]any{"parent_id": server.ID, "id": id, "descr": req.GetDescription(), "apply": true}
		if err := c.api.send(ctx, http.MethodPatch, staticMappingPath, body, &renamed); err != nil {
			return err
		}
		out = renamed.proto(server.ID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &pfsenseapipb.RenameStaticMappingResponse{Mapping: out}, nil
}
