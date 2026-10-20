package adguardapi

import (
	"context"
	"encoding/json"
	"net/netip"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
)

// Persistent clients. AdGuard's update replaces the whole client, so a
// client is kept as the raw JSON object AdGuard sent, and only the two
// fields the Action owns are changed before it goes back.

func (c *component) ListClients(ctx context.Context, _ *adguardapipb.ListClientsRequest) (*adguardapipb.ListClientsResponse, error) {
	return c.clients.Get(ctx)
}

func (c *component) fetchClients(ctx context.Context) (*adguardapipb.ListClientsResponse, error) {
	raw, err := c.readClients(ctx)
	if err != nil {
		return nil, err
	}
	out := &adguardapipb.ListClientsResponse{}
	for _, r := range raw {
		out.Clients = append(out.Clients, r.client())
	}
	return out, nil
}

// rawClient is a persistent client exactly as AdGuard sent it.
type rawClient map[string]json.RawMessage

func (r rawClient) str(key string) string {
	var s string
	_ = json.Unmarshal(r[key], &s)
	return s
}

func (r rawClient) flag(key string) bool {
	var b bool
	_ = json.Unmarshal(r[key], &b)
	return b
}

func (r rawClient) ids() []string {
	var ids []string
	_ = json.Unmarshal(r["ids"], &ids)
	return ids
}

func (r rawClient) client() *adguardapipb.Client {
	return &adguardapipb.Client{
		Name:              r.str("name"),
		Ids:               r.ids(),
		UseGlobalSettings: r.flag("use_global_settings"),
		FilteringEnabled:  r.flag("filtering_enabled"),
	}
}

// readClients reads the persistent clients as they are now, uncached.
func (c *component) readClients(ctx context.Context) ([]rawClient, error) {
	var data struct {
		Clients []rawClient `json:"clients"`
	}
	if err := c.api.get(ctx, "/control/clients", nil, &data); err != nil {
		return nil, err
	}
	return data.Clients, nil
}

// clientOf is the persistent client one of whose ids is ip itself; a
// client for a network holding ip is not it.
func clientOf(clients []rawClient, ip netip.Addr) rawClient {
	for _, r := range clients {
		for _, id := range r.ids() {
			if a, err := netip.ParseAddr(id); err == nil && a == ip {
				return r
			}
		}
	}
	return nil
}

func (c *component) SetClientFiltering(ctx context.Context, req *adguardapipb.SetClientFilteringRequest) (*adguardapipb.SetClientFilteringResponse, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(req.GetIp()))
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%q is no address", req.GetIp())
	}
	select {
	case c.queue <- struct{}{}:
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	defer func() { <-c.queue }()
	defer c.clients.Invalidate()

	clients, err := c.readClients(ctx)
	if err != nil {
		return nil, err
	}
	out := &adguardapipb.SetClientFilteringResponse{}
	found := clientOf(clients, ip)
	if found == nil {
		if req.GetEnabled() {
			// Without a client of its own the address follows the global
			// settings: ad-blocking is on, and nothing needs creating.
			return out, nil
		}
		// A client that changes nothing yet, so a failure before the
		// update below leaves the address as it was.
		add := map[string]any{
			"name":                        newName(clients, strings.TrimSpace(req.GetName()), ip),
			"ids":                         []string{ip.String()},
			"use_global_settings":         true,
			"use_global_blocked_services": true,
		}
		if err := c.api.post(ctx, "/control/clients/add", add); err != nil {
			return nil, err
		}
		out.Created = true
		if clients, err = c.readClients(ctx); err != nil {
			return nil, err
		}
		if found = clientOf(clients, ip); found == nil {
			return nil, status.Errorf(codes.Unavailable, "AdGuard Home doesn't list the client created for %s", ip)
		}
	}

	data := rawClient{}
	for k, v := range found {
		data[k] = v
	}
	data["use_global_settings"] = json.RawMessage("false")
	data["filtering_enabled"] = json.RawMessage("false")
	if req.GetEnabled() {
		data["filtering_enabled"] = json.RawMessage("true")
	}
	if err := c.api.post(ctx, "/control/clients/update", map[string]any{"name": found.str("name"), "data": data}); err != nil {
		return nil, err
	}
	out.Client = data.client()
	return out, nil
}

// newName is name, or the address when it is empty; with the address
// added when another client has that name already (names are unique).
func newName(clients []rawClient, name string, ip netip.Addr) string {
	if name == "" {
		return ip.String()
	}
	if slices.ContainsFunc(clients, func(r rawClient) bool { return r.str("name") == name }) {
		return name + " (" + ip.String() + ")"
	}
	return name
}
