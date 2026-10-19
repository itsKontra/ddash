# Plain nginx: inspecting, validating and reloading conf files from ddash

Research for [#7](https://github.com/itsKontra/ddash/issues/7). Researched 2026-10-19.

Question: how can a Go service parse plain nginx configs into server blocks? How is a config validated and reloaded when nginx runs in a different container or host than ddash? How does Nginx Proxy Manager (NPM) store its proxy hosts, and how could they be exported to plain conf files?

## Answer in brief

- **Parse with `nginx -T` + `nginx-go-crossplane`.** `nginx -T` run inside the nginx container prints every config file nginx actually loaded, each prefixed with `# configuration file <path>:`. Feed that into crossplane through its `Open`/`Glob` hooks. crossplane gives a directive tree (`server`, `listen`, `server_name`, `ssl_certificate`, `location`, `proxy_pass`, ...). ddash then walks the tree to build its own server-block model. Don't write our own parser.
- **Validate with `nginx -t`, reload with `nginx -s reload` (or SIGHUP), both run in the nginx container.** Over the Docker Engine API, that means an exec (create, start, then inspect for `ExitCode`) for `-t`/`-T`, and either an exec of `nginx -s reload` or `POST /containers/{id}/kill?signal=SIGHUP`. A failed reload is silent: nginx rolls back and keeps the old config. So always run `-t` first and read its exit code and stderr.
- **NPM keeps hosts in a database and renders them into real nginx files.** The database is SQLite at `/data/database.sqlite` by default; MySQL and Postgres are optional. Each host is rendered from Liquid templates into `/data/nginx/proxy_host/<id>.conf`. Those files are already plain nginx. But they depend on NPM's `/etc/nginx/conf.d/include/*.conf` snippets, on NPM-specific certificate paths, and on `set $server/$port` variables instead of a literal `proxy_pass`. Exporting means copying the rendered files plus the snippets and certificates, then rewriting the NPM-isms. Alternatively, regenerate clean files from the DB or the REST API.

## 1. Parsing nginx config from Go

### Option A: `nginxinc/nginx-go-crossplane` (recommended)

- This is F5/NGINX's own Go port of the Python `crossplane` config/JSON converter. It is Apache-2.0 and actively tagged; the latest tag is `v0.4.89` (2026-05-25). [README](https://github.com/nginxinc/nginx-go-crossplane/blob/11d0d6bebfebff2fd79062e5a4ff7afd418dcc36/README.md)
- `crossplane.Parse(filename, *ParseOptions) (*Payload, error)` returns a `Payload` with a `Config` per file. Each config holds a `Parsed` tree of `Directive{Directive, Line, Args, File, Includes, Block, Comment}`. [types.go](https://github.com/nginxinc/nginx-go-crossplane/blob/11d0d6bebfebff2fd79062e5a4ff7afd418dcc36/types.go)
- `ParseOptions` has the hooks ddash needs ([parse.go](https://github.com/nginxinc/nginx-go-crossplane/blob/11d0d6bebfebff2fd79062e5a4ff7afd418dcc36/parse.go)):
  - `Open func(path string) (io.ReadCloser, error)`: "use this alternative to open config files". This lets ddash parse files it fetched remotely (from a `nginx -T` dump, a tar from the Docker archive API, or SSH) without them being on ddash's disk.
  - `Glob func(path string) ([]string, error)` resolves wildcard `include`s, such as `include /etc/nginx/conf.d/*.conf;`, against the same virtual file set.
  - `CombineConfigs` folds includes into one tree. `SingleFile` parses one file only. `ParseComments` keeps comments (useful for ddash-specific annotations in conf files).
  - Directive validation: by default it checks context and argument count against a built-in directive table (`DefaultDirectivesMatchFunc`, [analyze.go](https://github.com/nginxinc/nginx-go-crossplane/blob/11d0d6bebfebff2fd79062e5a4ff7afd418dcc36/analyze.go)). `ErrorOnUnknownDirectives`, `SkipDirectiveContextCheck`, `SkipDirectiveArgsCheck` and `DirectiveSources` (generated tables for OSS versions, NGINX Plus, njs, lua, headers-more, ...) tune this.
- It can also write config: `Build`, `BuildFiles`, `BuildInto` ([build.go](https://github.com/nginxinc/nginx-go-crossplane/blob/11d0d6bebfebff2fd79062e5a4ff7afd418dcc36/build.go)). If ddash ever generates conf files, it can emit them from the same tree.
- **Limits.** crossplane is a syntax-level parser. It doesn't check that certificate files exist, that upstream hosts resolve, or anything else nginx checks at load time. That is what `nginx -t` is for (section 2). It also doesn't model semantics: ddash must interpret the tree itself. For example: `server` → `listen` (`ssl` flag) + `server_name` + `ssl_certificate` + `location` → `proxy_pass`, plus `upstream` blocks by name.

### Option B: `nginx -T` as the source of truth

- `-T`: "same as `-t`, but additionally dump configuration files to standard output (1.9.2)". [nginx command-line switches](https://nginx.org/en/docs/switches.html)
- In the nginx source, the dump runs only after the config loads successfully. Each file is written as `# configuration file <name>:` followed by its raw contents. On failure, nginx prints `configuration file %s test failed` to stderr and exits 1, without a dump. [src/core/nginx.c L299-L335](https://github.com/nginx/nginx/blob/1725db9059e8d4057ce8d5090673d16e20713b45/src/core/nginx.c#L299-L335)
- So `nginx -T` gives ddash the exact set of files the running nginx would load, including resolved globs, with no file-system mount. Split stdout on the `# configuration file ...:` markers into a `map[path]content`, then hand that map to crossplane via `Open`/`Glob`. This is the cleanest combination: nginx resolves `include`, and crossplane parses.

### Option C: our own parser

Not worth it. nginx syntax has quoting, escaping, `map`/`types`-style blocks and lua/njs blocks. crossplane already handles all of these (`lex.go`, `analyze_map.go`, `lua.go` in the repo).

## 2. Validating and reloading when nginx is elsewhere

### What nginx itself guarantees

- `-t`: "nginx checks the configuration for correct syntax, and then tries to open files referred in the configuration." `-s reload`: "reload configuration, start the new worker process with a new configuration, gracefully shut down old worker processes." `-q` suppresses non-error messages during testing. [switches](https://nginx.org/en/docs/switches.html)
- Because `-t` opens referenced files, such as certificates and `auth_basic_user_file`, it is only meaningful where nginx's file system is, that is, **inside the nginx container**, not in ddash's.
- On reload, the master "first checks the syntax validity, then tries to apply new configuration ... If this fails, it rolls back changes and continues to work with old configuration." [Controlling nginx](https://nginx.org/en/docs/control.html); same wording in the [Beginner's Guide](https://nginx.org/en/docs/beginners_guide.html).
- **Consequence:** a reload that fails is not reported to the caller. The signal is delivered and nginx keeps serving the old config, logging the error. ddash must gate every reload on a `nginx -t` with exit code 0, and show `-t`'s stderr to the operator on failure. NPM does exactly this: `reload()` calls `test()` first (see section 3).
- Reload signal: HUP to the master process. Its PID is in the file named by the `pid` directive. [Controlling nginx](https://nginx.org/en/docs/control.html)

### Doing it through the Docker Engine API (nginx in a container on a Docker host)

From the Docker Engine API v1.56 spec ([v1.56.yaml](https://docs.docker.com/reference/api/engine/version/v1.56.yaml)):

- `POST /containers/{id}/exec`: "Run a command inside a running container". The body takes `Cmd`, `AttachStdout`, `AttachStderr`, `User`, and other fields. It returns an exec ID (201). It returns 409 if the container is paused.
- `POST /exec/{id}/start` with `{"Detach": false}` streams output (`application/vnd.docker.multiplexed-stream` when no TTY). Each frame has an 8-byte header `{STREAM_TYPE, 0, 0, 0, SIZE1..4}` that separates stdout from stderr. Go's `github.com/docker/docker/pkg/stdcopy` demultiplexes it.
- `GET /exec/{id}/json` returns `Running` and `ExitCode`. Poll it after the stream closes to get `nginx -t`'s result.
- `POST /containers/{id}/kill?signal=SIGHUP`: "Send a POSIX signal to a container". The signal is a string or integer; the default is SIGKILL, so always pass it. Returns 409 if the container isn't running.
- In the official `nginx` image, the entrypoint ends in `exec "$@"` and `CMD ["nginx", "-g", "daemon off;"]`, so the nginx master is PID 1. Docker's kill signal therefore goes straight to the master. The image also sets `STOPSIGNAL SIGQUIT` (graceful). [docker-nginx Dockerfile](https://github.com/nginx/docker-nginx/blob/4b9f658be76820b24e4ef4605413a5c08707a512/mainline/debian/Dockerfile), [docker-entrypoint.sh](https://github.com/nginx/docker-nginx/blob/4b9f658be76820b24e4ef4605413a5c08707a512/entrypoint/docker-entrypoint.sh)

The flow for ddash:

1. Inspect: exec `nginx -T` → ExitCode 0 → split the dump → crossplane parse → server-block model.
2. Validate: exec `nginx -t -q` → read ExitCode and stderr.
3. Reload: only if step 2 returned 0, exec `nginx -s reload` (or kill with `SIGHUP`). Re-run `-t` right before, as NPM does, so the check and the reload see the same files.

If ddash should also *edit* conf files, it needs write access to the nginx container's config. That means either a shared bind mount or volume, or `PUT /containers/{id}/archive`, which uploads a tar into the container. The ticket only asks about inspect, validate and reload; editing is a separate decision.

**Security note.** Exec and kill need a Docker API client with full control of that daemon. Docker's own guidance is "only trusted users should be allowed to control your Docker daemon", and the daemon "requires `root` privileges" unless rootless. [Docker Engine security](https://docs.docker.com/engine/security/) This belongs in the security-model ticket: one Docker-socket connection gives ddash root on that Docker host. A socket proxy that allows only `exec` and `kill` on the nginx container is the usual way to narrow it.

### If nginx runs on a plain host or an LXC instead of Docker

The same three commands apply. They run over SSH (a restricted key, or `sudo` rules for `nginx -t`, `nginx -T` and `nginx -s reload` only), or the host's service manager reloads nginx. Nothing in nginx's docs offers an HTTP control API for OSS nginx: the documented control interface is the signal/`-s` interface above.

## 3. How Nginx Proxy Manager stores proxy hosts (v2.16.0)

All links are pinned to tag [`v2.16.0`](https://github.com/NginxProxyManager/nginx-proxy-manager/tree/v2.16.0) (2026-09-24).

### Database

- The default is SQLite at `DB_SQLITE_FILE`, else `/data/database.sqlite`, via `better-sqlite3`. MySQL/MariaDB (`DB_MYSQL_*`) and Postgres (`DB_POSTGRES_*`) are selected by env vars. [backend/lib/config.js](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/backend/lib/config.js)
- `proxy_host` table: the initial migration creates `id`, `created_on`, `modified_on`, `owner_user_id`, `is_deleted`, `domain_names` (JSON), `forward_ip`, `forward_port`, `access_list_id`, `certificate_id`, `ssl_forced`, `caching_enabled`, `block_exploits`, `advanced_config` and `meta` (JSON). [migrations/20180618015850_initial.js](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/backend/migrations/20180618015850_initial.js) Later migrations add or rename columns: websockets, `forward_host` (replacing `forward_ip`), http2, `forward_scheme`, `enabled`, `locations` (custom locations), HSTS, `trust_forwarded_proto`, and others. [backend/migrations/](https://github.com/NginxProxyManager/nginx-proxy-manager/tree/v2.16.0/backend/migrations) JSON attributes on the model are `domain_names`, `meta` and `locations`. [models/proxy_host.js](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/backend/models/proxy_host.js)
- Rows are soft-deleted (`is_deleted`), so an export must filter `is_deleted = 0`.

### Generated files

- Path: `/data/nginx/<host_type>/<id>.conf`, for example `/data/nginx/proxy_host/12.conf`. The default host is `/data/nginx/default_host/site.conf`. `getConfigName` in [backend/internal/nginx.js](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/backend/internal/nginx.js)
- Rendering: Liquid templates `backend/templates/<host_type>.conf` are rendered with the host row and written with `fs.writeFileSync` (`generateConfig`). [proxy_host.conf](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/backend/templates/proxy_host.conf) includes the partials `_listen.conf`, `_certificates.conf`, `_assets.conf`, `_exploits.conf`, `_hsts.conf`, `_forced_ssl.conf`, `_access.conf` and `_location.conf`.
- Validation and reload in NPM (`configure`): test, delete the old file, render, test again. On failure it sets `meta.nginx_online=false` and `meta.nginx_err`, and keeps the bad file as `<id>.conf.err`. Then it calls `reload()`, which is `nginx -t -g "error_log off;"` followed by `nginx -s reload`. [backend/internal/nginx.js](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/backend/internal/nginx.js) This is the same test-then-reload pattern ddash should use.
- The main config includes `/data/nginx/{default_host,proxy_host,redirection_host,dead_host,temp}/*.conf` in `http {}` and `/data/nginx/stream/*.conf` in `stream {}`. It also has optional `/data/nginx/custom/*[.]conf` hooks and `pid /run/nginx/nginx.pid`. [docker/rootfs/etc/nginx/nginx.conf](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/docker/rootfs/etc/nginx/nginx.conf)

### NPM-isms inside the generated files (what an export must fix)

- **Upstream through variables, not a literal `proxy_pass`.** The server block does `set $forward_scheme ...; set $server "<forward_host>"; set $port <port>;`, and the shared snippet does `proxy_pass $forward_scheme://$server:$port$request_uri;`. [proxy_host.conf](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/backend/templates/proxy_host.conf), [conf.d/include/proxy.conf](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/docker/rootfs/etc/nginx/conf.d/include/proxy.conf) With variables in `proxy_pass`, a domain-name upstream "is determined using a resolver" at request time. [ngx_http_proxy_module proxy_pass](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_pass) So plain nginx needs a `resolver` directive, or the export rewrites the line to a literal `proxy_pass http://host:port;`. A ddash parser that reads NPM-style files must resolve `set` variables to find the upstream. One that reads hand-written files can read `proxy_pass` directly. A custom location (`_location.conf`) does use a literal `proxy_pass {{scheme}}://{{host}}:{{port}}{{path}}`.
- **Relative includes of NPM snippets:** `include conf.d/include/proxy.conf`, `letsencrypt-acme-challenge.conf`, `ssl-cache.conf`, `ssl-ciphers.conf`, `block-exploits.conf`, `assets.conf`, `force-ssl.conf`. These ship in the image under `/etc/nginx/conf.d/include/`. [docker/rootfs/etc/nginx/conf.d/include/](https://github.com/NginxProxyManager/nginx-proxy-manager/tree/v2.16.0/docker/rootfs/etc/nginx/conf.d/include) Copy them alongside the export or inline them.
- **Certificate paths:** `/etc/letsencrypt/live/npm-<certificate_id>/{fullchain,privkey}.pem` for Let's Encrypt, `/data/custom_ssl/npm-<id>/...` for custom certificates. [_certificates.conf](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/backend/templates/_certificates.conf) Renewal is done by NPM's certbot. After migration, something else must renew them (certbot or acme.sh on the nginx host). That is a separate decision.
- **Access lists:** `auth_basic_user_file /data/access/<access_list_id>` plus generated `allow`/`deny` rules. [_access.conf](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/backend/templates/_access.conf)
- **Logs:** `access_log /data/logs/proxy-host-<id>_access.log proxy;` uses NPM's `proxy` log format, defined in its `log-proxy.conf`.
- **Disabled hosts** render to a file with no `server` block, because the template is wrapped in `{% if enabled %}`.
- **File names are numeric IDs**, so `12.conf` says nothing about the site. Rename to `<first domain>.conf` on export.

### Export options

1. **Copy the rendered files (fastest, most faithful).** Take `/data/nginx/proxy_host/*.conf` (plus `redirection_host`, `stream` and `dead_host` if used), `/etc/nginx/conf.d/include/*.conf`, `/data/access/*` and the certificate directories. Then rewrite: rename by domain, either replace `set $server/$port` + `include conf.d/include/proxy.conf` with a literal `proxy_pass` or add a `resolver`, fix certificate paths, and drop the NPM log format. crossplane can do the rewriting mechanically (parse → modify the tree → `BuildFiles`). Verify with `nginx -t` in the new container.
2. **Regenerate from data.** Read `proxy_host` rows from `/data/database.sqlite` (filter `is_deleted=0`, parse the JSON `domain_names` and `locations`), or call NPM's REST API. The API has `POST /api/tokens` with `{identity, secret}`, then `GET /api/nginx/proxy-hosts?expand=certificate,access_list` with a bearer token. [schema/paths/tokens/post.json](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/backend/schema/paths/tokens/post.json), [schema/paths/nginx/proxy-hosts/get.json](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.16.0/backend/schema/paths/nginx/proxy-hosts/get.json) Emit clean conf files from ddash's own template. This gives tidier output but must reproduce `advanced_config` verbatim, and that field is arbitrary nginx text.

The migration is one-off, so option 1, done once by hand or with a throwaway script, is enough. ddash itself never needs to understand NPM afterwards.

## Implications for ddash

- One **nginx repository** (one per data source, per the map) with three operations: `Inspect()` (`nginx -T` → crossplane → server blocks), `Validate()` (`nginx -t`) and `Reload()` (validate, then `-s reload`/SIGHUP). It reaches nginx through a small transport seam: Docker exec now, SSH possible later.
- The parsed model per server block: `listen` ports and TLS flag, `server_name`s, `ssl_certificate` path, and `location`s with their `proxy_pass` target (resolve `upstream` names and `set` variables). That is enough to link a server block to an **App** (by `server_name` → URL) and its upstream to a **Workload** (by host:port).
- Reading a certificate's expiry needs the PEM itself. Exec `cat` on the path, or use `GET /containers/{id}/archive?path=...`. Alternatively, a TLS Check against the site reports the served certificate, which needs no file access.
- Open decisions for other tickets: whether ddash *writes* conf files (mount vs archive upload), the Docker socket's blast radius (socket proxy), and certificate renewal after NPM is gone.
