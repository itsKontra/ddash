# Check engine: probes in Go, history, and ntfy

Research for [#8](https://github.com/itsKontra/ddash/issues/8). Researched 2026-10-19 against primary sources. Source links to GitHub are pinned to the commit that was read.

Framing from the map ([#1](https://github.com/itsKontra/ddash/issues/1)): less configuration is better, and the main question is "is it up?". A **Check** yields up or down plus a latency.

## Answer in brief

- **HTTP(S), TCP, DNS: stdlib is enough.** `net/http` for HTTP(S), `net.Dialer` for TCP, `net.Resolver` (pure-Go, custom `Dial` to the target server) for DNS. No third-party library is needed for "is it up?".
- **ICMP: use `github.com/prometheus-community/pro-bing`** (v0.9.1) in its default *unprivileged* mode. Under docker compose on a bridge network this needs **no capability and no sysctl**: Docker sets `net.ipv4.ping_group_range = "0 2147483647"` in every container that gets its own network namespace. `CAP_NET_RAW` is only the fallback (host networking, user-namespace remap).
- **History:** Uptime Kuma keeps a raw `heartbeat` table (one row per probe) plus per-Check aggregate buckets (`stat_minutely`, `stat_hourly`, `stat_daily` holding `up`/`down` counts and ping avg/min/max). Uptime % is `up / (up + down)` summed over buckets; maintenance beats are excluded and pending beats count as down. Retention: minutely 24 h, hourly 30 d, daily and raw "important" beats 365 d (one setting).
- **ntfy:** `POST https://<server>/<topic>` with the message as body; headers `Title`, `Priority` (1–5 or `min|low|default|high|max/urgent`), `Tags` (comma list, emoji shortcodes), `Click`; auth `Authorization: Bearer tk_...` (or Basic). Or `POST` JSON to the server root with `topic`, `message`, `title`, `priority` (int), `tags` (array), `click`.

## 1. Probes in Go

### HTTP(S): `net/http`

- `http.Client.Timeout` is "a time limit for requests made by this Client"; it covers connecting, redirects and reading the body. "A Timeout of zero means no timeout", so always set it. ([pkg.go.dev/net/http#Client](https://pkg.go.dev/net/http#Client))
- With `CheckRedirect` nil the client "stop[s] after 10 consecutive requests" (follows up to 10 redirects). (same)
- The caller must close `resp.Body`; if it is not read to EOF and closed the Transport may not reuse the connection. ([pkg.go.dev/net/http#Client.Do](https://pkg.go.dev/net/http#Client.Do))
- Build requests with `http.NewRequestWithContext(ctx, method, url, body)` so a per-Check deadline cancels them. (same page)
- Latency = wall time around `client.Do`. "Up" default: status 2xx/3xx after redirects (Uptime Kuma's default accepted range is `200-299`; ddash can choose). TLS errors fail the request naturally; `resp.TLS.PeerCertificates[0].NotAfter` is available if certificate expiry is wanted later.

### TCP: `net.Dialer`

- `net.DialTimeout(network, address string, timeout time.Duration) (Conn, error)`, or `(&net.Dialer{Timeout: t}).DialContext(ctx, "tcp", "host:port")`. `Dialer.Timeout` "is the maximum amount of time a dial will wait for a connect to complete." ([pkg.go.dev/net#Dialer](https://pkg.go.dev/net#Dialer))
- Up = connect succeeded; close immediately. Latency = connect time.

### DNS: `net.Resolver` (stdlib) first, `miekg/dns` only if needed

- `net.Resolver{PreferGo: true, Dial: ...}`: `PreferGo` selects Go's built-in resolver for this resolver only; `Dial` "optionally specifies an alternate dialer for use by Go's built-in DNS resolver to make TCP and UDP connections to DNS services". ([pkg.go.dev/net#Resolver](https://pkg.go.dev/net#Resolver)) Pointing `Dial` at the target server (e.g. AdGuard Home on `udp://10.0.0.2:53`) makes a Check of *that* DNS server.
- Lookups available: `LookupHost`, `LookupIP`, `LookupNetIP`, `LookupCNAME`, `LookupMX`, `LookupNS`, `LookupSRV`, `LookupTXT`, `LookupAddr`, `LookupPort`. Enough for "does the server answer for `example.com`?". (same)
- On Unix "the pure Go resolver is preferred over the cgo resolver, because a blocked DNS request consumes only a goroutine". ([pkg.go.dev/net — Name Resolution](https://pkg.go.dev/net#hdr-Name_Resolution))
- Limits: the stdlib gives no raw RCODE, no arbitrary record types, and no per-query transport choice. If ddash later needs those, `github.com/miekg/dns` (v1) is in fix-only mode: its README says "DNS version 2 is now available at https://codeberg.org/miekg/dns", "the version here will only see specific fixes and nothing else" and "at some point this repo will be archived". ([github.com/miekg/dns](https://github.com/miekg/dns)) v2 lives at `codeberg.org/miekg/dns` and is pre-1.0: "will not soon see a v1.0.0 release because I want to be able to still make changes". ([codeberg.org/miekg/dns](https://codeberg.org/miekg/dns)) Another reason to stay on the stdlib for v1.

### ICMP: `pro-bing` over `golang.org/x/net/icmp`

**The two socket kinds** ([pkg.go.dev/golang.org/x/net/icmp#ListenPacket](https://pkg.go.dev/golang.org/x/net/icmp#ListenPacket)):

- Non-privileged: network `"udp4"`/`"udp6"`, a datagram ICMP endpoint that "allows to read, write a few limited ICMP messages such as echo request and echo reply". "Currently only Darwin and Linux support this."
- Privileged: network `"ip4:icmp"` / `"ip6:ipv6-icmp"`, a raw socket (needs `CAP_NET_RAW` or root).
- The package's `NonPrivilegedPing` example warns you "may need to adjust the net.ipv4.ping_group_range kernel state".

**Linux gate: `net.ipv4.ping_group_range`.** Kernel docs: "Restrict ICMP_PROTO datagram sockets to users in the group range. The default is "1 0", meaning, that nobody (not even root) may create ping sockets." ([docs.kernel.org ip-sysctl](https://docs.kernel.org/networking/ip-sysctl.html)) It is per network namespace.

**Docker already opens that gate.** In moby's `daemon/oci_linux.go`, for any container whose network mode is private (its own netns, i.e. the compose default bridge network):

```go
// We cannot set up ping socket support in a user namespace
userNS := daemonCfg.RemappedRoot != "" && c.HostConfig.UsernsMode.IsPrivate()
if !userNS && !userns.RunningInUserNS() && sysctlExists("net.ipv4.ping_group_range") {
    // allow unprivileged ICMP echo sockets without CAP_NET_RAW
    s.Linux.Sysctl["net.ipv4.ping_group_range"] = "0 2147483647"
}
```

([moby/moby daemon/oci_linux.go @ 3f38b15](https://github.com/moby/moby/blob/3f38b15147b14ce0ce7b11558a1aa9e9f14db103/daemon/oci_linux.go#L754-L776)). The surrounding comment: "We do not set network sysctls if network namespace is host, or if we are joining an existing namespace, only if we create a new net namespace."

Separately, `CAP_NET_RAW` is in Docker's default capability set ([moby daemon/pkg/oci/caps/defaults.go](https://github.com/moby/moby/blob/3f38b15147b14ce0ce7b11558a1aa9e9f14db103/daemon/pkg/oci/caps/defaults.go)), but only for root inside the container; a non-root user does not get it in its effective set, and hardening with `cap_drop: [ALL]` removes it. Unprivileged ping sockets avoid depending on it.

Consequences for ddash's compose deployment (Docker host is an LXC on Proxmox):

| Situation | Unprivileged ICMP works? | What to do |
|---|---|---|
| Default bridge network, non-root user, `cap_drop: [ALL]` | Yes (Docker sets the sysctl) | Nothing |
| `network_mode: host` | Only if the *LXC's* `ping_group_range` allows it | Avoid host networking, or set the sysctl in the LXC, or add `cap_add: [NET_RAW]` and use privileged mode |
| Docker with `userns-remap`, or Docker running inside a user namespace (e.g. an *unprivileged* LXC) | No, Docker skips the sysctl | Add `sysctls: { net.ipv4.ping_group_range: "0 2147483647" }` in compose, or `cap_add: [NET_RAW]` |

The last row matters: if the Docker-host LXC is an unprivileged Proxmox container, Docker runs in a user namespace (`userns.RunningInUserNS()` is true) and will *not* set the sysctl. One explicit compose `sysctls:` line then fixes it without granting a capability. Worth confirming on the real host with `docker run --rm alpine sysctl net.ipv4.ping_group_range`.

**Library: `github.com/prometheus-community/pro-bing`** (latest release v0.9.1). README ([github.com/prometheus-community/pro-bing](https://github.com/prometheus-community/pro-bing)):

- Linux default is unprivileged: it "attempts to send an "unprivileged" ping" over a datagram socket, which "must be enabled" via `sysctl -w net.ipv4.ping_group_range="0 2147483647"` (Docker does this, above).
- `pinger.SetPrivileged(true)` switches to raw sockets; grant with `setcap cap_net_raw=+ep` or run as root.
- Usage: `probing.NewPinger(host)`, set `Count`, `Run()` (blocks), then `Statistics()` (packets sent/received, loss, min/avg/max RTT).
- Gotcha from source: `New()` sets `Interval: time.Second` and `Timeout: time.Duration(math.MaxInt64)` ([ping.go](https://github.com/prometheus-community/pro-bing/blob/main/ping.go)), so a Check **must** set `Timeout` (and probably `Count = 1..3`, `Interval` short) or a dead host blocks forever. Prefer `RunWithContext(ctx)`.

Writing this on raw `golang.org/x/net/icmp` is possible (example in the package docs) but means handling echo IDs/sequence matching, IPv4/IPv6 and timeouts by hand; pro-bing is a thin, maintained wrapper over exactly that.

## 2. Uptime Kuma as prior art for history and uptime %

All from [louislam/uptime-kuma @ b092505](https://github.com/louislam/uptime-kuma/tree/b092505f894d66669b67f46e1da72b51016eceab) (v2 line).

### Raw beats: `heartbeat`

[`db/knex_init_db.js`](https://github.com/louislam/uptime-kuma/blob/b092505f894d66669b67f46e1da72b51016eceab/db/knex_init_db.js#L125-L148):

| column | type | meaning |
|---|---|---|
| `id` | increments | |
| `important` | boolean, default false | true when status changed (the beat that triggers a notification) |
| `monitor_id` | int FK → monitor, cascade | |
| `status` | smallint | `DOWN=0, UP=1, PENDING=2, MAINTENANCE=3` ([src/util.ts](https://github.com/louislam/uptime-kuma/blob/b092505f894d66669b67f46e1da72b51016eceab/src/util.ts#L40-L43)) |
| `msg` | text | error / status message |
| `time` | datetime | |
| `ping` | int | latency ms |
| `duration` | int | seconds since previous beat |
| `down_count` | int | consecutive downs (for resend logic) |
| `end_time` | datetime, nullable | added later ([migration](https://github.com/louislam/uptime-kuma/blob/b092505f894d66669b67f46e1da72b51016eceab/db/knex_migrations/2023-08-18-0301-heartbeat.js)) |

Indexes: `(monitor_id, time)`, `(monitor_id, important, time)`, `important`, `monitor_id`.

### Aggregates: `stat_minutely`, `stat_hourly`, `stat_daily`

[`2023-08-16-0000-create-uptime.js`](https://github.com/louislam/uptime-kuma/blob/b092505f894d66669b67f46e1da72b51016eceab/db/knex_migrations/2023-08-16-0000-create-uptime.js), [`2023-12-22-0000-hourly-uptime.js`](https://github.com/louislam/uptime-kuma/blob/b092505f894d66669b67f46e1da72b51016eceab/db/knex_migrations/2023-12-22-0000-hourly-uptime.js), [`2023-12-21-0000-stat-ping-min-max.js`](https://github.com/louislam/uptime-kuma/blob/b092505f894d66669b67f46e1da72b51016eceab/db/knex_migrations/2023-12-21-0000-stat-ping-min-max.js). Each table has the same shape:

```
id, monitor_id (FK, cascade),
timestamp INTEGER  -- unix seconds rounded down to the minute / hour / day
ping FLOAT         -- average ping ms (UP beats only)
ping_min FLOAT, ping_max FLOAT
up SMALLINT, down SMALLINT   -- beat counts in the bucket
UNIQUE (monitor_id, timestamp)
```

(A later migration adds an `extras` JSON column, used for maintenance counts.)

### How uptime % is computed

[`server/uptime-calculator.js`](https://github.com/louislam/uptime-kuma/blob/b092505f894d66669b67f46e1da72b51016eceab/server/uptime-calculator.js):

- On every beat, `update(status, ping)` increments the current minute, hour and day bucket in memory and upserts the three rows. `MAINTENANCE` beats only bump a `maintenance` counter (not `up` or `down`), so they **drop out of the ratio**. `UP` increments `up` and folds ping into running avg/min/max. Everything else (`DOWN`, `PENDING`) increments `down` (L212–L282; `flatStatus` maps PENDING to DOWN, L541–L556).
- `getData(num, type)` sums `up` and `down` over the last `num` buckets and returns `uptime = up / (up + down)` (0 if no beats) and `avgPing = Σ(avgPing·up) / Σup` (L600–L688). So uptime is **beat-weighted**, not time-weighted.
- `getDataByDuration("24h" | "30d" | "1y")` picks the granularity: `m` → minutely, `h` → hourly, `d`/`w`/`M`/`y` → daily (L770–L800). In memory it keeps `LimitQueue(24*60)` minutely, `LimitQueue(30*24)` hourly, `LimitQueue(365)` daily (L34–L47).

### Retention

- `stat_minutely` kept 24 h, `stat_hourly` kept 30 d (`statMinutelyKeepHour = 24`, `statHourlyKeepDay = 30`), pruned inline on each `update` (L63–L64, L358–L370).
- A daily cron job at `03:14` runs `clearOldData` ([server/jobs.js](https://github.com/louislam/uptime-kuma/blob/b092505f894d66669b67f46e1da72b51016eceab/server/jobs.js)):
  - deletes **non-important** heartbeats older than 24 h, keeping at least the latest 100 per monitor ([server/database.js `clearHeartbeatData`](https://github.com/louislam/uptime-kuma/blob/b092505f894d66669b67f46e1da72b51016eceab/server/database.js#L1047-L1072));
  - deletes all heartbeats and `stat_daily` rows older than `keepDataPeriodDays` (default **365**; `< 1` disables) and runs `PRAGMA optimize` on SQLite ([server/jobs/clear-old-data.js](https://github.com/louislam/uptime-kuma/blob/b092505f894d66669b67f46e1da72b51016eceab/server/jobs/clear-old-data.js)).
- A second job runs incremental vacuum (same `jobs.js`).

### Minimal schema this suggests for ddash (proposal, not decided)

Keep Kuma's shape but drop what "is it up?" does not need:

```sql
-- one row per state change (Kuma's "important" beats) — the incident log
CREATE TABLE check_event (
  check_id   INTEGER NOT NULL,
  at         INTEGER NOT NULL,        -- unix seconds
  up         INTEGER NOT NULL,        -- 0/1
  latency_ms INTEGER,
  message    TEXT,
  PRIMARY KEY (check_id, at)
);

-- per-bucket counts; uptime% = SUM(up)/SUM(up+down) over buckets
CREATE TABLE check_stat (
  check_id   INTEGER NOT NULL,
  bucket     INTEGER NOT NULL,        -- unix seconds, floor to hour (or minute)
  up         INTEGER NOT NULL DEFAULT 0,
  down       INTEGER NOT NULL DEFAULT 0,
  maint      INTEGER NOT NULL DEFAULT 0,
  latency_avg_ms REAL,
  latency_max_ms REAL,
  PRIMARY KEY (check_id, bucket)
) WITHOUT ROWID;
```

- Upsert with `INSERT ... ON CONFLICT(check_id, bucket) DO UPDATE SET up = up + excluded.up, ...`, so no in-memory cache is required.
- The latest result per Check can live in memory (or one `check_state` row) rather than storing every beat; the sparkline of recent beats can come from minute buckets.
- Retention with no settings: minute buckets 48 h, hour buckets 90 d (or 365 d; one Check at 60 s is 8,760 rows/year), state changes 365 d; one daily `DELETE` job.
- Whether to keep minute *and* hour buckets, or hourly only, is a call for the Check-model grilling ticket (#9).

## 3. ntfy publish API

From [docs.ntfy.sh/publish](https://docs.ntfy.sh/publish/).

**Plain publish:** `PUT` or `POST` to `https://<server>/<topic>`, body is the message. `curl -d "Backup successful 😀" ntfy.sh/mytopic`. Topic names are `[-_A-Za-z0-9]{1,64}`; on an open server the topic name is the only access control.

**Headers** (each has short aliases):

| Purpose | Header | Aliases | Values |
|---|---|---|---|
| Title | `X-Title` | `Title`, `ti`, `t` | text; default is the topic URL |
| Priority | `X-Priority` | `Priority`, `prio`, `p` | `5`/`max`/`urgent`, `4`/`high`, `3`/`default`, `2`/`low`, `1`/`min` |
| Tags | `X-Tags` | `Tags`, `tag`, `ta` | comma-separated; a tag matching an emoji shortcode (`warning`, `rotating_light`, `white_check_mark`, …) renders as an emoji, others are listed below the message |
| Click URL | `X-Click` | `Click` | URL opened on tap (e.g. the App or ddash's Check page) |
| Markdown | `X-Markdown` | `Markdown`, `md` | `yes`/`true`/`1` |

Messages over 4,096 bytes or with non-UTF-8 content are turned into attachments.

**JSON publish:** `POST`/`PUT` a JSON object to the server **root** (`https://ntfy.example/`), not the topic URL. Fields: `topic` (required), `message`, `title`, `tags` (string array), `priority` (int 1–5), `click`, `markdown` (bool), `icon`, `actions`, `attach`, `filename`, `delay`, `email`, `call`, `sequence_id`. JSON is the cleaner fit for a Go notifier (no header encoding of non-ASCII titles).

**Auth** (section "Authentication"):

- Access token: `Authorization: Bearer tk_...` (created with `ntfy token add` or in the web app's Account page). Also accepted as Basic auth with empty username (`curl -u :tk_...`).
- Username/password: `Authorization: Basic base64(user:pass)`; the docs warn to use HTTPS.
- Query param `?auth=<raw-base64 of the whole Authorization header value, '=' stripped>` for clients that cannot set headers. Not needed from Go.

**Suggested ddash mapping** (proposal): Check goes down → `priority: 4` (high), `tags: ["rotating_light"]`, `title: "<Check> is down"`; recovers → `priority: 3`, `tags: ["white_check_mark"]`; `click` → the App URL or ddash Check page. Config is three values in YAML: server URL, topic, token from env.

## Configuration surface this implies

Per Check: kind (`http|tcp|icmp|dns`) and target; everything else defaulted (interval 60 s, timeout 10 s, HTTP up = 2xx/3xx, DNS query name default e.g. the server's own zone or `example.com`). Global: ntfy URL/topic/token. Deployment: nothing extra on a bridge network; one `sysctls:` line if Docker runs inside an unprivileged LXC.

## Open points for the grilling ticket (#9)

- Retries before declaring down (Kuma's PENDING state) vs. flip on first failure.
- Bucket granularity (minute+hour vs hour-only) and retention numbers.
- Whether "up %" is beat-weighted (Kuma) or time-weighted from state changes.
- Verify on the real Docker-host LXC whether it is unprivileged (affects the ICMP sysctl line).
