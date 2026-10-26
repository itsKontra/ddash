# pfSense REST API: DNS Resolver Host Overrides

Research for [#136](https://github.com/itsKontra/ddash/issues/136), part of the map [#134](https://github.com/itsKontra/ddash/issues/134). Gathered 2026-10-26.

The question: can the pfSense REST API that `components/pfsense-api` uses list, create, change and delete DNS Resolver Host Overrides? How are changes applied, how do ids behave, how does this fit the existing write queue, and does an App's aliases fit a Host Override's own aliases or one Host Override per hostname?

## Sources

- **Live firewall (L).** Read-only `GET` requests with the key from `pfsense.env`. No write was sent. Hostnames and addresses are left out here; only shapes and counts are recorded.
- **Live OpenAPI schema (S).** `GET /api/v2/schema/openapi`, served by the installed package. Each operation's description states its privileges and whether it *applies immediately*.
- **Package source (C).** [pfrest/pfSense-pkg-RESTAPI at tag `v2.10.2`](https://github.com/pfrest/pfSense-pkg-RESTAPI/tree/v2.10.2/pfSense-pkg-RESTAPI/files/usr/local/pkg/RESTAPI), which matches the installed version. Paths below are relative to `RESTAPI/`.
- **Package docs (D).** At the same tag: [`docs/WORKING_WITH_OBJECT_IDS.md`](https://github.com/pfrest/pfSense-pkg-RESTAPI/blob/v2.10.2/docs/WORKING_WITH_OBJECT_IDS.md) and [`docs/COMMON_CONTROL_PARAMETERS.md`](https://github.com/pfrest/pfSense-pkg-RESTAPI/blob/v2.10.2/docs/COMMON_CONTROL_PARAMETERS.md).
- **pfSense source (P).** [pfsense/pfsense `master`](https://github.com/pfsense/pfsense/tree/master/src/etc/inc) (`services.inc`, `unbound.inc`, `system.inc`). There is no public `RELENG_2_9_0` branch, so this is the closest public code to the installed 2.9.0. The functions quoted are long-standing.
- **ddash code (R).** `components/pfsense-api/writes.go`, `alias_writes.go`, `pfsense_api.go` on `main` at `e3a234b`.

## Versions

| Fact | Value | Source |
|---|---|---|
| pfSense | `2.9.0-RELEASE`, patch 0 | L `GET /api/v2/system/version` |
| REST API package | `pfSense-pkg-RESTAPI` **v2.10.2**. This is the latest; `update_available: false`. | L `GET /api/v2/system/restapi/version` |
| Recorded test data | `components/pfsense-api/testdata/` has **no** `services/dns_resolver/*` fixtures. Host Overrides have never been read by ddash. | R |

These are the same versions [`research/pfsense-api`](https://github.com/itsKontra/ddash/blob/research/pfsense-api/docs/research/pfsense-api.md) recorded on 2026-10-19.

## Endpoints

All of these exist on the installed package (S). Every one is marked **"Applies immediately: No"**. Each has its own privilege, `api-v2-services-dns-resolver-host-override[s][-alias[es]]-<method>`, and `page-all` grants them all.

| Path | Methods | What it does |
|---|---|---|
| `/api/v2/services/dns_resolver/host_overrides` | `GET` | List all, with `limit`, `offset`, `sort_by`, `sort_order` and query filters |
| | `PUT` | **Replace every** Host Override with the array sent |
| | `DELETE` | Delete every Host Override matching a query ("use with caution") |
| `/api/v2/services/dns_resolver/host_override` | `GET` (`id`), `POST`, `PATCH` (`id`), `DELETE` (`id`, `apply` in the query) | One Host Override |
| `/api/v2/services/dns_resolver/host_override/aliases` | `GET`, `DELETE` (query) | Every Host Override alias, across all parents |
| `/api/v2/services/dns_resolver/host_override/alias` | `GET`, `POST`, `PATCH`, `DELETE`, each with `parent_id` + `id` | One alias inside one Host Override |
| `/api/v2/services/dns_resolver/apply` | `GET` → `{applied: bool}`; `POST` | Pending-change status; apply pending changes |

## Fields

`DNSResolverHostOverride` (C `Models/DNSResolverHostOverride.inc`, S):

| Field | Type | Notes |
|---|---|---|
| `id` | int | Array position (see below) |
| `host` | string | Required, but may be empty, which overrides the bare domain. Max 255, hostname-validated. |
| `domain` | string | Required, hostname-validated |
| `ip` | string[] | Required, IPv4 and/or IPv6. Stored in config as a comma-joined string. |
| `descr` | string | Default `""` |
| `aliases` | `DNSResolverHostOverrideAlias[]` | Nested. Default `[]`. |

- **Uniqueness:** `host` + `domain` together must be unique among Host Overrides (`unique_together_fields`). Otherwise the call fails with `MODEL_FIELDS_NOT_UNIQUE_TOGETHER`. This check does **not** look at aliases, or at other Host Overrides' aliases (C `Core/Model.inc` `check_unique_together`, which queries only the same model and parent).
- **Required on POST:** `host`, `domain` and `ip` (S request schema).

`DNSResolverHostOverrideAlias` (C `Models/DNSResolverHostOverrideAlias.inc`) has `parent_id`, `id`, `host` (may be empty), `domain` and `descr` (stored as `description`). An alias has **no IP of its own**: pfSense resolves each alias to its parent's `ip` list (P `system.inc` `system_hosts_override_entries`). `host` + `domain` is unique only *within one parent*.

### Live data (L)

- 6 Host Overrides, all in one domain, each with exactly one IP.
- **One Host Override, the one whose address is the Reverse Proxy's, carries 23 aliases.** The other 5 have none. So today the Operator registers every proxied hostname as an alias of that one Host Override, not as a Host Override of its own.
- No alias has a description.
- The Host Overrides come back sorted by `host`. The 23 aliases do **not**: they are in the order they were added through the pfSense web UI.
- DNS Resolver settings: `enable: true`, `dnssec: true`, `regdhcp: false`, `regdhcpstatic: false`. The "register DHCP leases" and "register DHCP static mappings" options are off.

## Ids: array positions, and they move

- Ids are array positions in `config.xml`, not stable keys. "pfSense does not use persistent object IDs … the object ID can change if objects are reordered or deleted." The same goes for `parent_id` (D `WORKING_WITH_OBJECT_IDS.md`).
- **Deleting** an entry shifts every later id down by one (D).
- **Creating or updating also moves ids.** Both models set `sort_by = ['host']`, `SORT_ASC` (C). After every create or update, `Model::sort()` rewrites the whole list sorted by `host` and gives the written object its new position (C `Core/Model.inc` `_create`, `_update`, `sort`). Creating `cloud` before an existing `docker` turns `docker`'s id from N into N+1.
- **A Host Override's aliases are re-sorted whenever the parent is written.** `NestedModelField::to_internal()` sorts the nested aliases by `host` before saving (C `Fields/NestedModelField.inc`). Creating an alias through the child endpoint sorts that parent's aliases the same way (`Model::sort()` with `parent_id`). On the live firewall the first API write to the Reverse Proxy's Host Override, or to any of its aliases, will **reorder all 23 aliases**, and their ids will change.
- The response to a create or update carries the object's post-sort `id` (C `Model::sort()` updates `$this->id`). The ids of the *other* objects are not returned, so they must be read again.
- The stable key is `host` + `domain`: it is unique for Host Overrides, and unique within a parent for aliases. Query filters on `host` and `domain` work on the plural `GET` (S `query` parameter).

## Apply

- Every write only changes `config.xml` and marks the `unbound` subsystem dirty. Nothing reaches Unbound until an apply runs (S "Applies immediately: No"; C `Model::create/update/delete` → `mark_subsystem_dirty('unbound')`).
- There are two ways to apply, as with aliases and static mappings:
  1. Send `"apply": true` on the `POST`, `PATCH` or `DELETE`. For `DELETE` it goes in the query string (S `apply(query)`).
  2. Send any number of writes without `apply`, then `POST /api/v2/services/dns_resolver/apply` once. `GET .../apply` returns `{applied: false}` while changes are pending; it is `!is_subsystem_dirty('unbound')` (C `Models/DNSResolverApply.inc`). It was `{applied: true}` when read (L).
- **What an apply does:** `DNSResolverApplyDispatcher` runs `services_unbound_configure()`, then `system_resolvconf_generate()` and `system_dhcpleases_configure()`, then clears the dirty flag (C `Dispatchers/DNSResolverApplyDispatcher.inc`). `services_unbound_configure()` **stops Unbound with SIGTERM, waits up to 30 s for it to exit, regenerates its config and starts it again** (P `services.inc`, `unbound.inc` `sync_unbound_service`). This is a full restart, not an `unbound-control reload`. Name resolution through the firewall is down for a moment on every apply. The resolver cache is lost unless the "dump cache" option is on, and this API's DNS Resolver settings model does not expose that option (L).
- **Async by default.** The dispatcher is spawned in the background (`async: true`), so the HTTP call returns before Unbound is back (C `Core/Dispatcher.inc`; D `async`). With `apply: true` on a write, the model clears the dirty flag right after *spawning* the apply (C `Model::create` etc.). A following `GET .../apply` therefore says `applied: true` even while the restart is still running. `"async": false` waits for it, but the docs warn it can hang (D). ddash's per-call `requestTimeout` is 5 s (R `pfsense_api.go`), which is shorter than the 30 s the stop may wait.
- **Concurrency:** each dispatcher allows `max_queue = 10` running processes. Past that, a write fails with `503 DISPATCHER_TOO_MANY_QUEUED_PROCESSES` (C `Core/Dispatcher.inc`). Separate `apply: true` writes in quick succession each spawn their own Unbound restart.
- **Batching:** there is no transaction or batch endpoint (D has none; S has none for this model). The options are:
  - several writes without `apply`, then one `POST .../apply`. This gives one Unbound restart, but the writes are not atomic: a failure midway leaves earlier writes saved and pending, `applied: false`, and the pfSense web UI shows its "changes must be applied" banner.
  - `PATCH` one Host Override with its whole `aliases` array. This is one write, and it replaces that parent's aliases (C `NestedModelField`).
  - `PUT .../host_overrides` with the full list, which replaces **every** Host Override, including the ones ddash does not manage (S).
- **Config write lock:** each write takes the API's config lock and retries for up to 60 s while another API write holds it (C `Model::write_config`). This only serialises the API's own writes against each other.

## Fit with the existing write queue (R)

- `component.write` (R `writes.go`) is a one-slot channel. Every static mapping write and alias write already goes through it, and so would Host Override writes. Inside it, today's writes read the list again uncached, find the object by a stable key (MAC or alias name), send one request with `"apply": true` and then invalidate the caches.
- Host Overrides fit that pattern, keyed on `host` + `domain` (and `parent_id` + alias `host` + `domain` for an alias). Because creates and updates also move ids, a lookup must happen inside the queued write right before each request, and not once before a run of several requests. This applies even within a single queued operation, such as "create the new Host Override, then delete the old one".
- One difference from today's writes: an `"apply": true` Host Override write restarts Unbound. A multi-step App change sent as separate `apply: true` writes restarts it once per step. Sending the steps without `apply` and ending with one `POST .../dns_resolver/apply` restarts it once. Today's writes all use the first form (R `writes.go` header comment).
- The queue serialises ddash's own writes only. Edits in the pfSense web UI, or another API client, can move ids between ddash's read and its write. The same is already true of aliases and static mappings.
- `alias_writes.go` avoids the `append`/`remove` PATCH flags because of their array-diff quirks (R). `aliases` is a nested-model field, not a plain string array, so replacing the whole `aliases` list read inside the queue matches the existing style. Alternatively, the child `host_override/alias` endpoints edit one alias at a time.
- A new cache would sit next to `servers`, `leases` and `aliases`, and `write` would invalidate it. The fake (`pfsensefake`) and `testdata/` have no DNS Resolver endpoints yet.

## A Host Override's own aliases vs one Host Override per hostname

These are facts for the decision, not the decision.

| | Aliases on one Host Override | One Host Override per hostname |
|---|---|---|
| DNS answer | Same A/AAAA records. An alias resolves to the parent's `ip` (P `system_hosts_override_entries`). | Same |
| PTR | Unbound adds one PTR per IP, for the first name it meets with that IP (P `unbound.inc` `unbound_generate_zone_data`). Aliases come after their parent, so the parent gets the PTR. | Whichever Host Override sorts first by `host` gets the PTR for a shared IP |
| Own IP | No. Moving one hostname to another address means removing the alias and creating a Host Override (or an alias elsewhere). | Yes, per hostname |
| Description | Each alias has `descr` | Each Host Override has `descr` |
| Uniqueness checked by the API | Only within the same parent. The same name can exist as a Host Override and as an alias somewhere else. | Across Host Overrides |
| Ids | `parent_id` + `id`. Both move, and every write to the parent re-sorts all its aliases. | `id`, which moves on any create, rename or delete |
| What the live firewall does today | **This one.** 23 hostnames are aliases of the Reverse Proxy's Host Override (L). | The 5 other Host Overrides, each a single name |
| Writing one App's hostnames at once | One `PATCH` of the parent's `aliases` if they share a parent. That parent's list is shared with every other App on the Reverse Proxy. | One write per hostname |

If ddash reads "is this hostname registered" from Host Overrides only, none of the 23 alias hostnames on the live firewall counts as registered. Aliases must be counted for today's data to read as registered.

## Not checked

- No write was sent, so the re-sorting, the id shifts and the restart time were not observed live. They are taken from the source code.
- Whether a DNS server in front of Unbound (such as AdGuard) keeps answering from its cache during a restart was not looked at.
