# AdGuard Home HTTP API: status, stats, query log, per-client blocking

Research for [#3](https://github.com/itsKontra/ddash/issues/3). Researched 2026-10-19 against AdGuard Home
`master` at commit [`fb7774f`][commit] (latest release: **v0.107.79**, 2026-08-18).

## Answer in brief

- **Base path** `/control`, JSON over HTTP(S), on the same port as the web UI. The same API backs the official UI ([openapi.yaml][spec] `servers`, `info.description`).
- **Auth:** HTTP Basic (`Authorization: Basic base64(user:pass)`) on every request, or a session cookie `agh_session` from `POST /control/login`. There are no API keys or tokens. Basic auth is the simple choice for a backend client ([openapi/README.md][readme], [authhttp.go][authhttp]).
- **Status:** `GET /control/status` returns `running`, `protection_enabled`, `protection_disabled_duration`, `version`, `start_time`, and more.
- **Global protection toggle / pause:** `POST /control/protection` `{"enabled": false, "duration": <ms>}`. Leave out `duration` for an indefinite toggle.
- **Stats:** `GET /control/stats[?recent=<ms>]` returns totals, time series and top-N lists (domains, blocked domains, clients, upstreams).
- **Query log:** `GET /control/querylog` with `limit` (default 500), `older_than` (cursor) or `offset`, `search` (domain, client IP, ClientID or client name; substring match, or exact if wrapped in `"..."`), and `reason` (repeatable).
- **Turning ad-blocking off for one device:** that device must be a **persistent client**. Set `use_global_settings: false, filtering_enabled: false` on it with `POST /control/clients/update`. The update **replaces the whole client object**, so read, modify, then write.
- **Client identity:** a persistent client has `ids`, each an IP, a CIDR, a MAC, or a ClientID. **MAC only matches when AdGuard Home is the DHCP server.** Otherwise only IP or CIDR (plain DNS) or ClientID (DoH/DoT/DoQ) can match a query.

## Authentication

- The spec declares one global security scheme, `basicAuth` (HTTP basic) ([spec `securitySchemes`][spec]).
- The official README: if the web user has a password, "a web client must use authentication mechanism… Basic access authentication is the most simple method: a client must pass `Authorization` HTTP header along with all requests" ([openapi/README.md][readme]).
- Source: the auth middleware first looks for the `agh_session` cookie, then falls back to `r.BasicAuth()` ([authhttp.go `userFromRequest`, `userFromRequestBasicAuth`][authhttp]). Cookie sessions last 365 days (`cookieTTL`).
- `POST /control/login` `{"name","password"}` returns `400` for bad credentials and **`429` when out of login attempts**, ([spec `/login`][spec]). Basic auth failures go through the same per-remote-IP rate limiter, so a wrong password in ddash's config temporarily blocks every request from ddash's host ([authhttp.go `userFromRequestBasicAuth`][authhttp]). Treat a 401 as fatal configuration, not something to retry in a loop.
- There is only one user model (the admin user from `AdGuardHome.yaml`). There are no scoped or read-only API credentials. ddash holds full-admin credentials.
- Basic auth sends the password with every request, so use HTTPS to AdGuard Home, or keep the path on a trusted LAN segment.

## Status and protection

`GET /control/status`, schema `ServerStatus` ([spec][spec]):

| field | meaning |
|---|---|
| `running` | DNS server running |
| `protection_enabled` | global filtering on/off |
| `protection_disabled_duration` | remaining pause in ms (int64). Renamed from `protection_disabled_until` in the v0.107.79 spec fix ([API changelog][apichangelog]) |
| `version`, `start_time` (ms since epoch, v0.107.70+), `dns_addresses`, `dns_port`, `http_port`, `dhcp_available`, `language` | |

`POST /control/protection`, body `SetProtectionRequest` ([spec][spec]; added in v0.107.27 per the [API changelog][apichangelog]):

```json
{ "enabled": false, "duration": 600000 }
```

`duration` is the pause length in milliseconds and only makes sense with `enabled: false`. Without it the toggle is indefinite. `{"enabled": true}` resumes. (`/dns_config` also takes `protection_enabled`, but `/protection` is the dedicated endpoint.)

For an "is it up?" Check, `GET /control/status` (authenticated) tells you whether the web API answers and whether DNS is `running`. A DNS-type Check against port 53 tests resolution itself.

## Statistics

`GET /control/stats`, optional `recent=<ms>` (a multiple of one hour, no larger than the configured stats interval; `400` otherwise) ([spec `/stats`][spec]). Schema `Stats`:

- Totals: `num_dns_queries`, `num_blocked_filtering`, `num_replaced_safebrowsing`, `num_replaced_safesearch`, `num_replaced_parental`, `avg_processing_time` (seconds).
- Series: `dns_queries[]`, `blocked_filtering[]`, `replaced_safebrowsing[]`, `replaced_parental[]`, with buckets in `time_units` (`hours` or `days`).
- Top lists: `top_queried_domains`, `top_blocked_domains`, `top_clients`, `top_upstreams_responses`, `top_upstreams_avg_time`. Each entry is a single-key object `{ "<domain or client>": <number> }` (`TopArrayEntry`). This is awkward to type in Go: decode as `[]map[string]float64`.
- There is **no per-client stats endpoint**. Per-client numbers exist only as `top_clients`, or by counting query-log entries.
- Config: `GET /control/stats/config`, `PUT /control/stats/config/update`. The older `/stats_info` and `/stats_config` are deprecated.

## Query log

`GET /control/querylog` ([spec `/querylog`][spec]; parser in [querylog/http.go `parseSearchParams`][qlhttp]):

| param | behaviour |
|---|---|
| `limit` | page size. Default **500** ([searchparams.go][qlparams]) |
| `older_than` | RFC 3339 (nano) timestamp cursor. Pass the response's `oldest` to get the next page. When used alone, a scan reads at most 50 000 entries per request ([searchparams.go `maxFileScanEntries`][qlparams]) |
| `offset` | index-based paging. Using it removes the scan cap. The spec says to choose one of `offset` or `older_than` and stick to it |
| `search` | matches **domain, client IP, ClientID, or persistent/runtime client name**. Case-insensitive substring match. Wrapping the value in double quotes (`"192.168.1.20"`) makes it an exact match ([searchcriterion.go `ctTerm`, `ctDomainOrClientCase*`][qlsearch]) |
| `reason` | repeatable `FilteringReason` filter (`FilteredBlackList`, `NotFilteredWhiteList`, `FilteredBlockedService`, `Rewrite`, …). Cannot be combined with the deprecated `response_status` |

Response `QueryLog`: `{ "oldest": "<RFC3339>", "data": [QueryLogItem…] }`. Useful item fields: `time`, `question` (name/type/class), `client` (IP), `client_id`, `client_proto` (`dot|doh|doq|dnscrypt|""`), `client_info.name`, `reason`, `rules[]` (`text`, `filter_list_id`), `service_name`, `status` (`NOERROR`, …), `elapsedMs` (a string), `cached`, `upstream`, `answer[]`.

To filter "by client", use `search="<exact IP>"` (quoted) or the client's name. No other client parameter exists. Settings: `GET /control/querylog/config`, `PUT /control/querylog/config/update`. `anonymize_client_ip` hides IPs, and a per-client `ignore_querylog` setting keeps that client out of the log.

## Clients and per-client settings

### Two kinds of client

- **Runtime ("auto") clients** are discovered from hosts files, rDNS, ARP, WHOIS and AdGuard's own DHCP leases. They give a name only and **carry no settings** ([wiki Clients][wikiclients]). `GET /control/clients` lists them in `auto_clients[]` (`ip`, `name`, `source`, `whois_info`).
- **Persistent clients** are configured by the operator. They are in `clients[]` of `GET /control/clients` (schema `Client`). Only these can have their own filtering settings.

### Client fields relevant to ddash

`name` (also the key used for update and delete), `ids[]`, `use_global_settings`, `filtering_enabled`, `parental_enabled`, `safebrowsing_enabled`, `safe_search{}`, `use_global_blocked_services`, `blocked_services[]`, `blocked_services_schedule`, `upstreams[]`, `tags[]`, `ignore_querylog`, `ignore_statistics` ([spec `Client`][spec]).

### Turning ad-blocking off for one device

1. `GET /control/clients` and find the persistent client whose `ids` cover the device. Or `POST /control/clients/search` `{"clients":[{"id":"192.168.1.20"}]}`, which does an exact match on IP, CIDR, MAC or ClientID and returns `[{ "<id>": {…client, disallowed, disallowed_rule, whois_info} }]`. `GET /control/clients/find?ip0=` is deprecated since v0.107.56 ([API changelog][apichangelog]).
2. If none exists, `POST /control/clients/add` with a full `Client` object (`name`, `ids: ["<ip>"]`, …).
3. `POST /control/clients/update` with `{"name": "<current name>", "data": { …full Client…, "use_global_settings": false, "filtering_enabled": false }}`.

Caveats from the source ([clientshttp.go `handleUpdateClient`, `jsonToClient`][clientshttp]):

- The update **replaces the whole client**. The handler is marked "TODO: Accept updated parameters instead of whole structure." Every field you leave out is reset to its zero value (`false`, empty). The only exceptions are `ignore_querylog`, `ignore_statistics` and `upstreams_cache_*`, which keep their previous value when left out. ddash must send the full object it read back.
- `filtering_enabled`, `parental_enabled` and `safebrowsing_enabled` only take effect when `use_global_settings` is `false` (`UseOwnSettings = !UseGlobalSettings`). So "disable for one device" means both flags must be set.
- Blocked services follow the same pattern: `use_global_blocked_services`.
- Each successful call writes `AdGuardHome.yaml` (`confModifier.Apply`).

Alternatives that leave client settings untouched: a custom rule `@@||*^$client=<ip|name>` via `/control/filtering/set_rules` ([wiki Clients][wikiclients]). Or the access list (`/control/access/set`), which *refuses* service to a client entirely, which is not the same as unblocking it. Filter-rule editing is out of scope for ddash (issue #1), so the persistent-client route is the right one.

### How a query is matched to a persistent client

Lookup order in [client/index.go `find`][index]: **ClientID → IP (exact, then any CIDR containing it) → MAC**.

- **IP / CIDR:** works for plain DNS. It breaks if the device's IP changes, so pair it with a DHCP reservation on pfSense.
- **MAC:** "only possible when AdGuard Home works as the network's DHCP server" ([wiki Clients][wikiclients]). On this network DHCP comes from pfSense (the lease source in #1's device-identity note), so a MAC id will **never match** unless AdGuard's DHCP is turned on. ddash must give AdGuard the device's IP (taken from the pfSense lease) and not its MAC.
- **ClientID:** only for encrypted DNS (DoH path `/dns-query/<id>` or `<id>.example.org`; DoT/DoQ need a wildcard certificate) ([wiki Clients][wikiclients]). Not usable for ordinary LAN devices that use plain DNS from DHCP.
- `ids` must be unique across persistent clients. Add or update fails if another client already uses the same IP, CIDR, MAC, ClientID or name ([index.go `clashes`][index]).

This matters for the open "Device identity across systems" question in #1. The key that joins AdGuard, pfSense leases and WireGuard peers is the **IP address**. MAC is only a join key on the pfSense side.

## Endpoints worth knowing but out of scope

`/control/filtering/*` (lists and rules), `/control/blocked_services/*`, `/control/rewrite/*`, `/control/dhcp/*`, `/control/access/*`, `/control/tls/*`. Filter-list management stays in the native UI (#1).

## Sources

- [spec]: AdGuard Home `openapi/openapi.yaml` @ `fb7774f`, the first-party API spec ("Our admin web interface is built on top of this REST-ish API").
- [readme]: `openapi/README.md`, Authentication section.
- [apichangelog]: `openapi/CHANGELOG.md`, per-version API changes.
- [authhttp]: `internal/home/authhttp.go`, cookie name and TTL, Basic auth fallback.
- [qlhttp] / [qlparams] / [qlsearch]: `internal/querylog/http.go`, `searchparams.go`, `searchcriterion.go`.
- [clientshttp]: `internal/home/clientshttp.go`, client add/update/search handlers.
- [index]: `internal/client/index.go`, persistent-client lookup and clash rules.
- [wikiclients]: official wiki "Clients" (marked outdated in favour of the AdGuard DNS KB, but its identification rules match the source above).

[commit]: https://github.com/AdguardTeam/AdGuardHome/commit/fb7774fa5ca1da62e0bc5b09e5e5f408ed1c8514
[spec]: https://github.com/AdguardTeam/AdGuardHome/blob/fb7774fa5ca1da62e0bc5b09e5e5f408ed1c8514/openapi/openapi.yaml
[readme]: https://github.com/AdguardTeam/AdGuardHome/blob/fb7774fa5ca1da62e0bc5b09e5e5f408ed1c8514/openapi/README.md
[apichangelog]: https://github.com/AdguardTeam/AdGuardHome/blob/fb7774fa5ca1da62e0bc5b09e5e5f408ed1c8514/openapi/CHANGELOG.md
[authhttp]: https://github.com/AdguardTeam/AdGuardHome/blob/fb7774fa5ca1da62e0bc5b09e5e5f408ed1c8514/internal/home/authhttp.go
[qlhttp]: https://github.com/AdguardTeam/AdGuardHome/blob/fb7774fa5ca1da62e0bc5b09e5e5f408ed1c8514/internal/querylog/http.go
[qlparams]: https://github.com/AdguardTeam/AdGuardHome/blob/fb7774fa5ca1da62e0bc5b09e5e5f408ed1c8514/internal/querylog/searchparams.go
[qlsearch]: https://github.com/AdguardTeam/AdGuardHome/blob/fb7774fa5ca1da62e0bc5b09e5e5f408ed1c8514/internal/querylog/searchcriterion.go
[clientshttp]: https://github.com/AdguardTeam/AdGuardHome/blob/fb7774fa5ca1da62e0bc5b09e5e5f408ed1c8514/internal/home/clientshttp.go
[index]: https://github.com/AdguardTeam/AdGuardHome/blob/fb7774fa5ca1da62e0bc5b09e5e5f408ed1c8514/internal/client/index.go
[wikiclients]: https://github.com/AdguardTeam/AdGuardHome/wiki/Clients
