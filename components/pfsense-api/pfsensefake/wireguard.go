package pfsensefake

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"time"
)

const (
	wireguardPeersPath   = "/api/v2/vpn/wireguard/peers"
	wireguardPeerPath    = "/api/v2/vpn/wireguard/peer"
	wireguardApplyPath   = "/api/v2/vpn/wireguard/apply"
	wireguardTunnelsPath = "/api/v2/status/wireguard/tunnels"
)

// AppliedWireGuard is the entry Log records when pending WireGuard changes
// take effect (the tunnels reload), from a write's "apply": true or from
// POST vpn/wireguard/apply. The tunnels' live status then lists every
// enabled peer configured on them.
const AppliedWireGuard = "apply WireGuard"

// WireguardPeer is a configured WireGuard peer as the fake holds it.
type WireguardPeer struct {
	Tunnel, PublicKey, Description string
	Enabled                        bool
	// "198.18.40.2/32", ...
	AllowedIPs []string
}

// WireguardPeers returns every configured peer in order.
func (f *Firewall) WireguardPeers() []WireguardPeer {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]WireguardPeer, 0, len(f.peers))
	for _, p := range f.peers {
		out = append(out, WireguardPeer{
			Tunnel:      text(p["tun"]),
			PublicKey:   text(p["publickey"]),
			Description: text(p["descr"]),
			Enabled:     p["enabled"] == true,
			AllowedIPs:  allowedIPs(p),
		})
	}
	return out
}

// WireGuardPending reports whether WireGuard changes wait for an apply.
func (f *Firewall) WireGuardPending() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wireguardPending
}

// FailWireGuardApply makes the next WireGuard apply fail with a 500 and
// leave the changes pending.
func (f *Firewall) FailWireGuardApply() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failWireGuardApply = true
}

// serveWireguard answers the WireGuard peer, apply and live status
// endpoints, reporting whether r was one.
func (f *Firewall) serveWireguard(w http.ResponseWriter, r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "GET " + wireguardPeersPath:
		f.mu.Lock()
		list := make([]map[string]any, len(f.peers))
		for i, p := range f.peers {
			list[i] = maps.Clone(p)
			list[i]["id"] = i
		}
		b, err := json.Marshal(list)
		f.mu.Unlock()
		if err != nil {
			reply(w, http.StatusInternalServerError, "SERVER_ERROR", err.Error(), nil)
			return true
		}
		reply(w, http.StatusOK, "SUCCESS", "", json.RawMessage(b))
	case "GET " + wireguardTunnelsPath:
		f.mu.Lock()
		b, err := json.Marshal(f.tunnels)
		f.mu.Unlock()
		if err != nil {
			reply(w, http.StatusInternalServerError, "SERVER_ERROR", err.Error(), nil)
			return true
		}
		reply(w, http.StatusOK, "SUCCESS", "", json.RawMessage(b))
	case "POST " + wireguardPeerPath:
		f.createPeer(w, r)
	case "GET " + wireguardApplyPath:
		f.mu.Lock()
		applied := !f.wireguardPending
		f.mu.Unlock()
		reply(w, http.StatusOK, "SUCCESS", "", map[string]any{"applied": applied})
	case "POST " + wireguardApplyPath:
		time.Sleep(f.opts.ApplyDelay)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failWireGuardApply {
			f.failWireGuardApply = false
			reply(w, http.StatusInternalServerError, "PFSENSEFAKE_INJECTED", "pfsensefake failed this apply as told.", nil)
			return true
		}
		f.applyWireguard()
		reply(w, http.StatusOK, "SUCCESS", "", map[string]any{"applied": true})
	default:
		return false
	}
	return true
}

