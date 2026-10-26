# Nginx Proxy Manager's API: Proxy Hosts, certificates and health

Research for #135 (map #134). Facts only; no design decisions.

## Sources

Nginx Proxy Manager publishes no API reference. The REST API is defined by its
backend source in `NginxProxyManager/nginx-proxy-manager` (formerly
`jc21/nginx-proxy-manager`; GitHub redirects). Two versions were read:

- **v2.10.4** (`fe93cb3`, 2023-08-02): the newest release the Operator's
  install matches (see [Version](#version)). Paths below are `backend/...` at
  that tag unless marked.
- **v2.16.0** (`a29eae9`, 2026-09-24): the latest release. `develop` was at
  `1aca775` (2026-10-14).

A path like `internal/proxy-host.js:21-108` means `backend/internal/proxy-host.js`
at v2.16.0. Where v2.10.4 differs, it says so. The API is served under `/api` on
the admin port (81). Since v2.12.0 it also serves its own OpenAPI document at
`GET /api/schema` (`schema/swagger.json`). v2.10.4 serves JSON hyper-schema
there instead (`schema/index.json`).

## Version

**The Operator runs Nginx Proxy Manager v2.10.x (between v2.10.0 and v2.10.4),
installed natively in an LXC by the Proxmox VE community-scripts. It does not
run the Docker image.**

Evidence comes from `components/nginx-config/testdata/recorded/nginx_-T.stdout`.
`baseline/` has the same content with redacted addresses.

- The shipped nginx files (`/etc/nginx/nginx.conf`, `conf.d/default.conf`,
  `conf.d/include/*.conf`) were diffed against `docker/rootfs/etc/nginx/` for
  every tag from v2.9.19 to v2.16.0. Three markers fix the range:
  - `default.conf` has `ssl_reject_handshake on;`. It arrived in `a7f0c3b`,
    first released in **v2.9.20**.
  - `nginx.conf` has a `pid /run/nginx/nginx.pid` line (commented out). It is
    first present in **v2.10.0**.
  - `include/force-ssl.conf` is the short form. **v2.11.0** replaced it with the
    `$test`/acme-challenge form.
- Proxy Host files contain `listen 443 ssl http2;`. Templates moved to a
  separate `http2 on;` in `d7e0558` (first in v2.12.2), which agrees with the
  range. These files are only rewritten when a Proxy Host is saved, so this is
  weaker evidence than the shipped files.
- The remaining differences from upstream are `#daemon off;`, `#pid ...`,
  `user root;` instead of `user npm;`, and absolute `include /etc/nginx/conf.d/...`
  paths. The community-scripts installer makes exactly these edits
  (`community-scripts/ProxmoxVE` `install/nginxproxymanager-install.sh` at
  `fa4e227`, lines 86, 89 and 192). It builds OpenResty from source, runs
  nginx and the Node backend as the systemd units `openresty` and `npm`, and
  writes the release number into `backend/package.json` (line 84), so
  `GET /api` reports a real version. Upstream source says `0.0.0`.
- The recorded Proxy Host 1 (`home.example`, `nginx.home.example`) forwards to
  `198.18.1.210:81`, the admin UI and API itself. `conf.d/production.conf`
  proxies `/api/` to `127.0.0.1:3000`.

Confirm the exact patch release with `GET http://<nginx-ip>:81/api`. That needs
no auth and returns `{"status":"OK","version":{"major":2,"minor":10,"revision":N}}`.

ddash today: `components/nginx-config` runs `nginx -T` over SSH (`ssh.go:69`)
and parses server blocks: file, line, `server_name`s, listens, `ssl_certificate`,
location `proxy_pass` with Nginx Proxy Manager's `set $server/$port` expanded
(`parse.go:195-209`). The Nginx Proxy Manager id is only implicit, in the file
name `/data/nginx/proxy_host/<id>.conf`.

## Auth

- **Email/password exchange only. There are no API keys.**
  `POST /api/tokens {"identity": email, "secret": password}` returns
  `{"token": "<JWT>", "expires": "<ISO>"}` (`routes/tokens.js`,
  `internal/token.js:24-96`). The JWT is RS256-signed with a key pair kept in
  `/data/keys.json` (`lib/config.js`, same in v2.10.4), so tokens survive
  restarts. Send it as `Authorization: Bearer <token>`.
- **Expiry**: the default is `1d`. `POST /api/tokens` can't set it, because its
  schema allows only `identity`, `secret` and `scope`
  (`schema/paths/tokens/post.json`). **`GET /api/tokens?expiry=<n><unit>`**,
  called with a valid token, returns a fresh token with any expiry matching
  `^[0-9]+(y|Q|M|w|d|h|m|s|ms)$` (for example `10y`), and **any** user may do
  this. Only `scope` is limited to admins (`internal/token.js:105-145`,
  `lib/helpers.js:23-30`; v2.10.4 `internal/token.js` lines 88-120, same
  behaviour). A long-lived token is therefore possible, but only by minting it
  from a password login.
- **Revocation**: nothing is stored server-side. Every request reloads the user
  and rejects the token if the user is deleted or disabled, or no longer has
  the token's scopes (`lib/access.js:55-106`). From **v2.16.0 only**, a token
  issued before the user's last password change is also rejected with 401
  (`e4585ac`). In v2.10.x, changing the password does not invalidate existing
  tokens.
- **2FA (TOTP)** arrived in **v2.13.6** (`427afa5`). A user with 2FA gets
  `{"requires_2fa": true, "challenge_token": ...}` from `POST /api/tokens` and
  must call `POST /api/tokens/2fa`. A service user would need 2FA left off.
- **A dedicated non-admin user can do every Proxy Host and certificate write
  ddash needs.** Each user has a permissions row: `visibility` (`all`|`user`),
  plus `proxy_hosts`, `certificates` and others (`view`|`manage`). Non-admins
  pass `proxy_hosts:create/update/delete` and `certificates:update` (which
  covers renew) when they have `manage` (`lib/access/proxy_hosts-*.json`,
  `certificates-*.json`, `lib/access.js:237-295`). Two catches:
  - A new non-admin user starts with **`visibility: "user"`**: it sees and edits
    only the hosts and certificates it owns (`internal/user.js:55`; v2.10.4
    `internal/user.js:67`). An admin must set `visibility: "all"` with
    `PUT /api/users/{id}/permissions` before the user can see the Operator's
    existing Proxy Hosts and certificates.
  - A Proxy Host created through the API is owned by the token's user
    (`owner_user_id`, `internal/proxy-host.js:51`). That only matters for
    users with `visibility: "user"`.

## Proxy Hosts

Endpoints (`routes/nginx/proxy_hosts.js`; the same set exists in v2.10.4
`routes/api/nginx/proxy_hosts.js`):

| Call | Notes |
|---|---|
| `GET /api/nginx/proxy-hosts?expand=owner,certificate,access_list&query=` | List. `query` is a `LIKE` on `domain_names`. Sorted by `domain_names`. |
| `GET /api/nginx/proxy-hosts/{id}?expand=...` | One host, or 404. |
| `POST /api/nginx/proxy-hosts` | Create. Returns **201** and the host. |
| `PUT /api/nginx/proxy-hosts/{id}` | **Partial** update (no required fields). Returns 200 and the host. |
| `DELETE /api/nginx/proxy-hosts/{id}` | Soft delete (`is_deleted=1`). Removes the config and reloads. Returns `true`. |
| `POST /api/nginx/proxy-hosts/{id}/enable` · `/disable` | Toggle `enabled`. |
| `GET /api/nginx/proxy-hosts/{id}/logs` | **v2.16.0 only.** |

**Identifier**: an integer `id` (auto-increment). It is the `<id>` in
`/data/nginx/proxy_host/<id>.conf` and does not change when the host is edited.

**Fields** (`schema/components/proxy-host-object.json`; v2.10.4
`schema/endpoints/proxy-hosts.json`):

| Field | Type | Matches |
|---|---|---|
| `domain_names` | string[], 1–100, unique | hostname and aliases. **Sorted alphabetically on every save** (`models/proxy_host.js` `$beforeInsert`/`$beforeUpdate`, both versions), so the "first" domain is the alphabetically first one, not the first one entered. |
| `forward_scheme` | `"http"` \| `"https"` | upstream scheme |
| `forward_host` | string ≤255 | upstream host |
| `forward_port` | int 1–65535 | upstream port |
| `block_exploits` | bool | Block Common Exploits |
| `allow_websocket_upgrade` | bool | Websockets |
| `certificate_id` | int ≥0 (0 = none) or `"new"` | certificate. `"new"` makes Nginx Proxy Manager request a Let's Encrypt certificate inline. |
| `ssl_forced` | bool | Force SSL |
| `http2_support` | bool | HTTP/2 |
| `hsts_enabled`, `hsts_subdomains` | bool | HSTS |
| `advanced_config` | string | advanced config |
| `caching_enabled`, `access_list_id`, `locations[]`, `enabled`, `meta` | | other fields |
| `trust_forwarded_proto` | bool | **v2.14.0+ only** (`187d21a`) |

- **The server overrides some flags.** With no certificate it forces
  `ssl_forced` and `http2_support` to false. Without `ssl_forced` it forces
  `hsts_enabled` to false, and without HSTS, `hsts_subdomains`
  (`internal/host.js:17-34`, `cleanSslHstsData`). A write can return different
  flags from the ones sent.
- **Request bodies are strict.** POST and PUT schemas set
  `additionalProperties: false`, in both versions. Sending a GET response back
  as a PUT body fails because of `id`, `created_on` and the rest, and so does
  sending `trust_forwarded_proto` to v2.10.x. Either way the result is 400.
- **Booleans as 0/1 before v2.12.0.** In v2.10.4 the model has no bool
  conversion, so responses carry `0`/`1` for the flags. v2.12.0 (`4572b20`)
  returns `true`/`false`. Requests accept either, because AJV runs with
  `coerceTypes`.
- **`meta`** includes `nginx_online` (bool) and `nginx_err` (string|null), set
  on every config write (see below). With `expand=certificate`, the list
  clears `certificate.meta`.

### Write and reload semantics

`internal/proxy-host.js:21-108` (create), `116-223` (update), `275-311`
(delete); `internal/nginx.js:29-121`. The flow is the same in v2.10.4.

1. Permission check, then a **duplicate-domain check**: each requested name is
   compared, case-insensitively and exactly, against the `domain_names` of
   every non-deleted Proxy Host, Redirection Host and 404 Host. An update
   excludes its own id (`internal/host.js:105-197`). Streams are not checked,
   and a wildcard doesn't match concrete names.
2. **The database row is written first** (`insertAndFetch` / `patch`).
3. `internalNginx.configure`:
   - Runs `nginx -t`. **If the existing config already fails, configure
     rejects here.** The row stays changed, no file is written, and the API
     returns 500 (below).
   - Deletes the host's old `.conf` and renders the template into
     `/data/nginx/proxy_host/<id>.conf`.
   - Runs `nginx -t` again. If it passes, `meta.nginx_online = true`. If it
     fails, `meta.nginx_online = false`, `meta.nginx_err = <nginx stderr>`, and
     the file is moved to `<id>.conf.err` and removed from the live config. The
     row keeps the new values, and **the HTTP call still succeeds (201/200)**.
   - `nginx -s reload`, after one more `nginx -t`.
4. An audit-log entry is written.

So a write is **not atomic**. The database always takes the new values. nginx
either takes the new config or drops this host entirely, and the only sign of
the second outcome is `meta.nginx_online:false` in the response. A host that is
disabled is saved but not configured. Delete removes the file and reloads.

### Error shape

`app.js:62-90` (v2.10.4 `app.js:60-80`) and `lib/error.js`:

```json
{ "error": { "code": 400, "message": "grafana.home.example is already in use" } }
```

| Case | Status | `message` |
|---|---|---|
| Duplicate domain | 400 `ValidationError` | `"<hostname> is already in use"` |
| Body fails schema | 400 `ValidationError` | AJV text, e.g. `data must NOT have additional properties` |
| Expired token | 400 `AuthError` | `"Token has expired"` |
| Malformed or wrongly signed token | 500 (raw `jsonwebtoken` error, no status) | `"Internal Error"` |
| Revoked token (v2.16.0+) | 401 `TokenRevokedError` | |
| No token, or missing permission | 403 `PermissionError` | `"Permission Denied"` |
| Unknown id | 404 `ItemNotFoundError` | |
| nginx fails *before* the write (`nginx -t` / reload `CommandError`) | 500 | `"Internal Error"`. The stderr isn't public; `debug` is attached only in debug mode or on `/nginx/certificates` routes. |
| New config fails `nginx -t` | **201/200** | none; see `meta.nginx_err` |

## Certificates

`routes/nginx/certificates.js`, `internal/certificate.js`:

- **List**: `GET /api/nginx/certificates?expand=owner&query=`, sorted by
  `nice_name`, with `query` matching `nice_name`. Each item has `id`,
  `provider` (`"letsencrypt"` | `"other"`), `nice_name`, `domain_names`
  (wildcards like `*.home.example` allowed), `expires_on`
  (`"YYYY-MM-DD HH:mm:ss"` from the DB) and `meta`. From **v2.13.0**
  (`058f49c`), `expand` also accepts `proxy_hosts`, `redirection_hosts`,
  `dead_hosts` and `streams`, which gives the Proxy Hosts using each
  certificate. **In v2.10.4 only `owner` expands**
  (`internal/certificate.js:471`), so the reverse mapping has to come from each
  Proxy Host's `certificate_id`. The API can't answer "which certificate covers
  hostname X". The client matches `domain_names` itself.
- Every recorded Proxy Host uses `/etc/letsencrypt/live/npm-3/`, which is
  certificate id 3, probably a wildcard.
- **Renew**: `POST /api/nginx/certificates/{id}/renew`, with permission
  `certificates:update`. It is **synchronous**: the request waits for certbot
  (`req.setTimeout(900000)`, 15 minutes). The command is
  `certbot renew --force-renewal --cert-name npm-<id>`, after which
  `expires_on` is re-read from the PEM. It returns the updated certificate row
  (`internal/certificate.js:907-930`; v2.10.4 `938-974`). **Only `letsencrypt`
  certificates renew.** Others get 400 `"Only Let'sEncrypt certificates can be
  renewed"`. A failure answers with a `debug` block, because certificate routes
  always attach one.
- **Renew does not reload nginx**, in either version. The new files are picked
  up at the next reload: any host write, or the 6-hourly IP-ranges refresh
  (`internal/ip_ranges.js:22,113`) unless `IP_RANGES_FETCH_ENABLED=false`.
- **Automatic renewal**: Nginx Proxy Manager runs a timer every hour that
  renews each Let's Encrypt certificate expiring within 30 days
  (`internal/certificate.js:36-108`; v2.10.4 the same).

## Health

There is no "nginx up" or "config valid" endpoint in either version. The
closest stand-ins:

- `GET /api`, unauthenticated, returns `{"status":"OK","version":{...}}`, plus
  `setup` in newer releases (`routes/main.js`). The string is always `"OK"`;
  it means the Node backend answered. On this install the API is reached
  through nginx itself (`production.conf` on port 81 proxies `/api/` to
  `127.0.0.1:3000`), so a 200 from `:81/api` also shows that nginx is serving.
  It says nothing about config validity.
- Per host, `meta.nginx_online` and `meta.nginx_err` hold the result of the
  last `nginx -t` run for that host's write. They go stale: they are not
  re-evaluated when another host breaks the config.
- `GET /api/reports/hosts` returns counts per host type.
- v2.16.0 only: `GET /api/logs/sources` and `/api/logs/tail`, which read the
  backend, Let's Encrypt and per-host log files.

## API stability across versions

- No public contract: no versioned path, no changelog for the API, and the
  maintainer's docs never describe it. Changes seen between v2.10.4 and v2.16.0:
  - v2.12.0: the schema was rewritten from JSON hyper-schema to OpenAPI
    (`dfe2588`), and booleans changed from 0/1 to true/false.
  - v2.13.0: certificate `expand` gained `proxy_hosts` and the other host types.
  - v2.13.6: 2FA changes the shape of the `POST /tokens` response.
  - v2.14.0: `trust_forwarded_proto` added, which strict v2.10.x validation
    rejects.
  - v2.16.0: tokens are revoked on password change (401), and the log endpoints
    were added.
- The core shape is stable from v2.10.4 to v2.16.0: endpoint paths, field
  names, integer ids, the `{error:{code,message}}` envelope, and the
  write-then-configure flow.

## Not verified

No live calls were made. The behaviour above is read from source. The exact
v2.10.x patch release is unconfirmed (use `GET /api`). It is also unconfirmed
whether the community-scripts update path rewrites the shipped `/etc/nginx`
files: if it doesn't, the shipped-file fingerprint dates the install, not the
current version.
