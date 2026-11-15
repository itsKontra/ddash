# Spec (draft): ddash manages what runs on the hosts — Agents, Compose Projects, LXC updates and new integrations

> **Draft.** This repo files specs as GitHub issues (`docs/agents/issue-tracker.md`). Unlike earlier specs, this one has no Wayfinder map behind it yet: the decisions marked **Proposed** are defaults chosen without a grilling ticket, and **Open Decisions** lists what a map should settle before implementation. Terms follow `GLOSSARY.md` (Operator, Action, Plan, Audit Log, Plugin, Component, Widget, Page, Release, App, Workload, Protected, Proxy Host, Host Override, Reverse Proxy, Check, Maintenance, Notification, Remote Access). New terms are proposed in [Glossary additions](#glossary-additions). Baseline: `main` at `02bec9a` (v0.0.2-rc.4).

## Problem Statement

ddash shows the Operator's Workloads, but it can barely change them. It can start, stop and restart a Proxmox LXC or VM, and nothing else.

- **Docker is read-only.** `docker-api` reads containers through `linuxserver/socket-proxy` with `POST=0`. Every Docker container on the Operator's hosts comes from a Docker Compose file, but ddash can't see those files, edit them, or bring a Compose Project up, down or onto newer images. The Operator SSHes into each host to do it.
- **Nobody says when an image is stale.** ddash doesn't compare a running container's image with its registry, so updates are found by chance.
- **LXCs are updated by hand.** Updating packages means logging into each LXC. No snapshot is taken first unless the Operator remembers, and no snapshots are pruned afterwards.
- **SSH access is set up by hand.** Enabling `sshd`, installing a key and turning off password login happen per LXC, from memory.
- **A new App takes many trips.** Add App (#157) already creates the Proxy Host and Host Override in one Plan. The Compose file, the `docker compose up`, and the Workload behind the App still happen elsewhere.
- **Several things live outside ddash entirely:** the Cloudflare Tunnel's Public Hostnames, Remote Access peers for a new phone, the house's photovoltaic and energy data, the Operator's calendar, and Notifications on Telegram.

## Solution

**Agents.** ddash ships two small programs in every Release, and the Operator installs them on the hosts ddash should control:

- the **Docker Agent**, a container started from a Compose file ddash generates. It mounts the Docker socket and the Compose Projects' directories, and serves ddash over HTTPS.
- the **Host Agent**, a service installed by a script ddash links to. It supports Ubuntu, Debian, Alpine, Fedora and Arch Linux, and gives ddash a fixed set of operations: package updates, and SSH setup.

Both are downloaded from the ddash installation, run the same Release as the core, and accept only ddash. Both hold root-equivalent power on their host, and the Operator opts in per host by installing one.

**Compose Projects in the Docker Plugin.** Every Compose Project on a host with a Docker Agent is listed with its Compose services and containers. The Operator can view and edit its Compose file (validated before it is saved), and can start, stop, restart and update it. Update means pull, then recreate, as one click and one Plan. A new Compose Project can be added from ddash, and adding one can also create its App: a Host Override in pfSense and a Proxy Host in Nginx Proxy Manager, in the same Plan as Add App.

**Image updates.** ddash compares every running container's image digest with its registry. When a newer image exists, it marks the container and Compose Project, and sends one Notification.

**LXC updates, snapshots and backups in the Proxmox Plugin.** On an LXC or VM with a Host Agent, the Operator updates its packages with one click. ddash takes a ddash Snapshot first, keeps exactly one ddash Snapshot per Workload, and deletes the old one only after the new one has succeeded. The Operator can also take a backup, and "Update all running" updates every eligible Workload one after another.

**SSH Keys in the Proxmox Plugin.** ddash keeps named public SSH Keys. The Operator either generates a key pair in ddash and downloads its private key once, or pastes an existing public key. Any stored key can be installed on a Workload with a Host Agent. The same Panel enables `sshd` and turns off password login.

**New Plugins**

- **Cloudflare Tunnel:** list and edit its Public Hostnames, and enable or disable each one.
- **Remote Access peers:** add a peer and show its WireGuard config as a QR code once, with a Widget for the Overview.
- **Energy:** photovoltaic production and household usage as live charts, and historic data from the Operator's database.
- **Calendar:** recent and upcoming events from Google Calendar, as an agenda Widget and Page.
- **Telegram:** a second channel for Notifications, beside ntfy.
- **AI App helper:** given a description, it drafts a Compose file and the Add App fields with Claude. The Operator reviews the draft, and it runs only through the normal Plan.

## User Stories

**Installing Agents**

1. As an Operator, I want ddash to give me a ready Compose file for a Docker host's Docker Agent, so that installing it is one `docker compose up -d`.
2. As an Operator, I want to pick which directories hold my Compose Projects before ddash generates that file, so that the Docker Agent mounts only what it needs.
3. As an Operator, I want a one-line install command and a downloadable script for the Host Agent, with its checksum shown, so that I can install it on Ubuntu, Debian, Alpine, Fedora or Arch Linux and verify what I run.
4. As an Operator, I want each Agent to pair with ddash using a one-time enrolment code, so that no other program can drive it.
5. As an Operator, I want the setup screen to say plainly that an Agent has root-equivalent control of its host, so that I install one knowingly.
6. As an Operator, I want an Agent from an older Release to be flagged, with "Update Agent" as its only allowed Action, so that the Agents never drift from the core.
7. As an Operator, I want to remove an Agent from ddash and be told how to uninstall it from the host, so that nothing is left listening.

**Compose Projects**

8. As an Operator, I want every Compose Project on each host listed with its Compose services, containers, state and images, so that I see what runs where in Compose's own terms.
9. As an Operator, I want containers that weren't started by Compose to stay listed as they are today, so that nothing disappears.
10. As an Operator, I want to open a Compose Project's file, and its `.env` when there is one, so that I can read its configuration from ddash.
11. As an Operator, I want to edit the Compose file in ddash, with `docker compose config` run on the host before it is saved, so that a syntax or interpolation error never reaches the disk.
12. As an Operator, I want a save to show a diff in a Plan, keep the previous file as a backup, and refuse when the file changed on disk since I opened it, so that I never overwrite a change I didn't see.
13. As an Operator, I want to choose whether a save also recreates the changed Compose services, so that I can stage an edit without applying it.
14. As an Operator, I want to start, stop and restart a Compose Project, or one of its Compose services, so that I control it without SSH.
15. As an Operator, I want stop and restart to ask me to confirm first and put the Project's Apps into Maintenance, so that ddash's own Action doesn't page me.
16. As an Operator, I want Update to pull newer images and recreate only the Compose services whose images changed, with the Plan listing each image's current and new digest, so that one click does the update and I know what it will change.
17. As an Operator, I want Update to say "already up to date" and do nothing when no image changed, so that an update without changes is harmless.
18. As an Operator, I want an update's progress streamed step by step, and where it stopped when a step failed, so that I know the state of the host.
19. As an Operator, I want to add a new Compose Project by naming it, picking a host and a directory, and writing or pasting its Compose file, so that new stacks start in ddash.
20. As an Operator, I want writes to a Compose Project refused while a Workload behind it is Protected, unless I unlock it in its own confirmation, so that ddash can't take down what it depends on.

**Image updates**

21. As an Operator, I want each running container to show whether a newer image exists for its tag, so that I know what is stale.
22. As an Operator, I want a Compose Project to show "update available" when any of its images is stale, with Update next to it, so that finding and fixing are one place.
23. As an Operator, I want one Notification when an image becomes stale, rather than one per check, so that updates don't flood my phone.
24. As an Operator, I want images pinned by digest, and local images that have no registry, to be shown as "not checked", so that ddash doesn't claim what it can't know.
25. As an Operator, I want registry checks to stay well under Docker Hub's rate limits, and to say so when a registry refuses ddash, so that checking never blocks my pulls.
26. As an Operator, I want the Docker Widget to count stale containers, so that the Overview shows updates waiting.

**A new App from a Compose Project**

27. As an Operator, I want Add App to offer "from a new Compose Project", so that the Compose file, the Host Override and the Proxy Host are one Plan.
28. As an Operator, I want the Proxy Host's upstream filled in from the Compose service's published port and its host's address, so that I don't type what ddash can read.
29. As an Operator, I want the new App's Workload to be its container, so that Checks, Maintenance and network info work from day one.
30. As an Operator, I want a failed step to leave its half-done state as Warnings, as Add App already does, so that I fix it with the same tools.

**AI App helper**

31. As an Operator, I want to describe an app in a sentence ("Paperless-ngx with Postgres and Redis") and get a draft Compose file and Add App fields, so that I start from something that runs.
32. As an Operator, I want the draft to open in the normal editor and Plan, never to run on its own, so that nothing reaches a host unreviewed.
33. As an Operator, I want to enter my Anthropic API key once, as a write-only secret, so that the key never shows again.
34. As an Operator, I want to see what ddash will send to the AI provider before it sends it, and never to send secrets from my `.env` files, so that I know what leaves the network.

**Proxmox: updates**

35. As an Operator, I want an Update button on every LXC and VM with a Host Agent, so that its packages are updated without logging in.
36. As an Operator, I want the Plan to list the packages that will change, read through the Host Agent, so that I know what an update does before I confirm.
37. As an Operator, I want the update to take a ddash Snapshot first, by default, so that I can roll back by hand when it goes wrong.
38. As an Operator, I want to turn off the snapshot per Workload, or for one run, so that Workloads on storage that can't snapshot can still be updated.
39. As an Operator, I want the Workload in Maintenance while it updates, and "reboot required" shown afterwards with a Restart button, so that I choose when it reboots.
40. As an Operator, I want "Update all running" to update every running Workload with a Host Agent one after another, snapshot each one first, and stop at the first failure, so that one click updates the lab without breaking everything at once.
41. As an Operator, I want "Update all running" to skip Protected Workloads and list the skipped ones in its Plan, so that ddash's own path stays untouched.
42. As an Operator, I want an update to keep running when I close the tab, and to show its outcome on the Workload and in the Audit Log, so that a long update is never left half-done by my browser.

**Proxmox: snapshots and backups**

43. As an Operator, I want to take a ddash Snapshot of any LXC or VM on demand, so that I can protect a change I'm about to make myself.
44. As an Operator, I want ddash to keep exactly one ddash Snapshot per Workload, deleting the old one only after the new one has succeeded, so that snapshots don't pile up and I always have one.
45. As an Operator, I want ddash Snapshots to be recognisable by their name and description, and snapshots I took by hand never touched, so that retention only prunes what ddash made.
46. As an Operator, I want to see each Workload's ddash Snapshot and its age, so that I know what I'd roll back to.
47. As an Operator, I want to start a backup of a Workload to a storage I pick, and see it finish, so that I don't open the Proxmox UI for a backup.

**Proxmox: SSH**

48. As an Operator, I want to store public SSH Keys under a name, so that I can install them on Workloads later.
49. As an Operator, I want to generate a key pair in ddash and download the private key exactly once, after which ddash keeps only the public key, so that the private key never sits in ddash.
50. As an Operator, I want to paste an existing public key instead, so that a key I already use works too.
51. As an Operator, I want to install one or more stored SSH Keys on a Workload, so that I can SSH into it.
52. As an Operator, I want to enable `sshd` on a Workload that doesn't run it, so that a fresh LXC becomes reachable.
53. As an Operator, I want to turn off password login and root login, refused unless at least one SSH Key is installed and `sshd -t` accepts the new config, so that ddash never locks me out.
54. As an Operator, I want to see which SSH Keys are installed on each Workload, and remove one, so that I can revoke a lost laptop's key.

**Cloudflare Tunnel**

55. As an Operator, I want to see my Cloudflare Tunnel's connectors and their state, so that I know whether the Tunnel is up.
56. As an Operator, I want to list its Public Hostnames with the local service each forwards to, so that I see what my network exposes to the internet.
57. As an Operator, I want to add, edit and remove a Public Hostname, including its Cloudflare DNS record, through a Plan, so that exposing a service is one confirmed Action.
58. As an Operator, I want to disable and re-enable a Public Hostname without losing its settings, so that I can close something temporarily.
59. As an Operator, I want each App to show whether it is reachable from the internet through the Tunnel, so that exposure is visible where I look at the App.

**Remote Access peers**

60. As an Operator, I want to add a Remote Access peer and see its WireGuard config as a QR code, so that a phone joins by scanning.
61. As an Operator, I want the QR code shown once, with its private key never stored by ddash, so that it can't leak later.
62. As an Operator, I want a Widget with "Add a device" and the peers' last handshake, so that Remote Access is on the Overview.

**Energy**

63. As an Operator, I want a Widget showing current PV production, house consumption, and grid import or export, so that I see the energy flow at a glance.
64. As an Operator, I want a Page with production and consumption charts for today, so that I see the day's curve.
65. As an Operator, I want to pick a past day, week, month or year and see its totals and chart from my database, so that I can look up history.
66. As an Operator, I want ddash to read my existing database read-only, so that it never changes my recorded data.

**Calendar**

67. As an Operator, I want a Widget showing my next events, so that my day is on the Overview.
68. As an Operator, I want a Page with the agenda for the coming days and the events just past, so that I can look further ahead.
69. As an Operator, I want ddash to read my calendars read-only, so that it can't change them.

**Telegram**

70. As an Operator, I want to receive ddash's Notifications in a Telegram chat, so that they reach me where I already read messages.
71. As an Operator, I want to choose per channel which Notifications it gets (Checks, image updates, Action outcomes), so that I can keep noise off one channel.
72. As an Operator, I want "Send test" for Telegram, as ntfy has, so that I can check the setup.

## Implementation Decisions

All decisions here are **Proposed**: they still need a Wayfinder map's grilling tickets to confirm them.

### Agents (two new programs, shipped in the Release)

- **Lockstep.** Both Agents are built and released with the core (ADR-0001). The Docker Agent is an image tagged with the Release; the Host Agent is a static binary per architecture (amd64, arm64) attached to the Release. The ddash installation serves both: `GET /agent/host/install.sh`, `/agent/host/<arch>` with checksums, and a generated Compose file for the Docker Agent that names its image by digest.
- **Pairing.** ddash creates a one-time enrolment code per Agent. On first contact the Agent sends the code, and both sides store a long-lived token. ddash pins the Agent's self-signed TLS certificate fingerprint at pairing (trust on first use). After pairing, every request carries the token, and ddash refuses an Agent whose fingerprint changed.
- **Fixed operations, no shell.** Each Agent exposes only named operations (below), never "run this command". Writes are Actions on the ddash side and go through the gate and the Audit Log as usual.
- **Version check.** An Agent reports its Release. On a mismatch ddash refuses every operation except "Update Agent": the Docker Agent recreates itself on the new image, and the Host Agent downloads the new binary and restarts.
- **Long-running jobs.** `ActionDeadline` is 60 s (`core/router/router.go`), and pulls, package updates and backups take longer. Each long operation runs as a **job** on the Agent (or as a Proxmox task). The Action returns once the job has started, the way `proxmox`'s power Actions return while `ListPowerTasks` tracks Proxmox's task. A getter lists running jobs with their steps, and the Action's Audit Log entry is completed with the job's outcome when it ends. This extends ADR-0004 and needs its own ADR (see Further Notes).
- **Docker Agent.** Runs as a container with `/var/run/docker.sock` and each chosen Compose root mounted at the **same path** as on the host, so `docker compose` resolves relative bind mounts and `.env` files exactly as on the host. Operations: list Compose Projects (from `com.docker.compose.project`, `…config_files` and `…working_dir` labels, plus Compose files found under the roots with nothing running), read and write a Compose file and its `.env` (write keeps `<file>.bak` and fails if the file's hash differs from the one the Plan read), validate (`docker compose config`), `up`, `down`, `restart`, `pull`, image inspect for digests, and a job's progress.
- **Host Agent.** Installed by `install.sh`, which detects the distribution from `/etc/os-release`, installs the binary to `/usr/local/bin/ddash-host-agent`, and registers a systemd unit (OpenRC on Alpine). Runs as root. Operations: list pending package updates, run a package update (`apt-get`, `dnf`, `apk`, `pacman`, non-interactive), report "reboot required", and SSH: read `sshd` state and settings, enable and start `sshd`, add and remove keys in a user's `authorized_keys` (marked with a `ddash:<key name>` comment), and set `PasswordAuthentication` and `PermitRootLogin` through a drop-in file, applied only after `sshd -t` passes, with a reload (not a restart) so open sessions survive.

### `docker-agent-api` (new Component)

- Config: a list of Docker hosts, each with a name, the Agent's URL, and its pairing state (the token is a write-only secret).
- Getters: `ListComposeProjects`, `GetComposeFile`, `ListJobs`. Actions: `WriteComposeFile`, `ValidateComposeFile`, `UpProject`, `DownProject`, `RestartProject`, `PullProject`.
- `docker-api` keeps reading socket-proxies for hosts without a Docker Agent. For a host with one, `docker-api` reads containers through the Agent and drops the socket-proxy (Open Decision 1).

### `compose` (new Component: the Docker Plugin's logic)

- Requires `docker-agent-api`, `docker-api`, `checks` and `ntfy-api` (for Notifications, until Open Decision 6 picks a channel model).
- Owns Compose Project Actions with Plans, the same pattern as `apps` (`PlanAction`, then a streaming `RunAction` that re-reads and refuses when the Plan no longer holds). Kinds: Save Compose file, Start, Stop, Restart, Update, Add Compose Project.
- **Update**: pull, compare digests per Compose service, and recreate only the changed services with `up -d <services>`. The changed Compose services' Apps go into Maintenance for the run.
- **Image checks**: for each running container, its image's repo digest (from image inspect) is compared with the registry's manifest digest for the same tag, through a `HEAD` request to the registry's manifest endpoint. Checks run every 6 hours and on Refresh. Registry credentials are optional per registry (a secret). A digest-pinned or local image is "not checked".
- **Notifications**: one per container per new digest, collapsed into one message per run when several images turn stale at once.
- **Protected**: Stop, Restart, Update and Save-with-recreate on a Compose Project are refused when any of its Workloads is Protected, unless the Operator confirms an explicit override in the Plan (Open Decision 4).

### `apps`: Add App from a new Compose Project

- Add App gains a source, "New Compose Project". Its Plan gains steps before the existing ones: write the Compose file, `up -d`, wait until the container is running, and then the Proxy Host and Host Override steps from #157.
- `apps` then requires `compose`. The upstream defaults to the Docker host's address and the Compose service's first published port. The new container becomes the App's Workload through the existing matching.
- A failure after the Compose steps leaves the half-done state as Warnings ("App has no Proxy Host", the existing kinds), with nothing rolled back, as ADR-0004 and #158 already do.

### `ai-helper` (new Component) and its UI inside Docker and Apps

- Config: the Anthropic API key (a secret) and the model. The Component calls the Messages API with a fixed system prompt that asks for a Compose file plus Add App fields as structured output, along with the context the Operator saw in the preview: the host's existing Compose Project names, used ports and networks.
- It never reads `.env` contents and never calls Agents. It returns a draft to the browser, which opens it in the Add Compose Project / Add App form for the normal Plan.
- Opt-in: no Plugin requires `ai-helper`. The "Draft with AI" button appears only when it is installed and configured.

### `proxmox`: updates, snapshots, backups, SSH Keys

- `proxmox` additionally requires a new `host-agent-api` Component (Agents by Workload, matched by address and VMID). `proxmox-api` gains the snapshot and backup calls.
- **Why an Agent for LXCs.** Proxmox's API can run a command in a VM through the QEMU guest agent, but it has no equivalent for LXCs. For an LXC, `pct exec` exists only on the node's shell. So package updates and SSH setup go through the Host Agent on both kinds, and nothing needs SSH to the node.
- **ddash Snapshot.** Named `ddash-<UTC timestamp>`, with the description `Created by ddash before <reason>`. Retention: create the new one, wait for its Proxmox task to succeed, then delete every older snapshot whose name starts with `ddash-`. When creating fails, nothing is deleted. Snapshots without the prefix are never touched. A Workload whose storage can't snapshot shows "snapshots not supported" and updates without one only when the Operator turns the snapshot off.
- **Update**: a Plan (pending packages, snapshot yes or no, Maintenance), then a job: snapshot, update, report "reboot required". It never reboots by itself.
- **Update all running**: one Action with one Plan listing every eligible Workload in order, and the skipped ones with their reason (Protected, no Host Agent, stopped). It runs one Workload at a time and stops at the first failure.
- **Backup**: `vzdump` through Proxmox's API to a storage the Operator picks (snapshot mode by default), tracked like a power task.
- **SSH Keys**: stored in `proxmox`'s SQLite as name, public key, fingerprint and created time. Generating a key creates an Ed25519 pair in the Component. The private key is returned in the Action's response exactly once, and is never written to disk or to the Audit Log (secret redaction already covers fields marked secret).
- **Proxmox token**: the deployment docs' role gains `VM.Snapshot`, `VM.Backup` and `Datastore.AllocateSpace` on the backup storage. Protected ACLs stay as they are for power Actions, and Protected Workloads are excluded from bulk updates (Open Decision 4).

### `cloudflare-api` (new Component) and the Cloudflare Tunnel Plugin

- Config: account ID, the Tunnel's ID, and an API token (secret) with Cloudflare Tunnel Edit and DNS Edit on the zone.
- Supports **remotely managed** Tunnels (configured in Cloudflare's dashboard and API) only. A locally managed `config.yml` is out of scope.
- Reads the Tunnel's connections and its ingress configuration. A Public Hostname is one ingress rule plus its proxied `CNAME`. "Disable" removes the ingress rule and DNS record and keeps their settings in ddash's own state, and "Enable" restores both. All writes run through a Plan.
- `apps` shows an "On the internet" fact for an App whose hostname is a Public Hostname.

### `pfsense-api` and `network`: Remote Access peers

- pfSense stores a peer's public key, not its private key. So an existing peer's QR code can't be rebuilt, and ddash can only show the QR code for a peer it creates.
- **Add peer**: ddash generates the key pair, creates the peer on `tun_wg1`'s tunnel with the next free address in the Remote Access subnet, applies, and returns the client config (and QR code) once. The private key is never stored.
- The QR code is rendered in the browser.
- The pfSense user gains the WireGuard peer write privileges, to be confirmed by a research ticket.

### `energy-api` (new Component) and the Energy Plugin

- Reads current values and history from the Operator's database, read-only. Which database and schema is Open Decision 7. The Component's config holds the connection and a mapping from its series to production, consumption, grid import and grid export.
- `@ddash/ui`'s line chart serves the day view. A stacked bar chart for daily or monthly totals is new in the charts family.

### `google-calendar-api` (new Component) and the Calendar Plugin

- Proposed: OAuth with the `calendar.readonly` scope. ddash's Page starts consent and its callback stores the refresh token as a secret. A simpler path, a calendar's secret iCal address, is weighed in Open Decision 8.

### `telegram-api` (new Component)

- Config: bot token (secret) and chat ID. `Send` and `SendTest`, with the same shape as `ntfy-api`.
- Today `checks` requires `ntfy-api` directly, so ntfy is the only way a Notification reaches the Operator. Telegram needs a channel model: one way for any Component to send a Notification to every configured channel (Open Decision 6).

### Glossary additions

- **Agent**: a ddash program, shipped in the Release, that the Operator installs on a host so ddash can change it. Either a **Docker Agent** or a **Host Agent**. _Avoid_: daemon, helper, client.
- **Compose Project**: the containers one Compose file defines, as Docker Compose names them. Its parts are **Compose services**, always qualified. _Avoid_: stack, app (an App is something else).
- **ddash Snapshot**: a Proxmox snapshot ddash created, the only kind its retention deletes. _Avoid_: backup (a backup is a `vzdump` archive).
- **SSH Key**: a named public key ddash stores and installs on Workloads.
- **Public Hostname**: a hostname the Cloudflare Tunnel serves from the internet to a local address. Always say "Cloudflare Tunnel", never bare "tunnel", because the glossary's VPN Tunnel and Tunnelled mean the outbound path.

## Open Decisions (for a Wayfinder map)

1. **Socket-proxy or Docker Agent for reads?** Does a host with a Docker Agent drop its socket-proxy, or do reads stay on the read-only proxy and only writes go through the Agent?
2. **Is one Agent enough?** On a Docker host that is also an LXC, should the Host Agent also cover Docker, rather than run two Agents?
3. **Agent transport.** Agent-serves-HTTPS (ddash connects in, as specified above) or Agent-dials-ddash (no inbound port, survives NAT). The ideas ask for the former, and the latter removes the open port.
4. **Protected Workloads.** Is updating packages, or updating a Compose Project, on a Protected Workload refused, or allowed with a stronger confirmation? Does "Protected" need a sibling term for "may be updated, never restarted"?
5. **Long jobs and the Audit Log.** How a job's outcome amends its Audit Log entry, what "Interrupted" means for a job that keeps running on the Agent while ddash restarts, and whether this is a new ADR or an amendment of ADR-0004.
6. **Notification channels.** One `notify` Component that fans out to ntfy and Telegram, or each Component requiring the channels it uses? Where do per-channel filters live?
7. **Energy data source.** Which database (InfluxDB, PostgreSQL/TimescaleDB, Home Assistant's recorder, the inverter's own API?) and schema. This decides `energy-api` entirely.
8. **Calendar access.** OAuth (needs a Google Cloud project, a consent screen, and a callback through Nginx Proxy Manager) or a secret iCal address (no OAuth, read-only, polled)?
9. **AI helper context.** What may be sent: Compose file text only, or also image names, ports and hostnames? Is the preview story 34 mandatory, or a setting?
10. **Compose Projects without a running container.** Should ddash discover Compose files by scanning the mounted roots, or only by container labels (missing stopped-and-removed projects)?

## Testing Decisions

- **External behaviour only**, as in earlier specs: what an RPC returns and what it wrote to a fake, and what the Operator sees and can click.
- **New seam: `agentfake`**, a stateful fake Docker Agent and Host Agent with pairing, Compose files with hashes, digests, jobs with injectable step failures, package lists, and `sshd` config, which records every request in order.
- **Registry fake** for image checks: tags with digests, 401 and 429 responses.
- **`proxmox-api` fixtures** gain snapshots, a failed snapshot task, and `vzdump`. The retention tests cover: a new snapshot succeeds and the old one goes, a new snapshot fails and the old one stays, and a manual snapshot is never touched.
- **`cloudflarefake`**, **`telegramfake`**, and a fake energy database follow the `pfsensefake` model.
- **The Host Agent's distro paths** run as integration tests in containers of each supported distribution, in CI.
- **Plugins** with vitest against Connect JSON fixtures: Compose editor and diff, Plan Dialogs, image badges, SSH Key "download once", QR shown once, energy charts.
- **`make smoke`** gains read-only Agent and Cloudflare reads. **`make smoke-writes`** gains a throwaway Compose Project, a throwaway LXC snapshot, and a throwaway Public Hostname named in `smoke.env`.

## Out of Scope

- Arbitrary shell or command execution through an Agent.
- Docker Swarm, Kubernetes and Podman.
- Automatic updates on a schedule: every update is an Operator click. Image checks only notify.
- Rolling back a failed update: the ddash Snapshot exists so the Operator can roll back in Proxmox by hand.
- Distribution upgrades (for example Debian 12 to 13): only package updates within a release.
- Rebuilding QR codes for Remote Access peers ddash didn't create.
- Locally managed Cloudflare Tunnels, and Cloudflare Access policies.
- Writing to the energy database or the calendar.
- The AI helper running anything itself, or editing existing Compose Projects.

## Further Notes

- **Conflicts with recorded decisions:**
  - _Contradicts the deployment rule "ddash never gets the Docker socket" (docs/deployment.md, ADR-0001's "reaches Docker only through read-only socket-proxies")._ This is worth reopening because every Docker write needs the socket. Here it goes to the Docker Agent on that host, not to ddash's container, and only where the Operator installs one. It needs a new ADR: "Agents hold root on the hosts that opt in".
  - _Extends ADR-0004._ Actions that outlast `ActionDeadline` become jobs. ADR-0004 rejected background jobs "for Actions that last seconds", and these last minutes.
  - _ADR-0003 moved ddash off SSH._ This spec keeps that: nothing here uses SSH to reach a host. The Host Agent replaces it.
- **Suggested order for implementation issues**: (1) Agents' ADR, pairing and lockstep; (2) Docker Agent with read-only Compose Projects and image checks; (3) Compose Project Actions; (4) Host Agent with Proxmox updates, ddash Snapshots and backups; (5) SSH Keys; (6) Add App from a Compose Project, then the AI helper; (7) Telegram and the channel model; (8) Remote Access peers; (9) Cloudflare Tunnel; (10) Energy and Calendar, once Open Decisions 7 and 8 are settled.
- **Research tickets before implementation**: Proxmox API snapshot and `vzdump` behaviour per storage type; registry `HEAD` behaviour and Docker Hub limits; pfSense REST API v2 WireGuard peer writes; the Cloudflare API for remotely managed ingress; the Operator's energy database.
