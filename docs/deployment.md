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
- [ ] nginx: paste the key line ddash shows (for `nginx -T` only, restricted
      to `<ddash-ip>`) into root's `authorized_keys` on the nginx LXC.
- [ ] pfSense: a dedicated `ddash` user holding only these `api-v2-*`
      privileges (System > User Manager > Effective Privileges), then
      generate its API key:
      - reads: `api-v2-status-gateways-get`,
        `api-v2-routing-gateway-groups-get`,
        `api-v2-status-dhcp-server-leases-get`,
        `api-v2-services-dhcp-servers-get`, `api-v2-firewall-aliases-get`,
        `api-v2-status-interfaces-get`,
        `api-v2-status-wireguard-tunnels-get`, `api-v2-firewall-rules-get`;
      - Adopt and rename: `api-v2-services-dhcp-server-static-mapping-post`,
        `api-v2-services-dhcp-server-static-mapping-patch`;
      - the internet path (Tunnelled, Direct, No Internet):
        `api-v2-firewall-alias-patch`, `api-v2-firewall-apply-post` (a
        move writes both aliases, then applies once);
      - `make smoke-writes` only (its cleanup and pending checks):
        `api-v2-services-dhcp-server-static-mapping-delete`,
        `api-v2-services-dhcp-server-apply-get`,
        `api-v2-services-dhcp-server-apply-post`,
        `api-v2-firewall-apply-get`.

      A privilege covers its endpoint for every object: ddash itself keeps
      to the bypass and No Internet aliases and the Devices' static
      mappings.
- [ ] pfSense, for No Internet (optional): a host alias for the No Internet
      Devices and a rule blocking its members from the internet, then pick
      the alias as network's **No Internet alias**. ddash never creates
      either.
- [ ] Proxmox: the role and token, plus the **Protected ACLs** below.
- [ ] ntfy: a reserved ntfy.sh topic and an access token.
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
