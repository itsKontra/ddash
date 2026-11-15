package pfsenseapi

// The bodies of the writes to pfSense's REST API, one type per request, so
// a mistyped key doesn't compile. Every field is sent, zero or not, as
// pfSense reads an absent one as "leave it".

// applyBody is POST …/apply for the DNS Resolver and WireGuard: wait for
// the apply to end.
type applyBody struct {
	Async bool `json:"async"`
}

// firewallApplyBody is POST firewall/apply, which takes no fields.
type firewallApplyBody struct{}

// aliasPatch is PATCH firewall/alias: the alias's whole member list.
type aliasPatch struct {
	ID      int      `json:"id"`
	Address []string `json:"address"`
	Detail  []string `json:"detail"`
	// Apply reloads the filter with this write.
	Apply bool `json:"apply"`
}

// hostOverrideCreate is POST services/dns_resolver/host_override.
type hostOverrideCreate struct {
	Host   string   `json:"host"`
	Domain string   `json:"domain"`
	IP     []string `json:"ip"`
	Descr  string   `json:"descr"`
}

// hostOverrideReaddress is PATCH services/dns_resolver/host_override.
type hostOverrideReaddress struct {
	ID int      `json:"id"`
	IP []string `json:"ip"`
}

// hostOverrideAliasCreate is POST …/host_override/alias.
type hostOverrideAliasCreate struct {
	ParentID int    `json:"parent_id"`
	Host     string `json:"host"`
	Domain   string `json:"domain"`
	Descr    string `json:"descr"`
}

// staticMappingCreate is POST services/dhcp_server/static_mapping.
type staticMappingCreate struct {
	ParentID string `json:"parent_id"`
	MAC      string `json:"mac"`
	IPAddr   string `json:"ipaddr"`
	Hostname string `json:"hostname"`
	Descr    string `json:"descr"`
	Apply    bool   `json:"apply"`
}

// staticMappingRename is PATCH services/dhcp_server/static_mapping.
type staticMappingRename struct {
	ParentID string `json:"parent_id"`
	ID       int    `json:"id"`
	Descr    string `json:"descr"`
	Apply    bool   `json:"apply"`
}

// wireguardPeerCreate is POST vpn/wireguard/peer.
type wireguardPeerCreate struct {
	Enabled    bool                 `json:"enabled"`
	Tun        string               `json:"tun"`
	PublicKey  string               `json:"publickey"`
	Descr      string               `json:"descr"`
	AllowedIPs []wireguardAllowedIP `json:"allowedips"`
}

type wireguardAllowedIP struct {
	Address string `json:"address"`
	Mask    int    `json:"mask"`
	Descr   string `json:"descr"`
}
