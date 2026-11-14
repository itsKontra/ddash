package pfsenseapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// The WireGuard peers as configured, and adding one. A create runs in the
// write queue and reads the peers right before it is sent, so two creates
// can't take one address. It doesn't apply itself: the apply that follows
// reloads the tunnel, and runs even when ctx is done, so what was written
// goes live.

const (
	wireguardPeersPath = "/api/v2/vpn/wireguard/peers"
	wireguardPeerPath  = "/api/v2/vpn/wireguard/peer"
	wireguardApplyPath = "/api/v2/vpn/wireguard/apply"
)

// wireguardPeer is one entry of GET vpn/wireguard/peers, and what POST
// vpn/wireguard/peer answers with.
type wireguardPeer struct {
	Enabled             flag    `json:"enabled"`
	Tun                 string  `json:"tun"`
	Endpoint            *string `json:"endpoint"`
	Port                number  `json:"port"`
	Descr               *string `json:"descr"`
	PersistentKeepalive number  `json:"persistentkeepalive"`
	PublicKey           string  `json:"publickey"`
	AllowedIPs          []struct {
		Address string `json:"address"`
		Mask    number `json:"mask"`
	} `json:"allowedips"`
}

func (p *wireguardPeer) proto() *pfsenseapipb.WireguardPeerConfig {
	out := &pfsenseapipb.WireguardPeerConfig{
		Tunnel:              p.Tun,
		Enabled:             bool(p.Enabled),
		PublicKey:           p.PublicKey,
		Description:         str(p.Descr),
		PersistentKeepalive: int32(p.PersistentKeepalive),
	}
	if e := str(p.Endpoint); e != "" {
		out.Endpoint = net.JoinHostPort(e, strconv.FormatUint(uint64(p.Port), 10))
	}
	for _, a := range p.AllowedIPs {
		out.AllowedIps = append(out.AllowedIps, a.Address+"/"+strconv.FormatUint(uint64(a.Mask), 10))
	}
	return out
}

// flag decodes a JSON boolean, or pfSense's "yes" and "no" (null is false).
type flag bool

func (f *flag) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case bool:
		*f = flag(v)
	case string:
		*f = flag(v == "yes" || v == "true")
	default:
		*f = false
	}
	return nil
}

func (c *component) ListWireguardPeers(ctx context.Context, req *pfsenseapipb.ListWireguardPeersRequest) (*pfsenseapipb.ListWireguardPeersResponse, error) {
	if req.GetFresh() {
		c.peers.Invalidate()
	}
	return c.peers.Get(ctx)
}

// readPeers reads the configured peers uncached; a create's check uses it.
func (c *component) readPeers(ctx context.Context) ([]wireguardPeer, error) {
	var data []wireguardPeer
	if err := c.api.get(ctx, wireguardPeersPath, &data); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *component) fetchPeers(ctx context.Context) (*pfsenseapipb.ListWireguardPeersResponse, error) {
	data, err := c.readPeers(ctx)
	if err != nil {
		return nil, err
	}
	out := &pfsenseapipb.ListWireguardPeersResponse{}
	for i := range data {
		out.Peers = append(out.Peers, data[i].proto())
	}
	return out, nil
}

func (c *component) CreateWireguardPeer(ctx context.Context, req *pfsenseapipb.CreateWireguardPeerRequest) (*pfsenseapipb.CreateWireguardPeerResponse, error) {
	allowed, err := checkPeer(req)
	if err != nil {
		return nil, err
	}
	out := &pfsenseapipb.CreateWireguardPeerResponse{}
	err = c.write(ctx, func() error {
		all, err := c.readPeers(ctx)
		if err != nil {
			return err
		}
		if err := peerFree(all, req.GetTunnel(), req.GetPublicKey(), allowed); err != nil {
			return err
		}
		ips := make([]map[string]any, 0, len(allowed))
		for _, p := range allowed {
			ips = append(ips, map[string]any{"address": p.Addr().String(), "mask": p.Bits(), "descr": ""})
		}
		body := map[string]any{
			"enabled":    true,
			"tun":        req.GetTunnel(),
			"publickey":  req.GetPublicKey(),
			"descr":      req.GetDescription(),
			"allowedips": ips,
		}
		var created wireguardPeer
		if err := c.api.send(ctx, http.MethodPost, wireguardPeerPath, body, &created); err != nil {
			return err
		}
		out.Peer = created.proto()
		if err := c.applyWireguard(ctx); err != nil {
			out.ApplyFailed = true
			return status.Errorf(status.Code(err), "pfSense holds the peer, but applying it failed: %s", status.Convert(err).Message())
		}
		return nil
	})
	if err != nil {
		if out.GetApplyFailed() {
			if st, derr := status.Convert(err).WithDetails(out); derr == nil {
				return nil, st.Err()
			}
		}
		return nil, err
	}
	return out, nil
}

// applyWireguard applies the pending WireGuard changes, which reloads the
// tunnels they touch, up to applyTimeout, even when ctx is done.
func (c *component) applyWireguard(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), applyTimeout)
	defer cancel()
	var state struct {
		Applied bool `json:"applied"`
	}
	return c.api.sendWithin(ctx, applyTimeout, http.MethodPost, wireguardApplyPath, map[string]any{"async": false}, &state)
}

// checkPeer refuses a request naming no tunnel, a public key that isn't
// one, or no allowed IP, and returns the allowed IPs.
func checkPeer(req *pfsenseapipb.CreateWireguardPeerRequest) ([]netip.Prefix, error) {
	if req.GetTunnel() == "" {
		return nil, status.Error(codes.InvalidArgument, "name the peer's tunnel")
	}
	if k, err := base64.StdEncoding.DecodeString(req.GetPublicKey()); err != nil || len(k) != 32 {
		return nil, status.Errorf(codes.InvalidArgument, "%q is no WireGuard public key", req.GetPublicKey())
	}
	if len(req.GetAllowedIps()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "give the peer at least one allowed IP")
	}
	var out []netip.Prefix
	for _, a := range req.GetAllowedIps() {
		p, err := netip.ParsePrefix(strings.TrimSpace(a))
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%q is no address with a prefix length, like 198.18.40.4/32", a)
		}
		out = append(out, p)
	}
	return out, nil
}

// peerFree refuses a public key any peer has, and an allowed IP
// overlapping one of another peer on the same tunnel, enabled or not.
func peerFree(all []wireguardPeer, tunnel, key string, allowed []netip.Prefix) error {
	for i := range all {
		p := all[i].proto()
		name := p.GetDescription()
		if name == "" {
			name = p.GetPublicKey()
		}
		if p.GetPublicKey() == key {
			return status.Errorf(codes.AlreadyExists, "the peer %s already has that public key", name)
		}
		if p.GetTunnel() != tunnel {
			continue
		}
		for _, held := range p.GetAllowedIps() {
			h, err := netip.ParsePrefix(held)
			if err != nil {
				continue
			}
			for _, a := range allowed {
				if a.Overlaps(h) {
					return status.Errorf(codes.AlreadyExists, "%s overlaps %s, which the peer %s on %s holds", a, h, name, tunnel)
				}
			}
		}
	}
	return nil
}
