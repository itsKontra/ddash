# Release notes

What an Operator does on updating, per Release, newest first. Cutting a
Release moves **Unreleased** under its tag ([releasing](releasing.md)).

## Unreleased

- The Energy Page's Sankey splits what was "Lost or unmetered" into
  **Battery losses** (from the Victron's VE.Bus counters, where it has
  them) and **Unmetered**. The use from before the Victron's first
  readings (or in a gap) goes to **Not split** and the Sankey says so,
  instead of counting as unmetered, which made a Year look half
  unmetered. Where it has the VE.Bus counters, the house is what they
  count to the AC output. energy-api attributes its cached hours again
  once after updating.

- The Energy Page splits where the energy went every 5 minutes instead of
  from the period's totals, so a day the battery charges from the sun in
  the morning and from the grid in the afternoon shows its direct solar
  use. The Sankey, the self-sufficiency and the ledger's own use come
  from that one split and agree; the ledger's savings count what the grid
  charged into the battery as bought. Where the Victron's VE.Bus counters
  are missing (a backfilled history), the split comes from the power
  readings and says it is estimated. energy-api caches the split hours in
  its own DB, so a Month or Year read after the first sums cached hours.

- The Energy Page's **Energy** panel draws the period as a Sankey: solar,
  battery and grid flow into the export, the battery and the use, and the
  use into the house, the heat pump and what was lost or unmetered, each
  with its kWh; hover or focus a flow for its own. **Money** is a ledger:
  bought less sold makes the grid bill (or what the feed-in earned net),
  a bar shows how much of the grid cost the feed-in covered, and the
  saving is split into what wasn't bought and the feed-in.

- Optional: set **Battery backup reserve (%)** in energy-api's config, and
  Energy counts the battery's time left down to that reserve instead of
  0 %, says "at backup reserve" once it gets there, and shows the reserve
  beside the battery on the Energy Page. Empty keeps counting to 0 %.

## v0.0.4-rc.1

- **To do after updating**: run **Update Agent** on each Docker Agent
  (Docker, Docker Agents) and each Host Agent (Proxmox). Until then they
  show as outdated and ddash doesn't read through them.
- The Docker Agent's own Compose Project (`ddash-docker-agent`) says it is
  the Docker Agent and how it is updated. It no longer offers Restart,
  Stop, Pull & update or its Compose file, none of which could work, and
  ddash refuses those Actions on it.
- A Compose Project's **Images** say why they weren't checked: "pinned by
  digest" or "local image", no longer "no registry" for both.
- Opening **Plugins** at `/plugins` directly, or reloading it, shows the
  page. It used to redirect to `/plugins/` and answer 404.
- The Docker Page offers **Update Agent** on a host it can't read because
  the host's Docker Agent runs another Release, with the same confirm as
  the Docker Agents list. Any other reason a host can't be read is only
  shown, as before.
- `make smoke`: a `smoke.env` config that isn't valid JSON fails naming
  its variable and the line and column, no longer quoting the value there,
  which may be a secret.
- A chart's legend entries show and hide their series: click **Used** on
  the Energy Page's power curve and its line and read-out go, and the
  scale fits the rest; click again to bring it back. The last series
  shown stays. What you hide stays hidden while you step through days or
  switch Day and Week, until you leave the Page.
- Notification channels are Plugins you install: **ntfy** and
  **Telegram** are in Plugins, each with no Page of its own. Installing
  one opens its config form (ntfy's topic; Telegram's bot token and chat
  ID, both required now) with **Send test**, and the switches for which
  Notifications it gets, which used to be notify's. Uninstalling one stops
  its Notifications. Checks, Docker and Proxmox no longer pull ntfy-api,
  telegram-api or notify in, so a channel in trouble shows down on its own
  Plugin only. The Checks Page's Notifications panel lists the installed
  channels with where each delivers to, what it gets and its last
  problem, and says so when none is installed.
- **On updating, nothing to do**: the first start installs the Plugin of
  each channel you had set up, with the switches you had for it, so
  Notifications keep arriving. A channel you hadn't set up stays
  uninstalled. notify is gone, and its config with it.

## v0.0.3

- **To do, for the Hosts Widget's meters**: give ddash's Proxmox API token
  `Sys.Audit` on `/nodes` and `Datastore.Audit` on `/storage`
  ([deployment](deployment.md)). Without them the new **Hosts** Widget
  shows the guests but says the token can't read the node.
- The Overview now says what is wrong and lets you fix it there. Its hero
  is a sentence ("Paperless is down. Everything else is up.") over a line
  of facts; the ring and the four Widget counts are gone. Under it,
  **Needs you** lists what every Plugin reports, worst first, each with
  the Action that fixes it: a down App with its stopped Workload as the
  cause and Start or Restart, a new Device with Adopt and Block, a failed
  image check with Check again, a guest or the node running out of memory
  or disk. Muted Checks and those in Maintenance sit folded under "Off on
  purpose". **Recently** at the bottom shows the last Actions and how they
  ended. Your saved layout is kept; the title block above the hero is
  gone and Edit sits in the hero's corner.
- New Widgets: **Network** (the route out, the WAN's throughput, Devices,
  ad blocking with its switch, Remote Access), **Hosts** (the node's CPU,
  memory and storage, and the guests under most pressure), **Docker
  hosts** (containers running, exited ones, image updates). **Energy now**
  leads with what the readings add up to ("Running on battery") instead
  of the flow diagram, which stays on the Energy Page. They are appended
  to a saved layout once; a fresh Overview starts with eight Widgets and
  offers the rest under Add Widget, and a Plugin installed later no longer
  places all of its Widgets.
- Optional: set **Battery capacity (kWh)** in energy-api's config, and
  Energy now also says how long the battery lasts or takes to fill at the
  current rate.
- **Pin Apps** from an App's detail or the Apps Widget's pin button, and
  order them: the Apps Widget then shows only the pinned ones.
- AdGuard's protection can be **paused for 10 min or 1 h** and resumed
  from the AdGuard and Network Widgets and the Network Page. AdGuard ends
  the pause itself.
- A finished run's result (an update, a Compose run, a backup) stays on
  its Page only while you have it open; afterwards it is in Recently and
  the Audit Log.
- After a Workload is started or restarted through ddash, its Apps' Checks
  are probed at once instead of at the next minute.

- Nothing to do: **Ctrl K** (⌘ K on a Mac, or `/` outside a text field)
  opens a command palette on every Page, also from "Search or run…" in
  the top bar. Type a word's start to open an App, go to a Page or the
  Audit Log, or switch light and dark; empty, it leads with what needs you
  and your pinned Apps. It also starts the Plugins' Actions: start,
  restart, stop or update a Proxmox guest, update all running guests,
  start, restart or update a Compose Project, go to a Device, add a
  Remote Access device ("restart pap" → Restart paperless). Each opens
  the same confirm or Plan as its button on the Page, and a Protected
  Workload offers no stop or restart.
- Nothing to do: a muted Check that is down no longer turns the Checks and
  Apps Widgets, the Overview or the Checks badge down; they say "N muted
  down" beside it instead.
- **Cancel run**: a Compose Project's run in flight (in its Plan Dialog and
  on its Page) can now be cancelled. Its Compose command is stopped
  part-way, its later steps don't run, nothing is rolled back, and the run
  says what it may have left half-done; run it again to finish. Only
  Compose runs: a package update and an Agent's own update can't be
  cancelled. It needs each Docker Agent on this Release: **Update Agent**
  on the Docker Page first, otherwise the cancel is refused with "update
  the Agent".
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
