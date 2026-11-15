# Release notes

What an Operator does on updating, per Release, newest first. Cutting a
Release moves **Unreleased** under its tag ([releasing](releasing.md)).

## Unreleased

- The shell's page now carries a full Content-Security-Policy: it runs
  scripts and loads styles only from ddash itself, connects only to
  ddash, and shows images from ddash or any http(s) site (Apps'
  favicons); Sign out may go on to Keycloak's origin. Nothing to do,
  unless your reverse proxy injects scripts or styles into ddash's pages
  (analytics, a CDN's script rewriting) or sets a Content-Security-Policy
  of its own: the browser now refuses the injected ones, and a second
  policy only narrows ddash's ([deployment](deployment.md#3-the-name-and-the-proxy)).
- Nothing to do: the registry credentials and the Anthropic API key that
  `v0.0.3-rc.2` left in compose's and ai-helper's stored config are removed
  from the core DB when each Component next starts. From now on a config
  field a Release removes doesn't stay stored. In return, rolling back to
  an older Release drops what the newer one stored in config fields the
  older one doesn't have: enter those again after updating once more.
- A draft's `Generate` entry in the Audit Log now records the system prompt
  and the schema by their length only.

## v0.0.3-rc.2

- **Enter two sets of credentials again.** Each external system is now
  reached by one `-api` Component (ADR-0007), and no config moved:
  - private-registry credentials and a Docker Hub mirror go into the new
    **registry-api** (they were compose's). Public images need nothing;
    until then private images show as refused.
  - the Anthropic API key, and the model or server if you changed them, go
    into the new **anthropic-api** (they were ai-helper's). Until then the
    AI App helper says "not set up".

  Both appear under their Plugin on the Plugins page.
- **Update every Agent** after updating ddash (Docker Page and each
  Workload's Host Agent Panel, **Update Agent**): the Agents' fixes take
  effect only then. A Host Agent no longer follows a symlink out of a
  user's home when it installs or removes an SSH Key; a Docker Agent
  updates itself only to the Docker Agent's own image, and refuses a
  Compose Project whose working directory lies outside its roots.
- **Turn on gzip for ddash's Proxy Host.** ddash serves its JavaScript and
  CSS uncompressed and leaves compression to the reverse proxy
  ([deployment](deployment.md#3-the-name-and-the-proxy)).
- ddash's container now also needs outbound HTTPS to `cdn.jsdelivr.net`:
  the core downloads each App's logo once and keeps it in `/data/icons`,
  and the browser no longer asks a CDN.
- Every response now carries `X-Frame-Options: DENY`, `nosniff` and, on an
  https public URL, HSTS for a year: ddash can no longer be shown in an
  `<iframe>`, and a browser that has seen it refuses plain http to its
  hostname.
- New in the Catalogue: **Energy** (solar, grid, battery and consumption
  from InfluxDB; set up `energy-api`). Compose Projects show and edit the
  `.env` and Dockerfiles beside the Compose file.
- A value on screen may be one refresh older than before: getters now
  answer at once with what they have and read the system behind that
  answer. Actions and Plans still read fresh.
- A system that refuses ddash's credentials now reads as "permission
  denied" in every Component, where some said "unavailable".
- `make build` runs the web build only when a source changed
  (`make -B build` forces it).

## v0.0.3-rc.1

- The GitHub release now ships each Plugin as one `<plugin>.ddapp` and each
  Component as one `<component>.ct` (zips) instead of loose binaries and
  bundles. Nothing to do on updating: ddash downloads the new files into
  `/data` itself. If you run from a local Release dir (`DDASH_RELEASE_DIR`),
  rebuild it with `make release-dir`; ddash unpacks its Component binaries
  into `/data/release-dir/`, so the mounted dir can stay read-only.
- **Docker Agents and Host Agents** (ADR-0005): ddash can now change what
  runs on a host through an Agent you install there. Nothing changes until
  you add one. Docker Page, **Docker Agents**: add a host, run the Compose
  file ddash shows (`/opt/ddash-docker-agent`), and its Compose Projects
  become editable, startable and updatable from ddash; ddash then reads that
  host through the Agent and its socket-proxy can go. For an LXC or VM, the
  Workload's **Host Agent** Panel gives the `install.sh` command
  ([deployment](deployment.md#9-docker-agents-optional-per-docker-host)).
  Allow ddash to reach port 7443 (Docker Agent) and 7444 (Host Agent).
- Mark what reaching ddash depends on as Protected in its Compose file:
  `labels: { ddash.protected: "true" }` on ddash itself and Keycloak.
- Package updates take a ddash Snapshot first and backups run from ddash:
  ddash's Proxmox role gains `VM.Snapshot`, `VM.Backup` and
  `Datastore.AllocateSpace` on the backup storage, Protected Workloads
  included ([deployment](deployment.md#7-proxmox-role-token-and-protected-acls)).
- Notifications now go through a new `notify` Component to ntfy and, when
  you set up `telegram-api`, Telegram; which Notifications each channel
  gets is in notify's config. Checks Notifications reach ntfy as before.
- Apps require `cloudflare-api` (for "On the internet") and
  `docker-agent-api`; both start unconfigured and change nothing until set
  up. New Plugins in the Catalogue: Cloudflare Tunnel, Calendar, AI App
  helper. Remote Access gains **Add a device**: set network's Remote Access
  endpoint, and give the pfSense user the WireGuard peer privileges
  ([deployment](deployment.md)).
- `make smoke` also reads Cloudflare and the calendars when `smoke.env` sets
  `SMOKE_CLOUDFLARE_API` and `SMOKE_CALENDAR_API` (see `smoke.env.example`);
  without them it skips both.

## v0.0.2-rc.4

- An Action you confirmed now runs to its end even when you close the tab,
  and ddash lets a running Action finish before it stops for an update. Set
  `stop_grace_period: 90s` on the ddash service in your compose file, as
  [`deploy/compose.yaml`](../deploy/compose.yaml) does, so Docker gives it
  that time.

## v0.0.2-rc.3

- ddash no longer reads nginx over SSH: the Reverse Proxy and Apps read
  Nginx Proxy Manager's API. Remove ddash's line (`command="/usr/sbin/nginx -T"`)
  from root's `authorized_keys` on the nginx host. Its old SSH settings are
  dropped on the update, so the Reverse Proxy and Apps ask you to set up
  Nginx Proxy Manager: create ddash's user there
  ([deployment](deployment.md#6-the-systems-ddash-reaches)) and fill in
  nginx-proxy-manager-api's config in Plugins.
- Apps shows each App's network info, so it now requires network, and with
  it adguard-api. Without the Network Plugin installed, fill in their
  config in Plugins; until then Apps work without the network info.
