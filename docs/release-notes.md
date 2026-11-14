# Release notes

What an Operator does on updating, per Release, newest first. Cutting a
Release moves **Unreleased** under its tag ([releasing](releasing.md)).

## Unreleased

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
- `make smoke` reads Cloudflare and the calendars: add `SMOKE_CLOUDFLARE_API`
  and `SMOKE_CALENDAR_API` to your `smoke.env` (see `smoke.env.example`).

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
