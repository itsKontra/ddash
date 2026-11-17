# Deploying ddash

ddash runs as one container on its own LXC: the core image
`ghcr.io/itskontra/ddash:<release>`, started by the reference
[`deploy/compose.yaml`](../deploy/compose.yaml) with its bootstrap config in
`ddash.env` (from [`deploy/ddash.env.example`](../deploy/ddash.env.example)).
The image holds only the core. On start, the core downloads its Release's
Plugins and Components from the matching GitHub release into `/data`,
verifies them against `SHA256SUMS` and runs each Component as a subprocess in
the same container, as the same user. ddash's container never gets the
Docker socket: a host ddash should change gets an Agent instead
([ADR-0005](adr/0005-agents-hold-root-on-the-hosts-that-opt-in.md)).

Placeholders: `<domain>` is the home domain, `<ddash-ip>` the ddash LXC,
`<nginx-ip>` the nginx LXC, `<pve-ip>` the Proxmox node, `<docker-env-ip>` and
`<media-stack-ip>` the two other Docker hosts, `<vmid>` a guest's ID. Real
values live only in the local, gitignored name map. Never commit them.

## Launch checklist

Do these by hand, in order, before first use.

### 1. The ddash LXC

- [ ] Create a new **unprivileged** Debian LXC `ddash` on the Proxmox node with
      `features: nesting=1,keyctl=1`, one NIC on the LAN bridge and a static
      address `<ddash-ip>` (VMID as the last octet).
- [ ] DNS: AdGuard first, so App domains resolve inside the container (App
      Checks need that).
- [ ] Install Docker CE (with the compose plugin).
- [ ] While the repo is private: `docker login ghcr.io` with a token that can
      read packages. Not needed once the repo is public.

### 2. compose and ddash.env

- [ ] `mkdir -p /opt/ddash/data && chown 65532:65532 /opt/ddash/data`. The
      container runs as `65532:65532` (distroless `nonroot`), and the
      Component binaries are executed from `/data`, so don't mount it
      `noexec`.
- [ ] Copy `deploy/compose.yaml` to `/opt/ddash/compose.yaml`. Set the image
      tag to the Release you deploy (never `latest`) and `<ddash-ip>`.
- [ ] Copy `deploy/ddash.env.example` to `/opt/ddash/ddash.env`,
      `chmod 600`, and fill in every placeholder. It holds bootstrap config
      only: Component credentials are entered in the UI.
- [ ] `docker compose up -d`, then `docker compose ps` shows `healthy` once
      `/ddash healthcheck` (GET `/healthz`) passes.
- [ ] Outbound HTTPS from the container: to `github.com` (its Release's
      Plugins and Components) and to `cdn.jsdelivr.net` (an App's logo, once
      each, kept in `/data/icons`; the browser never asks a CDN).

### 3. The name and the proxy

- [ ] AdGuard DNS rewrite `ddash.<domain>` → `<nginx-ip>`.
- [ ] nginx Proxy Host for `ddash.<domain>` → `http://<ddash-ip>:8080` with the
      wildcard certificate. It must send `X-Forwarded-For` and
      `X-Forwarded-Proto`; ddash trusts them only from `DDASH_TRUSTED_PROXY`.
- [ ] The Proxy Host compresses what ddash serves: ddash sends its
      JavaScript and CSS uncompressed, and leaves compression to the proxy.
      In Nginx Proxy Manager, add to the Proxy Host's Advanced config:
      `gzip on; gzip_types text/javascript text/css application/json;`.
      Check with
      `curl -sI -H 'Accept-Encoding: gzip' https://ddash.<domain>/assets/<file>.js`
      (signed in): it answers `content-encoding: gzip`.
- [ ] The Proxy Host leaves ddash's pages as they are: the shell's
      Content-Security-Policy refuses any script the proxy injects, and a
      second policy the proxy adds only narrows ddash's.

### 4. Keycloak

- [ ] A confidential OIDC client (`DDASH_OIDC_CLIENT_ID`): client
      authentication on, only the standard flow, PKCE `S256`, web origins
      empty.
- [ ] Valid redirect URI exactly `https://ddash.<domain>/auth/callback`; valid
      post-logout redirect URI `https://ddash.<domain>/auth/logged-out`.
