# Deploying ddash

ddash runs as one container on its own LXC: the core image
`ghcr.io/itskontra/ddash:<release>`, started by the reference
[`deploy/compose.yaml`](../deploy/compose.yaml) with its bootstrap config in
`ddash.env` (from [`deploy/ddash.env.example`](../deploy/ddash.env.example)).
The image holds only the core. On start, the core downloads its Release's
Plugins and Components from the matching GitHub release into `/data`,
verifies them against `SHA256SUMS` and runs each Component as a subprocess in
the same container, as the same user. ddash never gets the Docker socket.

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

### 3. The name and the proxy

- [ ] AdGuard DNS rewrite `ddash.<domain>` → `<nginx-ip>`.
- [ ] nginx Proxy Host for `ddash.<domain>` → `http://<ddash-ip>:8080` with the
      wildcard certificate. It must send `X-Forwarded-For` and
      `X-Forwarded-Proto`; ddash trusts them only from `DDASH_TRUSTED_PROXY`.

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
- [ ] ntfy: a reserved ntfy.sh topic and an access token.
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
- [ ] Telegram (optional, a second Notification channel beside ntfy):
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

      Left empty, telegram-api is "not set up" and Notifications go to
      ntfy only; ntfy-api unconfigured likewise sends to Telegram only.
- [ ] AI App helper (optional, opt-in): install the **AI App helper**
      Plugin only if you want Claude to draft new Apps. No other Plugin
      requires it; Docker and Apps show "Draft with AI" only while it is
      installed. Create an API key at console.anthropic.com and enter it as
      ai-helper's **Anthropic API key**: it is a write-only secret (the form
      never shows it again, the Audit Log redacts it). Drafts are billed to
      that key; the **Model** defaults to `claude-sonnet-5-5`. Left empty,
      ai-helper is "not set up" and its Page says so.

      What is sent, per draft, only when you press **Send to Anthropic**
      after the Page has shown it to you word for word: your description,
      and the chosen Docker host's name, its Compose Project names, the
      host ports already published there, the networks and home domain you
      typed, under ddash's fixed instructions. Never a Compose file's or
      `.env`'s content, and ai-helper never calls a Docker Agent. The draft
      comes back to the browser and opens in Add Compose Project or Add
      App, where the normal Plan checks it; nothing runs on its own.
      ddash needs outbound HTTPS to `api.anthropic.com`.
- [ ] Which Notifications each channel gets: notify's config form, one
      switch per channel and category (Checks, image updates, Action
      outcomes). Everything goes everywhere by default. The Checks Page's
      Notifications panel shows which channels are set up and take Checks.
- [ ] Enter every credential in the UI (each Component's config form), not in
      `ddash.env`.

### 7. Proxmox role, token and Protected ACLs

ddash may start, stop and restart a Workload only where Proxmox grants it
`VM.PowerMgmt`. A **Protected** Workload is one reaching ddash depends on; it
gets an audit-only ACL that replaces the inherited one, so Proxmox itself
refuses ddash's power Actions there and ddash shows a "protected in Proxmox"
lock. Token permissions are the intersection of the user's and the token's,
so every ACL goes on **both**.

```sh
pveum role add DdashOperator --privs "VM.Audit VM.PowerMgmt VM.GuestAgent.Audit"
pveum role add DdashAuditor  --privs "VM.Audit VM.GuestAgent.Audit"
pveum user add ddash@pve --comment "ddash"
pveum user token add ddash@pve dashboard --privsep 1     # prints the secret once
pveum acl modify /vms --users ddash@pve --roles DdashOperator
pveum acl modify /vms --tokens 'ddash@pve!dashboard' --roles DdashOperator

# Once per Protected Workload:
pveum acl modify /vms/<vmid> --users ddash@pve --roles DdashAuditor
pveum acl modify /vms/<vmid> --tokens 'ddash@pve!dashboard' --roles DdashAuditor

pveum user token permissions ddash@pve dashboard         # verify
```

- [ ] The v1 Protected set: the firewall, AdGuard, nginx and **ddash's own
      LXC**. Keycloak is not Protected (it shares `docker-env` with other
      containers).
- [ ] A Workload created later is controllable unless it gets the same ACL.

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

## Updating

Updates are by hand; ddash never updates itself and only shows a newer Release
in the Catalogue. Bump the image tag in `compose.yaml`, then:

```sh
docker compose pull && docker compose up -d
```

The core downloads the new Release's Plugins and Components and snapshots
each Component's DB before its first run on the new Release. Rolling back is
the same with the older tag; moving back restores the snapshot.

## Backup

A PBS vzdump of the ddash LXC in snapshot mode. SQLite runs in WAL mode, so a
crash-consistent snapshot is fine. ddash has no backup feature of its own.
