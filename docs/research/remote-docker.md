# Reading Docker state on remote Docker hosts

Research for [#5](https://github.com/itsKontra/ddash/issues/5). Researched 2026-10-19.

**Question.** ddash runs on one Docker host LXC. Two more Docker hosts run in other LXCs on the same Proxmox node, on a trusted LAN. What are the options for read access to their containers and their status, and what does each cost in security and setup? Which needs the least configuration?

## Answer in brief

Run **`linuxserver/socket-proxy`** as a small compose service on each of the two remote hosts, read-only (`POST=0`) and allowing only `CONTAINERS`, `EVENTS`, `PING` and `VERSION`. Publish it on the host's LAN IP, port 2375, and firewall that port so only ddash's host can reach it. ddash uses the official Go client with `WithHost("tcp://<host>:2375")`, one client per host. Use the same proxy for ddash's own host (`tcp://socket-proxy:2375` on the compose network) so every host goes through one code path.

Per host that is one compose service, plus one line in ddash's YAML (`tcp://docker2.lan:2375`). There are no certificates, keys, users or daemon changes. Use linuxserver's image, not Tecnativa's, because of the archive/export hole described below.

## What ddash needs from each host

- **List containers with state:** `GET /containers/json?all=true`, which is what `docker ps` calls. It supports filters such as `health=` and `status=` ([Engine API v1.51 spec][api]).
- **Live updates:** `GET /events` streams real-time events. Containers report `start`, `stop`, `die`, `health_status`, `restart` and others ([api]).
- **Optionally** inspect (`GET /containers/{id}/json`), `/_ping` and `/version`.

All of these are GET requests. The Engine API itself has no authentication: its "Authentication" section covers registry credentials only, which are "handled client side" ([api]). So the transport is the only access control. Whoever reaches the endpoint has whatever the endpoint allows.

## Options

### 1. Docker socket proxy over plain TCP (recommended)

An HAProxy container mounts `/var/run/docker.sock` and forwards only the API path prefixes enabled by environment variables. Anything else gets `403 Forbidden` ([Tecnativa README][tec]).

- **Read-only:** with `POST=0`, only GET and HEAD are allowed. Tecnativa enforces this as `http-request deny unless METH_GET || { env(POST) -m bool }` ([Tecnativa haproxy.cfg][teccfg]; [linuxserver README][lsio]).
- **Defaults:** `EVENTS`, `PING` and `VERSION` are on. `CONTAINERS`, `INFO`, `EXEC`, `IMAGES`, `POST` and the rest are off ([tec]). ddash only needs to add `CONTAINERS=1`.
- **Tecnativa vs linuxserver.** linuxserver's image is "conceptually based on" Tecnativa's and is "designed to act as a drop-in replacement" ([lsio]). The difference that matters here:
  - In **Tecnativa's** image, `CONTAINERS=1` allows every path that starts with `/containers` ([teccfg], line 60). That includes `GET /containers/{id}/archive` and `GET /containers/{id}/export`, which download files or the whole filesystem of any container, and `/logs`.
  - **linuxserver's** image denies `/archive`, `/export`, `/logs`, `/top` and `/changes` unless `ALLOW_ARCHIVE`, `ALLOW_EXPORT`, `ALLOW_LOGS`, `ALLOW_TOP` or `ALLOW_CHANGES` is set, and all of them default to `0` ([linuxserver haproxy.cfg][lsiocfg]; [lsio]). It also runs with a read-only socket mount (`docker.sock:ro`), a `--read-only` root filesystem and a tmpfs `/run` ([lsio]).
- **Security.** There is no TLS and no authentication: it is plain HTTP to the socket ([tec]). Both READMEs say never to expose the port to a public network. linuxserver says to treat it like the Docker socket itself ([tec], [lsio]). With read-only settings, a LAN peer can list containers and inspect them. Inspect output includes each container's configured environment, so env-var secrets are readable. That leak is why the port should be firewalled to ddash's IP. It cannot start, exec or mount anything.
- **Later control.** linuxserver has `ALLOW_START`, `ALLOW_STOP` and `ALLOW_RESTARTS`, which "work even with `POST=0`" ([lsio]). If ddash later needs to restart containers, flip one env var. In Tecnativa's image the `POST` deny rule comes before those allows ([teccfg], lines 48-52), so they are not usable read-only there.
- **Setup:** one compose service per host, plus a firewall rule (Proxmox guest firewall or nftables in the LXC) allowing 2375/tcp only from ddash's host.

```yaml
services:
  socket-proxy:
    image: lscr.io/linuxserver/socket-proxy:latest
    environment:
      - CONTAINERS=1   # EVENTS, PING, VERSION already default to 1; POST defaults to 0
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    read_only: true
    tmpfs:
      - /run
    ports:
      - "<lan-ip>:2375:2375"
    restart: unless-stopped
```

### 2. Docker daemon on TCP with mutual TLS

This configures `dockerd` itself to listen on `tcp://0.0.0.0:2376` with `--tlsverify`. Docker's docs list the steps: create a CA; issue a server certificate with the host's DNS name and IPs in `subjectAltName`; issue a client certificate; set key permissions; start the daemon with `--tlscacert/--tlscert/--tlskey` ([Protect the daemon socket][protect]). Daemon listeners go in either the systemd unit or `daemon.json`. Setting both "causes a conflict that prevents Docker from starting" ([Remote access][remote]).

- **Security:** strong transport auth, but **all or nothing**. The docs warn that "anyone with the keys can give any instructions to your Docker daemon" and say to "guard these keys as you would a root password" ([protect]). TLS cannot make the daemon read-only. ddash would hold root-equivalent keys for three hosts. Plain TCP without TLS is worse: "remote non-root users [can] gain root access on the host", and it "will require explicit opt-in in a future release" ([remote]).
- **Setup:** the most of any option. You run a CA, issue per-host certificates, rotate them, change daemon config on every host and restart dockerd. The docs call it "an advanced topic" ([protect]).

### 3. SSH (`ssh://user@host`)

Docker supports `DOCKER_HOST=ssh://USER@HOST`, and the user "must have permissions to access the docker socket on the remote machine" ([protect]).

- **How it works in Go:** the Go client does not handle `ssh://` by itself. The CLI's helper `github.com/docker/cli/cli/connhelper` runs the **local `ssh` binary** with `docker system dial-stdio` as the remote command ([connhelper.go][connh]). That needs `ssh` inside the ddash image and the Docker CLI on each remote host, and it pulls in `docker/cli` as a dependency. ("ssh://... requires Docker 18.09 or later on the remote host" ([connhelper pkg][connhpkg]).)
  - Alternative: `golang.org/x/crypto/ssh` `Client.Dial("unix", "/var/run/docker.sock")` opens a StreamLocal channel to the remote socket ([x/crypto ssh tcpip.go][xssh]). sshd allows this by default (`AllowStreamLocalForwarding` "yes (the default)" ([sshd_config][sshd])). Pass that dialer to the Docker client with `WithDialContext`. This needs no external binaries, but it is custom code.
- **Security:** strong auth and encryption, but **root-equivalent**: a user in the `docker` group can do anything the socket allows. Nothing makes it read-only.
- **Setup:** a user with docker socket access on each host, an SSH key mounted into ddash, and `known_hosts` management. That is moderate, and it puts a root-equivalent key inside a web-facing app.

### 4. A tiny custom agent

You could write a small Go binary per host that reads the local socket and serves a narrow JSON API.

- **Security:** as narrow as you design it, but auth and transport are yours to build. On a trusted LAN that is roughly what socket-proxy already gives.
- **Setup:** the most code. It is a second artifact to build, version, image and deploy to every host, and it has to be kept in step with ddash. It does nothing socket-proxy cannot do for read-only listing and events.

### 5. "The Docker Go SDK against each"

This is not a transport. It is the client used with any of options 1 to 3. The official client is now `github.com/moby/moby/client`. Its README example uses `client.New(client.FromEnv, ...)`, with "API-version negotiation ... enabled by default" ([moby client README][moby]). The older import path `github.com/docker/docker/client` offers `NewClientWithOpts`, `FromEnv`, `WithHost` and `WithDialContext` ([docker client pkg][dockerpkg]). For option 1 the client needs only `WithHost("tcp://host:2375")`. Both proxies list supported API versions up to 1.51 ([tec]).

## Comparison

| Option | Per-host setup | Read-only possible | Credential ddash holds | Extra in ddash image |
|---|---|---|---|---|
| socket-proxy (linuxserver) | 1 compose service + firewall rule | Yes (`POST=0`, archive/export/logs denied) | None | Nothing |
| TLS daemon | CA, certs, daemon config, restart | No | Root-equivalent client cert | Certs |
| SSH | User, key, known_hosts | No | Root-equivalent SSH key | `ssh` binary (or custom dialer) |
| Custom agent | Deploy and maintain a binary | Yes (by design) | Whatever you build | Nothing |

## Recommendation

Use **linuxserver/socket-proxy, read-only, on every Docker host**, including ddash's own host through a sidecar on the compose network. ddash then mounts no socket at all.

- It needs the **least configuration**: one compose service per host and one `tcp://` URL per host in ddash's YAML. No certificates, keys or daemon edits.
- It is the **only off-the-shelf option that is actually least-privilege**. TLS and SSH both hand ddash root-equivalent access to three hosts, which matters because ddash may later be reachable through the Cloudflare Tunnel (see #1).
- The firewall rule (2375/tcp from ddash's IP only) is the one piece of hardening that matters. Without it, any LAN device can read container env vars.
- If ddash later needs start, stop or restart, enable `ALLOW_RESTARTS`, `ALLOW_START` or `ALLOW_STOP` while leaving `POST=0`. Do not switch to TLS or SSH for that.
- Treat each Docker host in config as just a URL. The repository implementation is one Docker repository per host built on `moby/moby/client`. A local or remote distinction is unnecessary.

[api]: https://docs.docker.com/reference/api/engine/version/v1.51.yaml
[tec]: https://github.com/Tecnativa/docker-socket-proxy/blob/master/README.md
[teccfg]: https://github.com/Tecnativa/docker-socket-proxy/blob/master/haproxy.cfg
[lsio]: https://github.com/linuxserver/docker-socket-proxy/blob/main/README.md
[lsiocfg]: https://github.com/linuxserver/docker-socket-proxy/blob/main/root/templates/haproxy.cfg
[protect]: https://docs.docker.com/engine/security/protect-access/
[remote]: https://docs.docker.com/engine/daemon/remote-access/
[connh]: https://github.com/docker/cli/blob/master/cli/connhelper/connhelper.go
[connhpkg]: https://pkg.go.dev/github.com/docker/cli/cli/connhelper
[xssh]: https://github.com/golang/crypto/blob/master/ssh/tcpip.go
[sshd]: https://man.openbsd.org/sshd_config#AllowStreamLocalForwarding
[moby]: https://github.com/moby/moby/blob/master/client/README.md
[dockerpkg]: https://pkg.go.dev/github.com/docker/docker/client