- [ ] A client role `operator`, granted to the Operator, and a client-roles
      mapper that adds `resource_access` to the **ID token**.
- [ ] Put the client secret in `ddash.env` as `DDASH_OIDC_CLIENT_SECRET`.

### 5. Firewall

- [ ] Proxmox datacenter firewall: `enable: 1`. The node's `host.fw` stays
      `enable: 0`.
- [ ] `docker-env` and `media-stack`: `firewall=1` on the LAN NIC, guest
      options `enable: 1` and `policy_in: ACCEPT`, then
      `IN ACCEPT -source <ddash-ip> -p tcp -dport 2375` and
      `IN REJECT -p tcp -dport 2375`.
- [ ] ddash LXC: the same pattern for 8080:
      `IN ACCEPT -source <nginx-ip> -p tcp -dport 8080`, then
      `IN REJECT -p tcp -dport 8080`. Sign-in stays the real boundary; this
      only stops LAN clients from bypassing nginx.

- [ ] A host with a Docker Agent (below): `IN ACCEPT -source <ddash-ip> -p
      tcp -dport 7443`, then `IN REJECT -p tcp -dport 7443`. Its socket-proxy
      and its 2375 rules can go once the Agent is paired: docker-api reads the
      host through the Agent.
- [ ] An LXC or VM with a Host Agent: the same for 7444.

### 6. The systems ddash reaches

- [ ] Docker: on both Docker hosts, run `lscr.io/linuxserver/socket-proxy`
      read-only (`POST=0`, `CONTAINERS=1`) published on the host's LAN
      address, port 2375 (`tcp://<docker-env-ip>:2375`,
      `tcp://<media-stack-ip>:2375`).
- [ ] pfSense: a dedicated `ddash` user holding only these `api-v2-*`
      privileges (System > User Manager > Effective Privileges), then
      generate its API key:
      - reads: `api-v2-status-gateways-get`,
        `api-v2-routing-gateway-groups-get`,
        `api-v2-status-dhcp-server-leases-get`,
        `api-v2-services-dhcp-servers-get`, `api-v2-firewall-aliases-get`,
        `api-v2-status-interfaces-get`,
        `api-v2-status-wireguard-tunnels-get`, `api-v2-firewall-rules-get`,
        `api-v2-services-dns-resolver-host-overrides-get`,
        `api-v2-vpn-wireguard-peers-get`;
      - Adopt and rename: `api-v2-services-dhcp-server-static-mapping-post`,
        `api-v2-services-dhcp-server-static-mapping-patch`;
      - the internet path (Tunnelled, Direct, No Internet):
        `api-v2-firewall-alias-patch`, `api-v2-firewall-apply-post` (a
        move writes both aliases, then applies once);
      - Host Overrides (a batch writes without applying, then applies
        once): `api-v2-services-dns-resolver-host-override-post`,
        `api-v2-services-dns-resolver-host-override-patch`,
        `api-v2-services-dns-resolver-host-override-delete`,
        `api-v2-services-dns-resolver-host-override-alias-post`,
        `api-v2-services-dns-resolver-host-override-alias-delete`,
        `api-v2-services-dns-resolver-apply-post`;
      - Remote Access peers (Add a device writes the peer, then applies
        WireGuard, which reloads the Remote Access tunnel):
        `api-v2-vpn-wireguard-peer-post`, `api-v2-vpn-wireguard-apply-post`;
      - `make smoke-writes` only (its cleanup and pending checks):
        `api-v2-services-dhcp-server-static-mapping-delete`,
        `api-v2-services-dhcp-server-apply-get`,
        `api-v2-services-dhcp-server-apply-post`,
        `api-v2-firewall-apply-get`,
        `api-v2-services-dns-resolver-apply-get`.

      A privilege covers its endpoint for every object: ddash itself keeps
      to the bypass and No Internet aliases and the Devices' static
      mappings.
- [ ] pfSense, for No Internet (optional): a host alias for the No Internet
      Devices and a rule blocking its members from the internet, then pick
      the alias as network's **No Internet alias**. ddash never creates
      either.
