# Plugin runtime: how Components run and Plugin UIs load

Research for [#18](https://github.com/itsKontra/ddash/issues/18). Researched 2026-10-19 against primary sources (official docs, source repos, package registries). The decision belongs to [#19](https://github.com/itsKontra/ddash/issues/19); this doc only lays out the options and their costs.

Framing from the map ([#1](https://github.com/itsKontra/ddash/issues/1)): a Go **core host** plus first-party **Plugins** (Widgets, Nav Entries, Pages; React + TS + Vite + shadcn/ui) and **Components** (single-purpose Go backend units with setup/health/config functions). Each table is defined and used by exactly one Component; others reach its data through that Component's getters. All code lives in this repo (`plugins/<name>/`, `components/<name>/`) and arrives by PR, so the trust model is first-party only. Deployment is docker compose on one Docker-host LXC. The operator leans toward (a), out of process.

Versions current on 2026-10-19: hashicorp/go-plugin v1.8.0 (2026-04-29), wazero v1.12.0 (2026-05-28), extism/go-sdk v1.7.1 (2025-03-02), Vite 8.3.4, @module-federation/vite 1.23.5, React 19.3.1, react-router 8.5.0 (from proxy.golang.org and registry.npmjs.org).

## Answer in brief

- **(a) Out of process is proven prior art.** hashicorp/go-plugin runs each plugin as a host-launched subprocess over gRPC; Grafana's backend plugins work this way. It isolates crashes and allows per-Component update, but the host has to supervise processes, route getter calls, and ship a binary per Component. Running one *container* per Component instead needs either the Docker socket (root-equivalent) or an operator-driven compose change for every install.
- **(b) WASM does not fit Components today.** `GOOS=wasip1` has no socket API, and Wasm is single-threaded. ICMP/TCP/DNS probes, `nginx -t`, and background loops would all have to become host functions, so the host ends up doing the Component's real work.
- **(c) Build time** gives plain Go interfaces and one image, but "install" means rebuilding the image, so a Go + Node toolchain and a build step move into the install path.
- **(d) Compiled in** is the cheapest option to operate: one binary, one image, getters are Go method calls, "install" means enable + configure + migrate. Its cost is that Plugins cannot be updated separately from ddash; every change ships as a new ddash image.
- **Go's own `plugin` package is out.** It needs the same toolchain, build flags, and dependency versions on both sides, and a plugin cannot be unloaded. That is the coupling of (c) without its safety.
- **UI:** runtime loading (native ESM + import map, or Module Federation) is needed only if Components can be installed at runtime, i.e. with (a) or (b). With (c) or (d) a build-time merge (`React.lazy` per Page, `go:embed`) is simpler. Whichever is chosen, React must be one shared instance, and Tailwind only generates classes it has scanned. A runtime-loaded Plugin must therefore ship its own CSS, or the shell must provide every class it uses.

## 1. Component runtime options

### (a) Out of process

#### (a1) Subprocess per Component via hashicorp/go-plugin

- "`go-plugin` is a Go (golang) plugin system over RPC", used by Packer, Terraform and Vault. It "works by launching subprocesses and communicating over RPC" (net/rpc or gRPC). ([hashicorp/go-plugin README](https://github.com/hashicorp/go-plugin))
- It is "currently only designed to work over a local [reliable] network. Plugins over a real network are not supported and will lead to unexpected behavior." (same) So it means subprocesses inside the ddash container, not separate containers.
- Crash isolation: "Plugins can't crash your host process: A panic in a plugin doesn't panic the plugin user." (same)
- Bidirectional: "the host process can send it interface implementations and the plugin can call back into the host process." (same) This is how a Component would reach the host's core services (config, Lock, audit log) and, through the host, other Components' getters.
- Handshake: the plugin prints one line on stdout, `CORE-PROTOCOL-VERSION | APP-PROTOCOL-VERSION | NETWORK-TYPE | NETWORK-ADDR | PROTOCOL`, e.g. `1|3|unix|/path/to/socket|grpc`. ([docs/internals.md](https://github.com/hashicorp/go-plugin/blob/main/docs/internals.md)) "A very basic 'protocol version' is supported that can be incremented to invalidate any previous plugins." (README)
- Lifecycle API ([pkg.go.dev go-plugin v1.8.0](https://pkg.go.dev/github.com/hashicorp/go-plugin)): `ClientConfig.Cmd` is "the unstarted subprocess"; `SecureConfig` verifies the executable's checksum before running it; `AutoMTLS` negotiates one-time certificates and the client "refuses servers it didn't start"; `Client.Kill` ends the subprocess; `CleanupClients` kills all managed subprocesses on exit; `Reattach` connects to an already-running plugin process (the README: so "the host process can be upgraded while the plugin is still running"). The library does **not** restart a crashed plugin; the host has to.
- Prior art: "The Grafana server launches each plugin backend as a subprocess and communicates with it over gRPC ... A panic in a plugin doesn't panic the server." ([Grafana backend plugins](https://grafana.com/developers/plugin-tools/key-concepts/backend-plugins/))

How the ddash needs map onto (a1):

- **Getters.** Each Component's getters become a gRPC service (protobuf). A Component calling another goes host → target through the broker, or the host hands it a client for the target. Each call costs serialization plus a local socket round trip. The contract is protobuf, not a Go interface, so it needs codegen and versioning discipline. With every Component in one repo and released together, the versioning burden is small, but the codegen stays.
- **SQLite tables.** Two shapes:
  1. *DB file per Component* (`/data/components/<name>.db`), opened by the Component process itself. This enforces "each table is used by exactly one Component" physically. The core migration runner would either run the Component's embedded migrations against that file before launching it, or tell the Component to migrate itself in its setup call. Cross-Component transactions are impossible, which the dogma already rules out.
  2. *One shared file* opened by several processes. This works on one host: WAL needs all processes "on the same host computer; WAL does not work over a network filesystem", and "there can only be one writer at a time" ([sqlite.org/wal.html](https://www.sqlite.org/wal.html)). Writers from different processes contend for one lock, and `SQLITE_BUSY` must be handled. Ownership is by convention (table prefixes), not enforced.
  - If the host instead opened all files and proxied SQL for Components, every query would be an RPC, so letting each Component own its file is the natural fit. `ATTACH` would let the host read across files for backups or debugging, but with WAL, multi-file transactions are atomic only per file ([sqlite.org/lang_attach.html](https://www.sqlite.org/lang_attach.html)).
- **Start/stop/update.** The host starts each enabled Component at boot, health-checks it, restarts it on crash (own code), and on update kills it, swaps the binary, migrates, and starts it again. Only that Component's process restarts.
- **Install from GitHub.** CI in this repo builds one static Linux binary per Component (amd64, plus arm64 if wanted) and attaches it to a GitHub Release with a checksum. The installer downloads it into a data volume, checks the sha256 through `SecureConfig`, and launches it. The ddash image itself does not change.
- **Ops cost on one Docker host.** Still one container. The host now contains a process supervisor, and logs from N processes are mirrored to the host ("output will get mirrored back to the host process", README). Memory is one Go runtime per Component, roughly 10–20 MB RSS each (estimate, not measured).

#### (a2) Container per Component

- Each Component is its own image (e.g. `ghcr.io/itskontra/ddash-<component>`) and talks to the host over HTTP/gRPC on the compose network. go-plugin does not apply here ("over a real network ... not supported").
- **Install** means a new container. There are two ways to create one, and both cost something:
  - ddash creates the container through the Docker API. That needs the Docker socket, and Docker's own guidance is "only trusted users should be allowed to control your Docker daemon" ([Docker security](https://docs.docker.com/engine/security/#docker-daemon-attack-surface)). [#5](https://github.com/itsKontra/ddash/issues/5) already rejected root-equivalent Docker access for the remote hosts.
  - The operator runs it: Compose profiles allow "selectively activating services" with `--profile` or `COMPOSE_PROFILES`, while "Services without a `profiles` attribute are always enabled" ([Compose profiles](https://docs.docker.com/compose/how-tos/profiles/)). Every Component would be pre-declared in `compose.yml`, and "install" would mean a manual `docker compose up` with that profile. Then it is not installed from the UI.
- **SQLite:** a volume per Component, which gives clean ownership. Same host, so WAL works if volumes are ever shared.
- **Ops cost:** N images to build, tag and pull; N containers with their own healthchecks and logs; service discovery and auth between containers. This is the heaviest option on one Docker host. Its benefit over (a1) is resource limits and isolation per Component, which matter less when all code is first-party.

### (b) WASM in the host (wazero / Extism)

- wazero "is a WebAssembly Core Specification 1.0 and 2.0 compliant runtime written in Go" with "zero dependencies, and doesn't rely on CGO". ([tetratelabs/wazero](https://github.com/tetratelabs/wazero)) Extism's Go SDK runs on it ("Wazero (the underlying Wasm runtime)"). ([extism/go-sdk](https://github.com/extism/go-sdk))
- **Network I/O.** "A notable missing feature in the `wasip1` API is a full implementation of network sockets"; only already-open sockets can be used. A third-party `github.com/stealthrocket/net` adds `net.Dial` through `go:wasmimport` on hosts that implement it. ([go.dev/blog/wasi](https://go.dev/blog/wasi), 2023-09-13) wazero's poll "do[es] not support other types of file descriptors such as socket handles" ([wazero RATIONALE.md](https://github.com/tetratelabs/wazero/blob/main/RATIONALE.md)). Extism offers HTTP only through the host, gated by `allowed_hosts` ("if left empty then no hosts are allowed") ([Extism manifest](https://extism.org/docs/concepts/manifest/)). That covers REST calls to pfSense, AdGuard, Proxmox and the Docker socket-proxy, but not ICMP, raw TCP connect checks, DNS to a chosen server, or `nginx -t` / reload (an exec).
- **Concurrency and background work.** "Wasm is a single threaded architecture with no parallelism", and a host function call "will cause all goroutines to block until the host function call has returned" (go.dev/blog/wasi). Extism recommends one instance per goroutine: "Each plugin instance has its own state" (go-sdk README). Pollers and collectors would be scheduled by the host calling exported functions on a timer.
- **SQLite.** No Wasm-side SQLite file access without WASI preopens (`allowed_paths`, "if left empty or `null`, then no file access is granted"). The practical shape is host functions `query/exec` scoped to that Component's tables. That works, but the host builds and maintains a DB API for guests.
- **Getters** are host functions that call into another instance, with bytes in and bytes out (JSON or protobuf), so they are untyped at the boundary.
- **Install/update** is the strongest point: fetch a `.wasm` (Extism can load from `WasmUrl`) and instantiate it, with no restart and no new process. Ops cost is one container.
- **Net:** the sandbox protects against code ddash does not trust, and "third-party Plugins, sandboxing and plugin permissions" are out of scope (#1). In exchange, ddash would have to re-implement network, exec, SQLite and timers as host functions.

### (c) Build time: rebuild the image with the chosen plugins

- Components are ordinary Go packages registered at compile time (e.g. a generated `plugins_gen.go` that blank-imports the selected `components/<name>` packages, the way xcaddy "make[s] custom builds of the Caddy Web Server" from a list of modules ([caddyserver/xcaddy](https://github.com/caddyserver/xcaddy))).
- **Getters:** Go interfaces, checked by the compiler, no serialization. **SQLite:** one file and one connection pool in the host, tables namespaced per Component, and the core migration runner keeps one version row per Component. **Lifecycle:** setup/health/config are Go calls. Start/stop of a Component means starting or stopping its goroutines; an update restarts the whole container.
- **Install from GitHub:** the catalogue writes the selection, then something must run `go build` + `vite build` + `docker build` and restart. Either the Docker host builds locally (toolchains plus Docker socket, the same concern as a2), or CI builds an image per selection (combinatorial and slow). On one host this is mostly (d) with a slower, more fragile install step.
- **Go's `plugin` package** (runtime `.so` loading) is not a way around the rebuild. It requires that all parts "are compiled using exactly the same version of the toolchain, the same build tags" and that "all common dependencies of the application and its plugins are built from the same source code"; a plugin "cannot be closed"; it is Linux/FreeBSD/macOS only and "poorly supported by the Go race detector". ([pkg.go.dev/plugin](https://pkg.go.dev/plugin)) Vault avoids it: "dynamic library loading is not acceptable for security reasons" (go-plugin README).

### (d) Compiled in: everything ships, install = enable + configure

- Same in-process model as (c), but every first-party Plugin and Component is always in the binary. The catalogue lists what exists. "Install" resolves required Components, runs their migrations and setup, and shows the config form. "Uninstall" disables them and keeps or drops their tables.
- **Getters, SQLite, lifecycle:** as in (c). Disabled Components never start their goroutines.
- **Install from GitHub** is just reading metadata. The code already ships, so the catalogue can be read from the embedded tree instead of GitHub. **Update** means pulling a new ddash image (`docker compose pull && up -d`), which updates everything at once.
- **Ops cost:** one image, one container, one process, one DB file. Matches the map's open note "embedded via `go:embed`, one image".
- Cost: no per-Plugin release cadence, and a bug in any Component ships with (and can crash) the whole binary, since there is no process isolation. `recover` per goroutine helps, but a nil map write or an `os.Exit` in a Component still takes the host down.

## 2. Plugin UI loading

### What must be shared with the shell

- **React must be a single instance.** "In order for Hooks to work, the `react` import from your application code needs to resolve to the same module as the `react` import from inside the `react-dom` package." Separate copies on one page are fine only if each tree is rendered by its own `react-dom` ([react.dev invalid-hook-call](https://react.dev/warnings/invalid-hook-call-warning)). A Widget rendered inside the shell's tree therefore has to use the shell's React, react-dom and `react/jsx-runtime`. The same goes for context-bearing libraries such as react-router (route context, `useNavigate`) and any shell providers (auth, query client, theme).
- **React ships CommonJS only.** The `react@19.3.1` entry is `module.exports = require('./cjs/react.production.js')` with no ESM build (unpkg, registry). For native-ESM sharing the shell must build its own ESM "vendor" entries that re-export React. react-router 8.5.0 is `"type": "module"` and has peer deps on react/react-dom `>=19.2.7` (registry).
- **shadcn/ui is source, not a package:** "This is not a component library. It is how you build your component library." ([ui.shadcn.com/docs](https://ui.shadcn.com/docs)) To share it, ddash would wrap the generated components in a workspace package (say `@ddash/ui`) and share that like React. Otherwise each Plugin keeps its own copy.
- **Tailwind generates only classes it scans:** "Tailwind works by scanning your project for utility classes, then generating all of the necessary CSS based on the classes you've actually used"; extra paths are added with `@source` ([Tailwind docs](https://tailwindcss.com/docs/detecting-classes-in-source-files)). For a runtime-loaded Plugin, either the shell's build scans `plugins/*/` (all in this repo, so this works, but then a new Plugin needs a shell rebuild for its CSS), or each Plugin bundle ships its own CSS file, which risks duplicate or conflicting utilities.

### Option U1: native ESM + import map

- "Import maps allow developers to specify (almost) any text they want in the module specifier", which maps bare specifiers like `react` to URLs. Several maps are merged into "a single global import map", but each must be "declared and processed before any `<script>` elements that import modules using specifiers declared in the map"; an already-mapped specifier keeps its first mapping. Baseline widely available since March 2023. ([MDN importmap](https://developer.mozilla.org/en-US/docs/Web/HTML/Reference/Elements/script/type/importmap))
- Shape: the host renders `index.html` with an import map pointing `react`, `react-dom/client`, `react/jsx-runtime`, `react-router`, `@ddash/ui`, `@ddash/sdk` at shell-built ESM files. Each Plugin is a Vite library build in `es` format that externalizes those deps ("Make sure to also externalize any dependencies that you do not want to bundle into your library, e.g. `vue` or `react`", via `build.rolldownOptions.external` in Vite 8 ([Vite build guide](https://vite.dev/guide/build.html))). The shell `await import('/plugins/<name>/index.js')` and reads the exported Widgets, Nav Entries and Pages.
- Prior art: Grafana loads plugin `module.js` through SystemJS; a shared dependency "must be listed in a SystemJS import map in the Grafana application" and "must be externalized in the plugin's build tool configuration", because "In some cases, only a single instance of a dependency can exist during runtime" ([Grafana NPM dependencies](https://grafana.com/developers/plugin-tools/key-concepts/npm-dependencies)). Grafana's 12→13 guide notes that it shares one React instance with all plugins at runtime ([migration guide](https://grafana.com/developers/plugin-tools/migration-guides/update-from-grafana-versions/migrate-12_x-to-13_x)).
- Cost: no extra runtime library, only browser standards. A missed external silently bundles a second React, which fails at runtime, not at build time. Shared versions are pinned by the shell, and a Plugin built against another version is not detected.

### Option U2: Module Federation (`@module-federation/vite`)

- "Vite Plugin for Module Federation" (`@module-federation/vite` 1.23.5) supports Vite 5–7 (Rollup) and 8+ (Rolldown) ([module-federation/vite](https://github.com/module-federation/vite)). A remote declares `exposes` and `shared` (e.g. `react: { singleton: true }`).
- Runtime registration: `registerRemotes([{ name, entry: '.../mf-manifest.json' }])` then `loadRemote('remote/util')`. `SharedConfig` has `singleton` and `requiredVersion`, and the runtime prefers an already-loaded shared module that satisfies them ([MF runtime API](https://module-federation.io/guide/runtime/runtime-api)). Caveat: the Vite plugin's `hostProvidesAllShared` mode does not support "`loadRemote` / runtime-registered remotes".
- Cost: a federation runtime and manifest format in the shell. The benefit over U1 is version negotiation for shared deps, which matters little when every Plugin is built from the same repo and lockfile.

### Option U3: build-time merge

- Plugins are workspace packages imported by the shell. Each Page is `React.lazy(() => import('@ddash/plugin-x/page'))`, so Vite code-splits it. One Tailwind scan and one React come for free. The `dist` is embedded with `//go:embed` (patterns are "relative to the package directory" and "may not contain '.' or '..'" ([pkg.go.dev/embed](https://pkg.go.dev/embed)), so the build copies `dist` under the Go package).
- Enabled-ness is runtime: the API tells the shell which Plugins are enabled, and the shell shows only their Nav Entries, Pages and Widgets.
- Natural pairing with (c)/(d). With (a)/(b) it means a Plugin's backend can be installed at runtime while its UI cannot, which defeats the point of runtime install.

## 3. Comparison

| | (a1) go-plugin subprocess | (a2) container per Component | (b) WASM (wazero/Extism) | (c) build-time image | (d) compiled in |
|---|---|---|---|---|---|
| **Getter calls** | gRPC via host broker; protobuf contracts + codegen | HTTP/gRPC over compose network; service discovery + auth | Host functions, bytes in/out, untyped | Go interfaces, compile-checked | Go interfaces, compile-checked |
| **Own SQLite tables** | DB file per Component (enforced), or shared file with cross-process write lock | Volume/file per Component | Host-mediated `query/exec` functions | One file, namespaced tables, core runner | One file, namespaced tables, core runner |
| **Migrations** | Host runs Component's embedded SQL on its file, or Component self-migrates on setup | Component self-migrates | Host runs them (SQL shipped with module) | Core runner, one version row per Component | Same as (c) |
| **Network I/O, exec, background** | Native Go | Native Go | Only via host functions; no sockets in wasip1; single-threaded | Native Go | Native Go |
| **Start/stop/update** | Per-Component process restart; host writes supervision and crash restart | Per-container; needs Docker socket or manual compose | Hot load/unload of `.wasm` | Whole-container restart after rebuild | Enable/disable at runtime; update = new ddash image |
| **Install from GitHub** | Download release binary + checksum (`SecureConfig`) | Pull GHCR image; create container | Download `.wasm` | Rebuild image (local toolchain or CI per selection) | Metadata only; code already shipped |
| **Crash isolation** | Yes | Yes | Yes (sandbox) | No | No |
| **Natural UI loading** | U1 or U2 (runtime) | U1 or U2 | U1 or U2 | U3 | U3 |
| **Ops cost, one Docker host** | 1 container, N processes, release pipeline per Component | N containers/images; heaviest | 1 container; large host-function surface | 1 container; build step in install path | 1 container, 1 process, 1 image; lightest |

UI options compared:

| | U1 import map + ESM | U2 Module Federation | U3 build-time merge |
|---|---|---|---|
| Runtime install of UI | Yes | Yes | No (rebuild) |
| Shares React/router/`@ddash/ui` | Shell-built ESM vendor files + externals | `shared` singletons with version negotiation | Automatically (one bundle) |
| Tailwind classes | Shell scans `plugins/*` or Plugin ships CSS | Same | One scan, automatic |
| Extra runtime machinery | None (browser standard) | MF runtime + manifests | None |
| Failure mode | Missed external → second React at runtime | Version mismatch resolved or warned at runtime | Build fails at build time |

## 4. Tradeoffs for the decision (#19)

- **What does "install from GitHub" need to mean?** If it means "new code arrives without a new ddash image", only (a) or (b) deliver it, and the UI must load at runtime (U1/U2). If it means "turn on a first-party feature", (d) does it with the least machinery, and the catalogue can read the embedded tree.
- **The operator's lean toward (a) buys** crash isolation and independent Component updates, and DB file ownership that the process boundary enforces. **It costs** protobuf contracts for every getter, a process supervisor in the host, a per-Component release pipeline, and a split migration story (the core no longer holds every DB connection).
- **(a1) vs (a2):** on one Docker host, (a1) gets most of the isolation without the Docker socket or N images. (a2) adds per-container resource limits but either needs root-equivalent Docker access (rejected in #5's spirit) or turns install into a compose edit.
- **(b) is ruled out in practice** by Components' needs (ICMP/TCP/DNS probes, exec of `nginx -t`, background pollers) rather than by the runtime's quality.
- **Hybrids are possible.** One is (d) for Components plus U3 now, keeping the getter interfaces narrow enough to lift a Component behind go-plugin later. Another is (a1) for Components that call flaky external APIs and in-process for core ones. The interface shape (getters as small Go interfaces) is the same either way, so the choice can be staged.
- **Whichever runtime is picked,** React, react-dom, `react/jsx-runtime`, react-router and the shell's `@ddash/ui` (shadcn wrapper) must be singletons, and Plugin CSS needs a rule (shell scans `plugins/*`, or each Plugin ships CSS).
