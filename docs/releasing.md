# Cutting a Release

A Release is one tag `vMAJOR.MINOR.PATCH` (a `-prerelease` suffix makes a
GitHub prerelease, which the core's newer-Release check skips). Pushing the
tag runs [`.github/workflows/release.yml`](../.github/workflows/release.yml),
which produces one artifact set:

- the core image `ghcr.io/itskontra/ddash:<tag>` (distroless/static, uid
  65532, port 8080, `ddash healthcheck` for `/healthz`);
- one GitHub release `<tag>` holding one file per Component and per Plugin,
  plus `catalogue.json`, `SHA256SUMS` and the Host Agent's two binaries
  (`ddash-host-agent-linux-amd64` and `-arm64`, which `install.sh` downloads
  as they are):
  - `<component>.ct`: a zip holding the Component's binary `<component>`
    (static `linux/amd64`, CGO off, stamped with the tag);
  - `<plugin>.ddapp`: a zip of the Plugin's built `dist/` (`index.js`,
    `index.css` and whatever else Vite emitted).

## Steps

1. Start from a green `main`: CI passed on the commit you'll tag.
2. On a dev machine on the LAN, with a filled-in, gitignored `smoke.env`
   (copy [`smoke.env.example`](../smoke.env.example)), run

   ```sh
   make smoke
   ```

   It calls every system Component's getters against the real systems and
   prints `ok <component> <getter>` or `FAIL …` per line. A Component that
   offers a setup hint fails with it until you've done what it says; its
   DB is kept in `.smoke/` between runs.

   **Cut the Release only if it passes.** If it fails, fix the Component (and
   re-record its fixtures, one PR) first. `make smoke` never runs in CI.

   Docker is read the way the lab reads it: each host with a socket-proxy
   from `SMOKE_DOCKER_API`, each host with a Docker Agent from
   `SMOKE_DOCKER_AGENT_API`, or both. With neither, it is skipped. Every
   host must answer and run a container. For the Agents:

   - An Agent accepts only the token of its one pairing, so the smoke run
     borrows the deployed ddash's. It only reads, never pairs, and never
     prints the token, so the deployed ddash's pairing stays as it was.
     The token is not read-only, though: it gives whoever holds it ddash's
     control of the host's Docker, so keep `smoke.env` at `chmod 600` on
     the dev machine. Copy every paired Agent's name, URL, token and
     certificate fingerprint from the deployed ddash's DB into `smoke.env`
     in one step, so the token never shows on screen:

     ```sh
     ssh root@<ddash-ip> sqlite3 -readonly /opt/ddash/data/db/docker-agent-api.db >> smoke.env <<'SQL'
     SELECT 'SMOKE_DOCKER_AGENT_API=''' || json_object('agents', json_group_array(json_object('name', name, 'url', url, 'token', token, 'fingerprint', fingerprint))) || ''''
     FROM agent WHERE token != '';
     SQL
     ```

     (`apt install sqlite3` on the LXC first, if it lacks it; a later line
     in `smoke.env` replaces an earlier one.) The Docker
     Page lists the same Agents with their fingerprints. Copy them again
     after an Agent is added, removed or paired again: an old token fails
     as `refuses ddash's token`, an old fingerprint as `certificate isn't
     the one pinned`.
   - The dev machine must reach each Agent's port 7443. The firewall lets
     in only `<ddash-ip>` ([deployment](deployment.md#5-firewall)): add an
     `IN ACCEPT` rule for the dev machine's address above the `REJECT`.
   - The Agents run the deployed Release, not the one you're about to cut,
     so the smoke run reads them as that Release (it prints `docker-agent-api
     runs as …`). It checks what the Agents read from Docker, not this
     build's Agent: that one runs after **Update Agent**.
   - `make record` doesn't record the Agents: docker-api's fixtures come
     from a socket-proxy host only.

   To re-record, fill in the `SCRUB_*` lines of `smoke.env` and a gitignored
   `scrub.map` (every other real name, `real = replacement` per line), then
   run `make record` (`SCENARIO=recorded` by default). It writes each answer,
   scrubbed, into `components/<name>/testdata/$(SCENARIO)/` and refuses any
   file that still holds a `scrub.map` name, the real domain or an address
   outside the scrubbed ranges. Review the files and move what the tests need
   into `baseline`; the IP guard in `go test` checks them again.
3. If this Release changes an Action, also run

   ```sh
   make smoke-writes
   ```

   and cut the Release only if it passes too. It needs the smoke targets
   ([deployment](deployment.md#8-smoke-targets)) and `SMOKE_UNUSED_IP` in
   `smoke.env`, and runs the real Actions as reversible round trips, touching
   nothing else:

   - pfSense: adds a placeholder address (`203.0.113.63`) to `DDASH_SMOKE`,
     checks it is applied and reads it back, removes it and checks again;
     creates a static map for the fake MAC `02:dd:a5:00:00:63` on
     `SMOKE_UNUSED_IP`, renames it, then deletes it with a raw API call;
   - AdGuard: `SetClientFiltering` on `SMOKE_UNUSED_IP` (creates a client
     `ddash-smoke` with filtering off, then toggles it on), then deletes the
     client with a raw API call;
   - Proxmox: starts `ddash-smoke`, waits for the task, shuts it down
     gracefully; then tries to start a running Protected Workload and
     passes only if Proxmox refuses the token (were it not refused, the
     Workload is running already, so nothing changes);
   - Nginx Proxy Manager: creates a Proxy Host for `SMOKE_PROXY_HOST`
     forwarding to `SMOKE_UNUSED_IP`, changes its port, then deletes it.
     Cleanup deletes only a Proxy Host with that one hostname forwarding to
     that address.

   Each round trip's cleanup runs after it whether it passed or not (and on
   Ctrl-C), and before it, removing what an interrupted run left. It refuses
   to start when `SMOKE_UNUSED_IP` has a lease, a static map or an AdGuard
   client, when a Proxy Host holds `SMOKE_PROXY_HOST`, or when `ddash-smoke`
   isn't stopped. Each line is `ok …`,
   `FAIL …` or `cleanup …`.
4. Write the Release's notes: move what [`release-notes.md`](release-notes.md)
   lists under **Unreleased** under a heading for the tag, and paste it at
   the top of the GitHub release once the workflow created it.
5. Tag and push:

   ```sh
   git tag -a v1.2.3 -m v1.2.3
   git push origin v1.2.3
   ```

6. Watch the Release workflow. It runs `make check`, builds the Release dir
   (`make release-dir RELEASE=<tag> PLATFORM=linux/amd64`) and the image
   (`make image RELEASE=<tag>`), pushes the image, then creates the GitHub
   release. A failed run can be re-run as a whole until the GitHub release
   exists; the image push is idempotent.
7. Only once the GitHub release is there, deploy it: bump the image tag in
   the LXC's `compose.yaml` and `docker compose pull && docker compose up -d`
   ([deployment](deployment.md#updating)).

The first push creates the GHCR package private: once the repo is public, make
the package public too (Package settings), so the host pulls without a login.

## Building a Release locally

The same make targets build everything without publishing anything:

```sh
make release-dir RELEASE=v0.0.0-local PLATFORM=linux/amd64   # dist/
make image RELEASE=v0.0.0-local                                # the image (DOCKER=podman works)
docker run --rm ghcr.io/itskontra/ddash:v0.0.0-local version  # prints v0.0.0-local
```

To run that image against the local Release dir instead of GitHub, mount
`dist/` and set `DDASH_RELEASE_DIR` to it (the dir must be readable by uid
65532; it may be read-only, since the Component binaries are unpacked into
`/data/release-dir/`).