- [ ] pfSense, for Add a device (optional): the Remote Access tunnel (a
      WireGuard tunnel assigned to an interface without a gateway, e.g.
      `tun_wg1` on `REMOTEACCESS`) with its interface address and subnet,
      the WAN rule passing its listen port, and the rules on its interface.
      Then, in network's config:
      - **Remote Access endpoint**: the public hostname (or address) a
        device dials, with `:port` when it isn't the tunnel's listen port
        (a port forward). Without it, Add a device is refused.
      - **Remote Access DNS** (optional): the DNS servers a device uses,
        e.g. AdGuard's address; empty keeps the device's own.
      - **Remote Access routes** (optional): what a device sends through
        the tunnel (its `AllowedIPs`). Empty means the subnets of the
        firewall's interfaces without a gateway (LAN, the Remote Access
        subnet, ...), so only home traffic; `0.0.0.0/0` sends everything.

      A device gets the lowest free address of the tunnel's subnet (not the
      firewall's, nor any configured peer's, disabled ones included) and a
      `PersistentKeepalive` of 25 s. ddash generates its key pair and gives
      pfSense the public key only; the config with the private key is shown
      once, as a QR code and as text, and kept nowhere.
- [ ] Proxmox: the role and token, plus the **Protected ACLs** below.
- [ ] Nginx Proxy Manager: a dedicated user for ddash (Users). Permissions:
      Item Visibility All Items, Proxy Hosts Manage, SSL Certificates View,
      everything else Hidden; 2FA off.
- [ ] ntfy (optional, a Notification channel): install the **ntfy**
      Plugin in Plugins; its ntfy-api config form opens. Enter a reserved
      ntfy.sh topic and an access token, and press **Send test**. To stop
      using ntfy, uninstall its Plugin.