// createPeer adds a peer, validating it as pfSense does: a public key no
// other peer has, an existing tunnel (or "unassigned"), and allowed IPs
// with an address and a mask, each address and mask once.
func (f *Firewall) createPeer(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		reply(w, http.StatusBadRequest, "INVALID_JSON", "The request body is not valid JSON.", nil)
		return
	}
	f.begin()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writing--

	key := text(body["publickey"])
	if key == "" {
		reply(w, http.StatusBadRequest, "FIELD_IS_REQUIRED", "Field `publickey` is required.", nil)
		return
	}
	for _, p := range f.peers {
		if text(p["publickey"]) == key {
			reply(w, http.StatusBadRequest, "FIELD_MUST_BE_UNIQUE", fmt.Sprintf("Field `publickey` must be unique. Value `%s` is already in use.", key), nil)
			return
		}
	}
	tun := text(body["tun"])
	if tun == "" {
		tun = "unassigned"
	}
	if tun != "unassigned" && f.tunnel(tun) == nil {
		reply(w, http.StatusBadRequest, "FOREIGN_MODEL_FIELD_VALUE_NOT_FOUND", fmt.Sprintf("Field `tun` could not locate `WireGuardTunnel` object with `name` set to `%s`.", tun), nil)
		return
	}
	list, _ := body["allowedips"].([]any)
	ips := make([]any, 0, len(list))
	seen := map[string]bool{}
	for i, x := range list {
		ip, _ := x.(map[string]any)
		addr, err := netip.ParseAddr(text(ip["address"]))
		mask, ok := ip["mask"].(float64)
		if err != nil || !ok || mask < 0 || int(mask) > addr.BitLen() || mask != float64(int(mask)) {
			reply(w, http.StatusBadRequest, "INVALID_ALLOWED_IP", fmt.Sprintf("Field `allowedips` item %d needs a valid `address` and `mask`.", i), nil)
			return
		}
		cidr := addr.String() + "/" + strconv.Itoa(int(mask))
		if seen[cidr] {
			reply(w, http.StatusBadRequest, "FIELDS_MUST_BE_UNIQUE_TOGETHER", "Fields `address` and `mask` must be unique together.", nil)
			return
		}
		seen[cidr] = true
		ips = append(ips, map[string]any{"parent_id": len(f.peers), "id": i, "address": addr.String(), "mask": int(mask), "descr": text(ip["descr"])})
	}
	peer := map[string]any{
		"enabled":             body["enabled"] == true,
		"tun":                 tun,
		"endpoint":            body["endpoint"],
		"port":                "51820",
		"descr":               text(body["descr"]),
		"persistentkeepalive": body["persistentkeepalive"],
		"publickey":           key,
		"allowedips":          ips,
	}
	f.peers = append(f.peers, peer)
	f.wireguardPending = true
	if body["apply"] == true {
		f.applyWireguard()
	}
	out := maps.Clone(peer)
	out["id"] = len(f.peers) - 1
	reply(w, http.StatusOK, "SUCCESS", "", out)
}

// applyWireguard makes pending WireGuard changes take effect: each enabled
// peer on a tunnel joins its live status, never connected; f.mu is held.
func (f *Firewall) applyWireguard() {
	if !f.wireguardPending {
		return
	}
	f.wireguardPending = false
	f.log = append(f.log, AppliedWireGuard)
	for _, p := range f.peers {
		t := f.tunnel(text(p["tun"]))
		if t == nil || p["enabled"] != true {
			continue
		}
		live, _ := t["peers"].([]any)
		if slices.ContainsFunc(live, func(x any) bool {
			m, _ := x.(map[string]any)
			return text(m["public_key"]) == text(p["publickey"])
		}) {
			continue
		}
		t["peers"] = append(live, map[string]any{
			"parent_id": t["id"], "id": len(live), "tunnel_device": t["name"],
			"public_key": p["publickey"], "endpoint": "(none)", "allowed_ips": allowedIPs(p),
			"latest_handshake": 0, "transfer_rx": 0, "transfer_tx": 0, "persistent_keepalive": "off", "descr": p["descr"],
		})
	}
}

// tunnel is the live status of the tunnel named name; f.mu is held.
func (f *Firewall) tunnel(name string) map[string]any {
	for _, t := range f.tunnels {
		if text(t["name"]) == name {
			return t
		}
	}
	return nil
}

// allowedIPs is a configured peer's allowed IPs as CIDRs.
func allowedIPs(p map[string]any) []string {
	list, _ := p["allowedips"].([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		ip, _ := x.(map[string]any)
		out = append(out, text(ip["address"])+"/"+text(ip["mask"]))
	}
	return out
}
