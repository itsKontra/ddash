# Agents hold root on the hosts that opt in

ddash changes what runs on a host through an **Agent** the Operator installs there, never through SSH or a Docker socket of its own: the **Docker Agent** (a container with the host's Docker socket and its Compose roots mounted at the same paths) and the **Host Agent** (a root service on an LXC or VM, for package updates and SSH setup). Both ship in every Release, run the core's Release, serve HTTPS with a self-signed certificate, and accept only the ddash they paired with: ddash creates a one-time enrolment code, the Operator hands it to the Agent at install, ddash presents it on first contact with a token both keep, and pins the Agent's certificate fingerprint (trust on first use). Each Agent exposes only named operations (list, read and write a Compose file, `up`, `down`, `restart`, `pull`, update packages, install an SSH Key, …), never "run this command". We chose this because every write the ideas ask for needs root-equivalent power on the host, and putting that power in a small, versioned program the Operator installs per host keeps ddash's own container without it and makes the grant visible and revocable.

## Considered Options

- **The Docker socket for ddash's container** (read-write socket-proxies): every Docker host's root in one place, and a Compose file's text isn't in the Docker API anyway.
- **SSH with a forced-command key per host**: what IDEAS.md first chose; it brings back the SSH ADR-0003 removed, the parsing of shell output, and a key on disk that can do anything its command can.
- **Agent dials ddash** (no inbound port, survives NAT): Open Decision 3 kept ddash dialling in, as the ideas asked; the Agent's port is firewalled to ddash's address like the socket-proxies' were.
- **One Agent for both** (the Host Agent also driving Docker on a Docker LXC): kept apart, so a Docker host needs no root service besides Docker and a host without Docker no socket.

## Consequences

- _Contradicts the deployment rule "ddash never gets the Docker socket" and ADR-0001's "reaches Docker only through read-only socket-proxies"_: ddash's container still never mounts the socket, but a host with a Docker Agent is read and changed through it, and docker-api drops that host's socket-proxy (Open Decision 1).
- Lockstep (ADR-0001) extends to the Agents: the Docker Agent's image is tagged with the Release and pinned by digest in the Compose file ddash generates; the Host Agent's binaries are Release assets, served by ddash at `/agent/host/<arch>` with `install.sh`. An Agent on another Release is refused everything but Update Agent.
- A Compose Project with a container labelled `ddash.protected=true` is Protected: Stop, Restart, Update and a save that recreates need an explicit override in the Plan. Bulk package updates skip Protected Workloads.
- Removing an Agent from ddash forgets its token, so ddash can no longer reach it; the Operator uninstalls it from the host with the commands ddash shows.
- Details: SPEC.md (Agents, Compose Projects, LXC updates), `agent/` and the `docker-agent-api` and `host-agent-api` Components.