- [ ] Cloudflare Tunnel (optional): ddash changes only a **remotely managed**
      Tunnel (created in Zero Trust → Networks → Tunnels, configured in the
      dashboard), never a locally managed `config.yml`. Create an API token
      (My Profile → API Tokens → Create Custom Token) with:
      - Account → **Cloudflare Tunnel: Edit** (reads the Tunnel, its
        connectors and ingress, and PUTs the ingress back whole),
      - Account → **Account Settings: Read** (lists the account's zones),
      - Zone → **DNS: Edit**, on the zone of the Public Hostnames (each
        Public Hostname's proxied CNAME to `<tunnel-id>.cfargotunnel.com`).

      Give cloudflare-api the account ID, the Tunnel's ID and the token.
      Left empty, it stays not set up and Apps show no "On the internet".
      Disabling a Public Hostname keeps its settings in cloudflare-tunnel's
      DB (back it up with the rest of the data directory).
- [ ] Calendar (optional): each calendar's **secret iCal address**. In
      Google Calendar on the web: Settings, the calendar under *Settings
      for my calendars*, *Integrate calendar*, **Secret address in iCal
      format**. Enter them in calendar-api's **Calendars** as
      `Name = address`, separated by `;`, e.g.
      `Family = https://calendar.google.com/calendar/ical/…/private-…/basic.ics; Work = https://…`.
      Any calendar's `.ics` subscription address works; `webcal://` is
      read as `https://`. There is no OAuth: no Google Cloud project or
      consent screen is needed.

      The address alone lets anyone read the whole calendar, without a
      sign-in: treat it as a password. ddash keeps it as a secret (the form
      never shows it again, errors name only its host) and only reads it,
      at most every 5 minutes; it can't change the calendar. If it leaks,
      reset it in the same place in Google Calendar and enter the new one.
      Google refreshes the file every few hours, so a new event can take a
      while to show. ddash needs outbound HTTPS to the calendar's host.
- [ ] Energy (optional): the InfluxDB v2 bucket Telegraf writes the house's
      readings to (the Huawei SUN2000 and its grid meter as measurements
      `kW` and `kWh`, the Victron GX as `victron`). In InfluxDB, *Load
      Data*, *API Tokens*, *Generate API Token*, *Custom API Token*: **Read**
      on that bucket only. Enter the address (e.g.
      `https://influxdb.home.example`), the token and the bucket (default
      `PV`) in energy-api's config, and the grid price and feed-in tariff in
      € per kWh to see costs. ddash only reads: the token can't change a
      reading. ddash needs HTTPS to InfluxDB.
- [ ] Telegram (optional, a Notification channel beside or instead of
      ntfy): install the **Telegram** Plugin in Plugins; its telegram-api
      config form opens.
      1. In Telegram, message **@BotFather**, send `/newbot`, and pick a
         name and a username ending in `bot`. BotFather answers with the
         bot token (`123456789:AAH…`): it is telegram-api's **Bot token**.
      2. Open a chat with the new bot and send it any message (a bot
         can't write to anyone who hasn't). For a group, add the bot to it
         and send `/start@<bot username>` there.
      3. Open `https://api.telegram.org/bot<token>/getUpdates` in a
         browser: `result[].message.chat.id` is the **Chat ID** (your user
         ID for the private chat, a negative number for a group). For a
         channel, make the bot an admin and use its `@username`.
      4. Save both in telegram-api's config form and press **Send test**.

      To stop using Telegram, uninstall its Plugin. With no channel
      installed, no Notification reaches you, and the Checks Page's
      Notifications panel says so; no other Plugin shows down or
      unconfigured for it. A channel whose delivery fails shows down on its
      own Plugin only.
- [ ] AI App helper (optional, opt-in): install the **AI App helper**
      Plugin only if you want Claude to draft new Apps. No other Plugin
      requires it; Docker and Apps show "Draft with AI" only while it is
      installed. Create an API key at console.anthropic.com and enter it as
      anthropic-api's **Anthropic API key**: it is a write-only secret (the form
      never shows it again, the Audit Log redacts it). Drafts are billed to
      that key; the **Model** defaults to `claude-sonnet-5-5`. Left empty,
      the helper is "not set up" and its Page says so. anthropic-api is the
      one Component that talks to Anthropic; ai-helper asks it.

      What is sent, per draft, only when you press **Send to Anthropic**
      after the Page has shown it to you word for word: your description,
      and the chosen Docker host's name, its Compose Project names, the
      host ports already published there, the networks and home domain you
      typed, under ddash's fixed instructions. Never a Compose file's or
      `.env`'s content, and ai-helper never calls a Docker Agent. The draft
      comes back to the browser and opens in Add Compose Project or Add
      App, where the normal Plan checks it; nothing runs on its own.
      ddash needs outbound HTTPS to `api.anthropic.com`.
- [ ] Which Notifications each channel gets: the switches in the
      channel's own config form (ntfy-api's, telegram-api's), one per
      category (Checks, Image updates, Action outcomes), all on by default.
      The Checks Page's Notifications panel lists the installed channels,
      where each delivers to, what it takes and its last problem.
- [ ] Enter every credential in the UI (each Component's config form), not in
      `ddash.env`.

### 7. Proxmox role, token and Protected ACLs

ddash may start, stop and restart a Workload only where Proxmox grants it
`VM.PowerMgmt`. A **Protected** Workload is one reaching ddash depends on; it
gets an audit-only ACL that replaces the inherited one, so Proxmox itself
refuses ddash's power Actions there and ddash shows a "protected in Proxmox"
lock. Token permissions are the intersection of the user's and the token's,
so every ACL goes on **both**.

ddash Snapshots need `VM.Snapshot` and backups `VM.Backup` on the
Workload, plus `Datastore.AllocateSpace` (and `Datastore.Audit`, to list
it) on the storage backups go to. Protected Workloads keep both: Protected
only takes power away, and a single update of one (with its override
ticked) still takes its ddash Snapshot.

```sh
pveum role add DdashOperator --privs "VM.Audit VM.PowerMgmt VM.GuestAgent.Audit VM.Snapshot VM.Backup"
pveum role add DdashAuditor  --privs "VM.Audit VM.GuestAgent.Audit VM.Snapshot VM.Backup"
pveum role add DdashBackup   --privs "Datastore.AllocateSpace Datastore.Audit"
pveum user add ddash@pve --comment "ddash"
pveum user token add ddash@pve dashboard --privsep 1     # prints the secret once
pveum acl modify /vms --users ddash@pve --roles DdashOperator
pveum acl modify /vms --tokens 'ddash@pve!dashboard' --roles DdashOperator

# Once per Protected Workload:
pveum acl modify /vms/<vmid> --users ddash@pve --roles DdashAuditor
pveum acl modify /vms/<vmid> --tokens 'ddash@pve!dashboard' --roles DdashAuditor

# Once per storage that takes backups (e.g. local, pbs):
pveum acl modify /storage/<storage> --users ddash@pve --roles DdashBackup
pveum acl modify /storage/<storage> --tokens 'ddash@pve!dashboard' --roles DdashBackup

# The Hosts Widget's node meters: CPU and memory need Sys.Audit on the
# node, storage needs Datastore.Audit on the storages holding guest disks:
pveum role add DdashNode --privs "Sys.Audit Datastore.Audit"
pveum acl modify /nodes   --users ddash@pve --roles DdashNode
pveum acl modify /nodes   --tokens 'ddash@pve!dashboard' --roles DdashNode
pveum acl modify /storage --users ddash@pve --roles DdashNode
pveum acl modify /storage --tokens 'ddash@pve!dashboard' --roles DdashNode

pveum user token permissions ddash@pve dashboard         # verify
```

An existing role gains the new privileges with `pveum role modify
DdashOperator --privs "…"` (the whole list again).

- [ ] The v1 Protected set: the firewall, AdGuard, nginx and **ddash's own
      LXC**. Keycloak is not Protected (it shares `docker-env` with other
      containers).
- [ ] A Workload created later is controllable unless it gets the same ACL.
- [ ] "Update all running" skips Protected Workloads (and lists them as
      skipped in its Plan). A single update of one needs its override
      ticked in the Plan.

### 8. Smoke targets

`make smoke-writes` round-trips Actions on sacrificial targets only. Create
them once:

- [ ] pfSense: an empty host alias named **`DDASH_SMOKE`** (never the bypass
      alias).
- [ ] Proxmox: a tiny, stopped LXC named **`ddash-smoke`**, controllable by the
      ddash token (not Protected).
- [ ] A free LAN address (no lease, no static mapping, no AdGuard client),
      outside the DHCP pools, as `SMOKE_UNUSED_IP` in the dev machine's
      `smoke.env`.
- [ ] A hostname no Host Override or alias holds, like
      `ddash-smoke.<domain>`, as `SMOKE_HOST_OVERRIDE` in `smoke.env`. The
      round trip creates it as a Host Override with the alias
      `ddash-smoke-alias.<domain>`, and deletes both. Each batch restarts
      Unbound once.
- [ ] A hostname no Proxy Host, Redirection Host or 404 Host holds (e.g.
      `ddash-smoke.<domain>`) as `SMOKE_PROXY_HOST`. It needs no DNS record:
      the round trip only creates and deletes its Proxy Host.
- [ ] With a Cloudflare Tunnel: a hostname in its zone that no ingress rule
      or DNS record holds, as `SMOKE_PUBLIC_HOSTNAME`. The round trip adds
      it as a Public Hostname (ingress rule and proxied CNAME, commented
      `ddash smoke-writes`), edits its service and removes both.

### 9. Docker Agents (optional, per Docker host)

A Docker Agent lets ddash list, edit, start, stop and update the host's
Compose Projects and check its images. It mounts the Docker socket, so it has
**root-equivalent control of its host**: install it only where ddash should
have that.

- [ ] Docker Page, **Docker Agents**, **Add a Docker Agent**: the host's name
      (as docker-api named it, if it had a socket-proxy), its address, and the
      directories holding its Compose Projects (each is mounted at the same
      path, so relative paths and `.env` files resolve as on the host).
- [ ] ddash shows a Compose file with a one-time enrolment code. On the host:
      `mkdir -p /opt/ddash-docker-agent && cd /opt/ddash-docker-agent`, save
      it as `compose.yaml`, `docker compose up -d`. ddash pairs with it within
      10 s and pins its certificate.
- [ ] Mark what reaching ddash depends on as **Protected** in its Compose
      file: `labels: { ddash.protected: "true" }` (the Reverse Proxy,
      Keycloak, ddash itself). ddash then stops, restarts or recreates it only
      with an explicit override.
- [ ] Image checks read each running container's image digest from its
      registry every 6 hours, with one manifest `HEAD` per distinct image
      (Docker Hub doesn't count those against its pull limit). Private images
      need registry-api's **Registry credentials**; a pull-through mirror goes
      in its **Docker Hub registry**.
- [ ] Removing an Agent from ddash forgets its token; ddash then shows how to
      uninstall it: `cd /opt/ddash-docker-agent && docker compose down -v`.
- [ ] An Agent from another Release is flagged; **Update Agent** recreates it
      on this Release's image.
- [ ] The Agent's own Compose Project (`ddash-docker-agent`) is listed as the
      Docker Agent, without Restart, Stop, Pull & update or its Compose file:
      its Page says it is updated with **Update Agent**, and offers it while
      the Agent runs another Release.

### 10. Host Agents (optional, per LXC or VM)

A Host Agent lets ddash update a Workload's packages and set up SSH on it,
on Ubuntu, Debian, Alpine, Fedora and Arch Linux (amd64, arm64). It runs as
root and has **root-equivalent control of its host**: install one only
where ddash should have that. It listens on port 7444 (HTTPS, a
self-signed certificate ddash pins at pairing) and accepts only the ddash
it paired with. It never runs arbitrary commands, and it never reboots.

- [ ] On the Workload's Page in the Proxmox Plugin, **Host Agent**: check
      the URL (`https://<the Workload's address>:7444`) and add it. ddash
      shows a one-time enrolment code in an install command:

      ```sh
      curl -fsSL https://ddash.<domain>/agent/host/install.sh | sudo sh -s -- --code <enrolment code>
      ```

      Read `install.sh` first if you like (the Panel links it): it detects
      the distribution, downloads `/agent/host/<arch>` from ddash, checks
      it against the SHA-256 it pins (the same as `/agent/host/SHA256SUMS`),
      installs `/usr/local/bin/ddash-host-agent` and starts it as a
      systemd unit (OpenRC on Alpine). ddash pairs with it within 10 s.
- [ ] Allow ddash's LXC to reach port 7444 on that Workload (the Workload's
      own firewall, if any).
- [ ] An Agent from an older Release is flagged; **Update Agent** (the only
      Action it then allows) has it download this Release's binary from
      ddash, check its SHA-256 and restart. ddash's public URL must be
      reachable from the Workload for this and for `install.sh`.
- [ ] Reinstalled or reset an Agent? Its certificate changed: press **Pair
      again** for a new code and run the install command again.
- [ ] To remove one: **Remove** in the Panel, then on the Workload, as root:

      ```sh
      curl -fsSL https://ddash.<domain>/agent/host/install.sh | sudo sh -s -- --uninstall
      ```

      SSH Keys ddash installed (marked `ddash:<name>` in
      `authorized_keys`) stay; remove them first in the SSH Panel if they
      should go.

Updates take a **ddash Snapshot** first (`ddash-<UTC time>`, "Created by
ddash before an update") and then delete every older snapshot named
`ddash-…` of that Workload, only once the new one succeeded; snapshots
without the prefix are never touched. On storage that can't snapshot (LVM
thick, a raw image on a directory storage) turn the Workload's "Snapshot
before each update" off, or turn it off for one run. SSH Keys generated in
ddash hand out their private key once, in the browser; ddash stores public
keys only.

## Updating

Updates are by hand; ddash never updates itself and only shows a newer Release
in the Catalogue. Bump the image tag in `compose.yaml`, then:

```sh
docker compose pull && docker compose up -d
```

The core downloads the new Release's Plugins and Components and snapshots
each Component's DB before its first run on the new Release. Rolling back is
the same with the older tag; moving back restores the snapshot.

After v0.0.3, Notification channels are Plugins of their own (**ntfy**,
**Telegram**). Updating from v0.0.3 or earlier, the first start installs
the Plugin of each channel that was set up (ntfy with a topic, Telegram with
a bot token and a chat ID), with the switches notify's config had for it, so
Notifications keep arriving. A channel that wasn't set up isn't installed.
There is nothing to do; Plugins and the Checks Page's Notifications panel
show the result.

## Backup

A PBS vzdump of the ddash LXC in snapshot mode. SQLite runs in WAL mode, so a
crash-consistent snapshot is fine. ddash has no backup feature of its own.
