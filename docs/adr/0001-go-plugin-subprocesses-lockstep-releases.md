# Components run as go-plugin subprocesses, released in lockstep with the core

ddash's Components run as hashicorp/go-plugin subprocesses inside the ddash container: one process per Component, getters as gRPC services routed through the host, one SQLite file per Component. Plugin UIs load at runtime through an import map. Every Plugin and Component ships in one repo-wide Release, in lockstep with the core image. The image holds only the core, and installing a Plugin downloads its checksum-verified binaries and bundles from that GitHub release into `/data`. We chose this so the operator installs only the Plugins they use (no idle processes or UI for the rest) and a crashing Component can't take the host down, while lockstep releases spare us any compatibility rules between core and Plugin versions.

## Considered Options

- **Compiled in** (all Plugins in the binary, install = enable): lightest, with plain Go getters, but every Plugin ships and runs in every install, and there is no crash isolation. Getters are kept as narrow interfaces either way, so the cost of this choice is the protobuf layer.
- **Container per Component, or build-time install** (`go build` / `docker build` in the install path): both need the Docker socket, which ddash never gets. It reaches Docker only through read-only socket-proxies.
- **WASM (wazero/Extism):** `wasip1` has no sockets and Wasm is single-threaded, so probes, `nginx -t` and pollers would all become host functions.
- **Go's `plugin` package:** needs an identical toolchain and dependency versions, and a plugin can't be unloaded.
- **Per-Plugin versions:** rejected for lockstep. With per-Plugin versions, Component versions could diverge and the gRPC getter contracts would need backward compatibility.

## Consequences

- Updating ddash means changing the image tag in compose. On start, the host brings installed Plugins to its own Release, and the previous Release stays on disk for rollback.
- The migration runner is a shared Go SDK linked into each Component, not a core service. The core only does bookkeeping and snapshots before a migrating update.
- Every Plugin's source is in the repo at Release build, so the shell's Tailwind build scans it and generates every Plugin's utility classes in one order. A Plugin's own stylesheet holds only its prefixed `@keyframes`, never utilities or selectors that could override the shell. Plugins build only from the core's `@ddash/ui` and Theme tokens, and a CI lint enforces this.
- Details: [Plugin runtime: how Components run and Plugin UIs load](https://github.com/itsKontra/ddash/issues/19).
