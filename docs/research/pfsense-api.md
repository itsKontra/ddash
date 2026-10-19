# pfSense REST API: what it can do on our firewall

Research for [#2](https://github.com/itsKontra/ddash/issues/2). Gathered 2026-10-19.

The question: which pfSense and REST API package versions are installed, and which endpoints cover what ddash needs? That means DHCP leases, static mappings, alias membership, restarting the VPN Tunnel, gateway status, Remote Access peers and interface stats, along with any apply step each one needs and how the API key's permissions can be scoped.

## Sources

- **Live firewall (L).** Read-only `GET` requests with the key from `pfsense.env`. No write was sent. Device MACs, hostnames, IPs and keys are left out here; only the shape of each response is recorded.
- **Live OpenAPI schema (S).** `GET /api/v2/schema/openapi`, served by the installed package. It is OpenAPI 3.0.0 with 267 paths. Each operation's description states its *allowed privileges*, *applies immediately* and *required packages*.
- **Package source (C).** [pfrest/pfSense-pkg-RESTAPI at tag `v2.10.2`](https://github.com/pfrest/pfSense-pkg-RESTAPI/tree/v2.10.2/pfSense-pkg-RESTAPI/files/usr/local/pkg/RESTAPI), which matches the installed version. Paths below are relative to `RESTAPI/`.
- **Package docs (D).** [pfrest.org Common Control Parameters](https://pfrest.org/COMMON_CONTROL_PARAMETERS/) and [Authentication and Authorization](https://pfrest.org/AUTHENTICATION_AND_AUTHORIZATION/).

## Versions and setup

| Fact | Value | Source |
|---|---|---|
| pfSense | `2.9.0-RELEASE` (CE), patch 0 | L `GET /api/v2/system/version` |
| REST API package | `pfSense-pkg-RESTAPI` **v2.10.2**, the latest available; there is no update | L `GET /api/v2/system/restapi/version` |
| Other packages | `pfSense-pkg-WireGuard` 0.2.13_4, `pfBlockerNG` 3.2.17_1, `acme` 1.3.2 | L `GET /api/v2/system/packages` |
| API base | `https://<host>/api/v2/...`, with JSON envelope `{code,status,response_id,message,data}` | L |
| Auth header | `X-API-Key: <key>` | D (Auth), S `securitySchemes.KeyAuth` |
| API settings | `auth_methods: [KeyAuth]` only, `allowed_interfaces: [lan]`, `read_only: false`, `hateoas: false`, `expose_sensitive_fields: false`, `represent_interfaces_as: id` | L `GET /api/v2/system/restapi/settings` |
| DHCP backend | **Kea**. The service list shows `kea-dhcp4` ("Kea DHCP Server") running and no `dhcpd`. | L `GET /api/v2/status/services` |
| VPN Tunnel | **WireGuard** to ProtonVPN (tunnel `tun_wg2`, interface `opt3`, gateway `ProtonGW`). There is also an **OpenVPN client** "Proton UDP ... (fallback)" (`ovpnc1`, interface `opt2`, gateway `PROTON_OPENVPN_VPNV4`), and it is up and connected too. | L `vpn/wireguard/tunnels`, `vpn/openvpn/clients`, `routing/gateways` |
| Remote Access | WireGuard tunnel `tun_wg1` ("Remote Access", interface `opt1`), with 2 peers | L `vpn/wireguard/tunnels`, `vpn/wireguard/peers` |

Notes:

- `allowed_interfaces: [lan]` means the API answers only on the LAN interface. ddash's Docker-host LXC must reach the firewall's LAN address.
- The DHCP backend has **no GET endpoint**. `/api/v2/services/dhcp_server/backend` is PATCH-only (S). ddash should not need to know the backend: leases come from pfSense's backend-agnostic `system_get_dhcpleases()` (C `Models/DHCPServerLease.inc`). Only lease *deletion* branches on the ISC or Kea backend.
- ISC DHCP is marked as deprecated, to be removed in a future pfSense version (S, `DHCPServerBackend.dhcpbackend` help text). Kea is the right assumption from now on.

## Apply semantics (applies to every write below)

- Config-changing endpoints whose schema says **"Applies immediately: No"** only write `config.xml` and mark a subsystem dirty. Nothing takes effect until an apply step runs (S, C).
- There are two ways to apply:
  1. Send **`"apply": true`** in the body of the POST, PATCH or DELETE itself (D, Common Control Parameters; C `Core/Endpoint.inc`, which passes `apply: $this->request_data['apply'] === true` to the model's create, update or delete).
  2. Call that area's **apply endpoint** afterwards with `POST .../apply`. `GET .../apply` reports `{applied: bool}` (plus `pending_subsystems` for the firewall), which makes it a cheap check for pending changes (L, C `Models/*Apply.inc`).
- Apply runs **async by default** (`async: true`): the response comes back before the backend reload finishes. Sending `"async": false` makes the call wait, and the docs warn this can hang or time out (D).
- `"dry_run": true` validates a write without saving it (D). This is useful for tests.
- `append` and `remove` (PATCH only, array fields only): when true, the array values in the request are **added to** or **removed from** the existing array instead of replacing it. This applies to *every* array field in that request (D; C `Core/Endpoint.inc` `validate_append` / `validate_remove`).
- **Object IDs are array positions**, not stable keys. Examples are alias `id` 0..7, the static mapping `id` within its parent DHCP server, and the service `id` in the status list. ddash must look an object up by a stable field (name, MAC, vpnid) right before writing it, and not cache IDs (see [pfrest.org Working with Object IDs](https://pfrest.org/WORKING_WITH_OBJECT_IDS/)).

## Endpoints for each ddash need

The privilege names are the per-endpoint privileges taken from S. `page-all` ("WebCfg - All pages") also grants every one of them.

### DHCP leases (read)

- `GET /api/v2/status/dhcp_server/leases`, privilege `api-v2-status-dhcp-server-leases-get`. Supports `limit`, `offset`, `sort_by`, `sort_order` and `query` filters (S).
- Fields: `ip, mac, hostname, if, starts, ends, active_status, online_status, descr` (S `DHCPServerLease`).
- Live shape (L, 12 entries): 11 have `active_status: "static"` and `online_status: "active/online"`, with `if: "lan"` and **empty `starts`/`ends`**; these are static mappings merged into the list. 1 is a dynamic lease with `active_status: "active"`, `online_status: "idle/offline"`, timestamps formatted `YYYY/MM/DD HH:MM:SS`, and **`if: null`**. So with Kea, dynamic leases can lack the interface. `descr` is filled only for some static entries.
- No apply step (read-only). `DELETE` exists (clear a lease) but ddash does not need it.

### DHCP static mappings (read/create/update/delete)

- Read all: `GET /api/v2/services/dhcp_server/static_mappings` (`...-static-mappings-get`). Alternatively, `GET /api/v2/services/dhcp_servers` returns each interface's server with its `staticmap` array (L: the `lan` server has 11 mappings; `wan`, `opt1` and `opt3` are disabled).
- Single: `GET|POST|PATCH|DELETE /api/v2/services/dhcp_server/static_mapping`. It is a child model: `parent_id` is the DHCP server's interface id (`lan`), and `id` is the index within it (S "Parent model: DHCPServer").
- Fields: `mac, ipaddr, cid, hostname, domain, descr, arp_table_static_entry, dnsserver[], gateway, ...` (S `DHCPServerStaticMapping`).
- **Applies immediately: No.** Send `"apply": true`, or follow with `POST /api/v2/services/dhcp_server/apply` (`...-dhcp-server-apply-post`). The apply re-runs the DHCP server configuration (C `Models/DHCPServerStaticMapping.inc` `apply()`, which calls `DHCPServerApplyDispatcher`).

### Aliases: read and update membership

- Read: `GET /api/v2/firewall/aliases` (`api-v2-firewall-aliases-get`). Fields: `id, name, type (host|network|port), descr, address[], detail[]` (S, L: 8 aliases).
- Update: `PATCH /api/v2/firewall/alias` (`api-v2-firewall-alias-patch`) with `id` plus `address` (and optionally `detail`). Use `"append": true` to add a member or `"remove": true` to remove one, so there is no read-modify-write race on the whole list. `detail[]` must stay aligned with `address[]` by position (S `FirewallAlias.detail` help text). When an address is added without a detail, the model fills in `"Entry added <date>"` (C `Models/FirewallAlias.inc`).
- **Applies immediately: No.** Send `"apply": true`, or follow with `POST /api/v2/firewall/apply` (`api-v2-firewall-apply-post`), which reloads the filter (C `FirewallAlias::apply()` calls `FirewallApplyDispatcher`).
- `GET /api/v2/firewall/apply` returns `{applied, pending_subsystems[]}`. It was `applied: true` at the time of reading (L).
- For "is this device Tunnelled right now", `GET /api/v2/diagnostics/table` reads the live pf table (S). This was not needed to answer the question and was not probed.
- Which alias is the VPN Tunnel's alias cannot be told from the API alone. The candidates seen are `Proton` (host, 1 entry) and `USE_WAN` (host, 9 entries); the rules are not exposed by the endpoints we use. This is for the downstream ticket to decide or configure.

### Restart the VPN Tunnel client service

- List: `GET /api/v2/status/services` (`api-v2-status-services-get`) returns `id, name, description, enabled, status`. The live list has `wireguard`, `openvpn` ("OpenVPN client: Proton UDP ... (fallback)"), `kea-dhcp4`, `unbound`, `dpinger`, `ntpd`, `syslogd`, `pfb_dnsbl` and `pfb_filter` (L).
- Act: `POST /api/v2/status/service` (`api-v2-status-service-post`) with body `{"id": <index from the list>, "name": "<name>", "action": "start"|"stop"|"restart"}`. The model sets `auto_create_id = false`, so **`id` is required**. The model then calls pfSense's `service_control_<action>()`, and for `openvpn` it passes the instance's `vpnid`/mode as extras, so a single OpenVPN client restarts (C `Models/Service.inc`, `Core/Model.inc`). It returns the re-read status. There is no separate apply step.
- **WireGuard caveat:** the service list has **one `wireguard` entry for both tunnels** (L). Restarting it therefore restarts the whole WireGuard service, so the VPN Tunnel *and* Remote Access drop together. That is an inference from the single service entry; the per-tunnel behaviour was not tested because doing so is a write. A gentler per-tunnel bounce through the API would be `PATCH /api/v2/vpn/wireguard/tunnel` (`enabled` false, then true) followed by `POST /api/v2/vpn/wireguard/apply`, but that rewrites config. This matters to ddash if the Remote Access session is the one triggering the restart.
- The OpenVPN fallback client can be restarted on its own (`name: "openvpn"` plus its list `id`).

### Gateway status

- `GET /api/v2/status/gateways` (`api-v2-status-gateways-get`) returns `name, srcip, monitorip, delay (ms), stddev, loss (%), status ("online"...), substatus` (S `RoutingGatewayStatus`).
- Live: 3 gateways (`WANGW`, `ProtonGW` for the WireGuard VPN Tunnel, and `PROTON_OPENVPN_VPNV4` for the OpenVPN fallback), all `online` (L). The data comes from `dpinger`.
- Configuration (which interface each gateway belongs to, monitor settings): `GET /api/v2/routing/gateways`.

### WireGuard (Remote Access) peers

- Config: `GET /api/v2/vpn/wireguard/peers` (`api-v2-vpn-wireguard-peers-get`) returns `id, tun, enabled, endpoint, port, descr, publickey, presharedkey, allowedips[], persistentkeepalive`. Filter on `tun == "tun_wg1"` for Remote Access (L: 2 peers on `tun_wg1` and 1 on `tun_wg2`). Requires `pfSense-pkg-WireGuard` (S).
- Live status: `GET /api/v2/status/wireguard/tunnels` (`api-v2-status-wireguard-tunnels-get`) returns each tunnel's `name, status, listen_port, transfer_rx/tx, inpkts/outpkts, descr` plus a nested `peers[]` with `public_key, endpoint, allowed_ips, latest_handshake (unix), transfer_rx/tx` (S `WireGuardTunnelStatus`, L).
- **Quirk:** `GET /api/v2/status/wireguard/peers` returned only the 2 `tun_wg1` peers and **omitted the Proton peer on `tun_wg2`**, while `status/wireguard/tunnels` listed all 3 (L). Use the tunnels endpoint as the source of truth for handshakes. Join it to the config peers on public key.
- `latest_handshake: 0` means the peer has never connected or has no current handshake (L, both Remote Access peers at the time of reading).
- The REST API setting `hide_secrets: true` applies to the WireGuard package (L `vpn/wireguard/settings`). Because `expose_sensitive_fields: false`, private and preshared keys are not returned.
- Writes (if ever needed): `POST|PATCH|DELETE /api/v2/vpn/wireguard/peer` are **not applied immediately**. Use `"apply": true` or `POST /api/v2/vpn/wireguard/apply` (C `Models/WireGuardApply.inc`).

### Interface / traffic stats

- `GET /api/v2/status/interfaces` (`api-v2-status-interfaces-get`) returns `name (wan/lan/opt1...), descr, hwif, status, enable, ipaddr/subnet, gateway, media, mtu` and counters `inbytes, outbytes, inpkts, outpkts, inerrs, outerrs, collisions` plus `*pass` variants (S `InterfaceStats`).
- Live: 5 interfaces (`WAN`, `LAN`, `WIREGUARDREMOTEACCESS`, `PROTON_OPENVPN`, `PROTON_VPN`), all `up` (L).
- These are **cumulative counters**. ddash must poll and compute the deltas itself to get rates; the API has no rate or history endpoint.
- Per-tunnel WireGuard byte counts are also in `status/wireguard/tunnels`. OpenVPN client status is in `GET /api/v2/status/openvpn/clients`, which returns `name, vpnid, status, state ("CONNECTED"), connect_time, virtual_addr, remote_host...`.

## API key permission scoping

- An API key carries **the privileges of the user who generated it** (D Auth). The current key belongs to **`admin`** (L `GET /api/v2/auth/keys`, `username` field only), so it effectively has `page-all` (full access).
- Every endpoint and method has its own privilege, named `api-v2-<path-with-dashes>-<method>`, e.g. `api-v2-firewall-alias-patch` (S). Users with "WebCfg - All pages" get everything (D).
- "User - Config: Deny Config Write" lets a user call the API but denies config modification (D). This suits a read-only key, but ddash needs some writes.
- **Recommendation:** create a dedicated pfSense user (e.g. `ddash`) and give it only these privileges, then generate its key:
  - Read: `api-v2-status-dhcp-server-leases-get`, `api-v2-services-dhcp-server-static-mappings-get`, `api-v2-firewall-aliases-get`, `api-v2-status-services-get`, `api-v2-status-gateways-get`, `api-v2-vpn-wireguard-peers-get`, `api-v2-vpn-wireguard-tunnels-get`, `api-v2-status-wireguard-tunnels-get`, `api-v2-status-interfaces-get`, `api-v2-status-openvpn-clients-get`, `api-v2-system-version-get`
  - Write: `api-v2-firewall-alias-patch`, `api-v2-firewall-apply-post`, `api-v2-status-service-post`, plus `api-v2-services-dhcp-server-static-mapping-{post,patch,delete}` and `api-v2-services-dhcp-server-apply-post` if ddash edits static mappings
- **What privileges cannot narrow:** they are per endpoint and method, not per object. `api-v2-firewall-alias-patch` allows editing *any* alias, not just the VPN Tunnel's, and `api-v2-status-service-post` allows stopping *any* service, `unbound` and `kea-dhcp4` included. ddash must enforce its own allow-list: the one alias it manages and the one or two services it may restart.
- Network scoping already exists: the API is enabled on LAN only (`allowed_interfaces: [lan]`). There is also an access list at `/api/v2/system/restapi/access_list` that can restrict by source address (S); it was not read in detail.

## Also present, possibly useful later

- `POST /api/v2/graphql`, a GraphQL endpoint (S).
- `GET /api/v2/diagnostics/arp_table`, which could help with presence detection and Device identity (S).
- `GET /api/v2/status/system`, the firewall's own CPU, memory and uptime (S).
- `GET /api/v2/status/logs/{firewall,dhcp,openvpn,system}` (S).
- `POST /api/v2/services/wake_on_lan/send` (S).
