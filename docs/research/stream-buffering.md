# Buffering between the browser and a Component, for a streamed Action

Research for [#164](https://github.com/itsKontra/ddash/issues/164) (map [#162](https://github.com/itsKontra/ddash/issues/162)). Researched 2026-10-26 against `main` at `ef4f067`.

Question: could anything between the browser and an Action's Component buffer a streamed response, or end it early, so progress arrives all at once or not at all? What must the host do so each message reaches the browser as it is written, without the Operator changing Nginx Proxy Manager?

## Answer in brief

- **Inside ddash, only `net/http`'s own write buffer holds messages back.** No middleware wraps the `ResponseWriter`, so `http.Flusher` and `http.ResponseController` reach the real writer. There is no compression. The server sets only `ReadHeaderTimeout`, so nothing cuts a 60 s stream. The router must flush after every message. The Vite dev proxy pipes responses through as they arrive.
- **In front, Nginx Proxy Manager buffers by default.** It never sets `proxy_buffering`, so nginx's default `on` applies to every Proxy Host. The Websockets toggle doesn't change that. The upstream switches it off per response by sending **`X-Accel-Buffering: no`**, which NPM does not ignore and nginx does not pass on to the browser.
- **Nothing else in NPM gets in the way.** gzip covers only `text/html`, so `application/connect+json` is not compressed. HTTP/2 to the browser doesn't buffer. `proxy_read_timeout` is 90 s between reads, above the 60 s `ActionDeadline`.
- **So the host must**: set `X-Accel-Buffering: no`, keep `Content-Type: application/connect+json` without HTTP-level `Content-Encoding`, and flush after the headers and after each message. No heartbeat is needed while `ActionDeadline` stays under 90 s. The Operator changes nothing in NPM.

## 1. Inside ddash

### The server and its timeouts

- Production builds one `http.Server` with only `ReadHeaderTimeout: 10 * time.Second` (`cmd/ddash/main.go:105`). `ReadTimeout`, `WriteTimeout` and `IdleTimeout` are zero.
- In `net/http`, a zero `WriteTimeout` "means there will be no timeout", and a zero `IdleTimeout` with a zero `ReadTimeout` means no idle timeout either. `ReadHeaderTimeout` resets the read deadline once headers are read. ([`net/http/server.go`, `Server` fields](https://github.com/golang/go/blob/go1.26.10/src/net/http/server.go#L3006-L3036))
- So nothing in the server ends a 60 s stream. The router's own `context.WithTimeout(..., Deadline(md))` (`core/router/router.go:136`), `ActionDeadline = 60 * time.Second` (`core/router/router.go:170`), is the only bound, as the map intends.
- **Shutdown does end a stream.** `drain` gives in-flight requests `httpShutdownTimeout = 5 * time.Second` and then calls `srv.Close()` (`cmd/ddash/main.go:49`, `cmd/ddash/main.go:134-147`). A streamed Action in flight at SIGTERM loses its browser after 5 s. That is the "in-flight Action at host shutdown" fog on the map, not a buffering problem.
- `cmd/dev` runs the same core on `127.0.0.1:8080` (`cmd/dev/main.go:46`); fakelab and smoke servers also set only `ReadHeaderTimeout` (`internal/fakelab/fakelab.go:159-161`, `internal/smoke/smoke.go:271-277`).

### The middleware around `core/router`

The handler chain for `/api/c/` is, outermost first (`core/host.go:144`, `core/host.go:154-182`):

1. `forwarded` from `auth.Forwarded(opts.TrustedProxy)` (`core/host.go:97`): rewrites `r.RemoteAddr` and drops untrusted `X-Forwarded-*` request headers, then calls `next.ServeHTTP(w, r)` with the same `w` (`core/auth/forwarded.go:32-42`).
2. The top `http.ServeMux` (`core/host.go:169`).
3. `guard`: `keycloak.Guard` (`core/auth/keycloak.go:130-150`), or `auth.As` in tests and dev (`core/auth/auth.go:67-74`, `core/auth/dev.go:8`). Both pass `w` through and only swap the request's context.
4. `requireConnectHeader` (`core/host.go:202-210`): checks `Connect-Protocol-Version: 1`, passes `w` through. A streaming client (connect-web) sends this header too, so it needs no change.
5. The `api` `http.ServeMux` (`core/host.go:155-156`), then `*router.Router` (`core/router/router.go:69`).

No layer wraps the `http.ResponseWriter`. The router receives `net/http`'s own `*response`, which implements `http.Flusher` and `FlushError` ([server.go L1730-L1743](https://github.com/golang/go/blob/go1.26.10/src/net/http/server.go#L1730-L1743)), so both `w.(http.Flusher)` and `http.NewResponseController(w).Flush()` work. `http.MaxBytesReader(w, r.Body, ...)` (`core/router/router.go:104`) wraps only the request body. (`http.StripPrefix` at `core/api.go:41` wraps only `/api/core`, and does not wrap `w` either.) If a writer wrapper is ever added, it must implement `Unwrap() http.ResponseWriter` so `http.ResponseController` still finds `Flush`.

There is **no compression** in the Go server: nothing sets `Content-Encoding` or wraps a gzip writer (the only `compress/gzip` use is decoding the embedded icon data, `core/icons/icons.go:58`).

### `net/http` buffers until you flush

- Each HTTP/1.x connection writes through a 4 KiB `bufio.Writer` ([server.go L1992](https://github.com/golang/go/blob/go1.26.10/src/net/http/server.go#L1992)), and the response holds the first 2048 bytes before choosing chunking ([`bufferBeforeChunkingSize`, L341](https://github.com/golang/go/blob/go1.26.10/src/net/http/server.go#L341)). A small Connect envelope sits there until the handler returns or flushes. **The router must flush after every message.**
- The request's protocol decides framing. For HTTP/1.1 with no `Content-Length`, Go uses `Transfer-Encoding: chunked`; for HTTP/1.0 it "cannot do chunked transfer encoding" and closes the connection to signal EOF ([server.go L1488-L1513](https://github.com/golang/go/blob/go1.26.10/src/net/http/server.go#L1488-L1513)). Either way, a flush puts the bytes on the wire at once. This matters because nginx speaks HTTP/1.0 to upstreams unless told otherwise (section 2).
- The router sets `Content-Type` itself (`core/router/router.go:149`), so `net/http` does not sniff the first 512 bytes.

### The dev server's proxy

- `web/vite.config.ts:69` proxies `/api` (so `/api/c`) to the core with `server.proxy: { "/api": core, ... }`; no options.
- Vite 8.3.4 (the repo's pin, `package.json`) depends on `http-proxy-3` ([package.json L107](https://github.com/vitejs/vite/blob/v8.3.4/packages/vite/package.json#L107)) and creates it with `httpProxy.createProxyServer(opts)` ([proxy.ts L107](https://github.com/vitejs/vite/blob/v8.3.4/packages/vite/src/node/server/middlewares/proxy.ts#L107)), without `selfHandleResponse`.
- `http-proxy-3` then pipes the upstream response straight into the browser's response: `proxyRes.pipe(res)` ([web-incoming.ts L172-L193](https://github.com/sagemathinc/http-proxy-3/blob/v1.23.3/lib/http-proxy/passes/web-incoming.ts#L172-L193)). It sets no proxy timeout unless `timeout`/`proxyTimeout` are given ([L47-L50, L130-L133](https://github.com/sagemathinc/http-proxy-3/blob/v1.23.3/lib/http-proxy/passes/web-incoming.ts#L47-L50)), and Vite's dev server adds no compression middleware.
- So `make dev` streams as written. Nothing to change there.

### The leg to the Component

The host reaches a Component over gRPC (`ep.Conn`, `core/router/router.go:138`). gRPC is built for streaming and sends each message as it is written; this leg was not researched further, since the ticket is about the HTTP side.

## 2. In front of ddash: Nginx Proxy Manager

Checked at NPM tags v2.10.0, v2.10.4, v2.11.0, v2.11.3 and v2.12.0–v2.12.6. The deployment puts a Proxy Host `ddash.<domain>` → `http://<ddash-ip>:8080` in front of ddash (`docs/deployment.md:49-51`).

### What NPM generates

- **Global `http` block** ([nginx.conf L33-L45 at v2.12.6](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.12.6/docker/rootfs/etc/nginx/nginx.conf#L33-L45); the same at [v2.10.0 L29-L41](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.10.0/docker/rootfs/etc/nginx/nginx.conf#L29-L41)):
  - `proxy_read_timeout 90s` (also `proxy_connect_timeout`, `proxy_send_timeout 90s`).
  - `gzip on`, with no `gzip_types` or `gzip_proxied`.
  - `proxy_ignore_client_abort off`, `proxy_http_version 1.1`, `proxy_cache off`.
  - `proxy_set_header Accept-Encoding ""`.
  - **No `proxy_buffering` anywhere**, so nginx's default, `on`, applies.
- **`location /`** includes [`proxy.conf`](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.12.6/docker/rootfs/etc/nginx/conf.d/include/proxy.conf#L1-L7): `add_header X-Served-By`, five `proxy_set_header` lines, `proxy_pass`. No buffering or timeout directives. Because it defines `proxy_set_header`, the global `Accept-Encoding ""` is not inherited there ("inherited ... if and only if there are no `proxy_set_header` directives defined on the current level", [proxy_set_header](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_set_header)): the browser's `Accept-Encoding` reaches ddash.
- **Websockets Support** adds `proxy_set_header Upgrade`/`Connection` and `proxy_http_version 1.1`, at server level and in `location /` ([proxy_host.conf L19-L23, L39-L43](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.12.6/backend/templates/proxy_host.conf#L19-L43)). It changes nothing about buffering, and HTTP/1.1 is already the global default. Streaming works with the toggle on or off.
- **HTTP/2 Support** only switches HTTP/2 on the browser side: ` http2` on `listen 443 ssl` up to v2.12.1, `http2 on;`/`http2 off;` from v2.12.2 ([_listen.conf at v2.12.6](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.12.6/backend/templates/_listen.conf#L16-L20), [at v2.12.1](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.12.1/backend/templates/_listen.conf)). The upstream leg stays HTTP/1.1. The nginx HTTP/2 module documents no response buffering; `http2_chunk_size` (8k) only caps the size of each DATA frame ([http2_chunk_size](https://nginx.org/en/docs/http/ngx_http_v2_module.html#http2_chunk_size)).
- **Cache Assets** adds a regex location for `.css`, `.js`, image and font suffixes with `proxy_cache` ([assets.conf](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.12.6/docker/rootfs/etc/nginx/conf.d/include/assets.conf#L1-L25)). `/api/c/<component>/<Method>` never matches it.
- **Block Common Exploits**, **Force SSL** and **HSTS** are `return 403`, a 301 and an `add_header` ([block-exploits.conf](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.12.6/docker/rootfs/etc/nginx/conf.d/include/block-exploits.conf), [force-ssl.conf](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.12.6/docker/rootfs/etc/nginx/conf.d/include/force-ssl.conf), [_hsts.conf](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.12.6/backend/templates/_hsts.conf)). None affects streaming.
- **Custom Locations** carry the Operator's advanced config plus `proxy_set_header`/`proxy_pass`; no buffering directives ([_location.conf L2-L21](https://github.com/NginxProxyManager/nginx-proxy-manager/blob/v2.12.6/backend/templates/_location.conf#L2-L21)).
- Not checked: extra `conf.d` files the `nginxproxymanager/nginx-full` base image might ship. NPM's own `conf.d` holds only `default.conf` and `production.conf`, with no buffer or gzip directives.

### What nginx does with that

- **`proxy_buffering on` is the one thing that would hold a stream.** With it, nginx reads the response into `proxy_buffers` (spilling to a temp file) rather than relaying each read. "Buffering can also be enabled or disabled by passing "`yes`" or "`no`" in the "X-Accel-Buffering" response header field. This capability can be disabled using the `proxy_ignore_headers` directive." With buffering disabled, "the response is passed to a client synchronously, immediately as it is received." ([proxy_buffering](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_buffering)). NPM sets `proxy_ignore_headers` only inside the Cache Assets location, and not for `X-Accel-Buffering`. In the source, unbuffered relays mark each chunk `flush`, so `postpone_output` doesn't hold it either ([ngx_http_upstream.c L3854](https://github.com/nginx/nginx/blob/release-1.25.3/src/http/ngx_http_upstream.c#L3854), [ngx_http_write_filter_module.c L219](https://github.com/nginx/nginx/blob/release-1.25.3/src/http/ngx_http_write_filter_module.c#L219)).
- **The header stays between nginx and ddash.** "By default, nginx does not pass the header fields "Date", "Server", "X-Pad", and "X-Accel-..." from the response of a proxied server to a client" ([proxy_hide_header](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_hide_header)). Harmless when ddash is reached directly (dev, LAN): the browser ignores it.
- **gzip won't touch it.** `gzip_types` defaults to `text/html` only ([gzip_types](https://nginx.org/en/docs/http/ngx_http_gzip_module.html#gzip_types)), and NPM adds no types, so `application/connect+json` (and `application/json`) pass uncompressed. Untested: how nginx's gzip filter flushes a stream, which NPM's defaults never exercise.
- **`proxy_read_timeout` is per read, not per response**: "set only between two successive read operations, not for the transmission of the whole response. If the proxied server does not transmit anything within this time, the connection is closed." ([proxy_read_timeout](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_read_timeout)). NPM's 90 s is above `ActionDeadline` (60 s), so even an Action that is silent until its last message is not cut. nginx's own default is 60 s, equal to `ActionDeadline`, which leaves no margin if a stock nginx ever sits in front.
- **`proxy_http_version`**: stock nginx defaulted to 1.0 before 1.29.7 and 1.1 since ([proxy_http_version](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_http_version)); NPM sets 1.1 regardless. Go then answers with chunked encoding; on an HTTP/1.0 request it would close the connection to end the body instead (section 1). Either way, flushed bytes are relayed.
- **`proxy_ignore_client_abort off`**: when the browser goes, nginx closes its upstream connection, so `r.Context()` is cancelled and further writes fail. That's what the map's detached Actions must survive: the Action must not run under `r.Context()`, and the router must stop writing (not stop the Action) when a write or flush fails.

## 3. What the host must do

All of this is the host's job; the Operator changes nothing in NPM.

1. **Send `X-Accel-Buffering: no`** on every streamed response, set before the first write. This is the only change NPM needs, and it needs it: without it, NPM's default `proxy_buffering on` holds the stream.
2. **Flush after every message** (`http.NewResponseController(w).Flush()`, or `w.(http.Flusher)`), and flush once right after writing the headers so the response starts at once. Without it, `net/http` holds small messages in its 2–4 KiB buffers, behind any proxy or none. Nothing in ddash's middleware hides the flusher today; keep it that way, or give any future wrapper `Unwrap()`.
3. **Keep `Content-Type: application/connect+json` and no HTTP `Content-Encoding`.** NPM's gzip ignores that type. Connect's own per-message compression (`Connect-Content-Encoding`) is opt-in and not needed for small `RunAction` messages.
4. **Timeouts need nothing new.** The Go server has no write or idle timeout; NPM's per-read timeout is 90 s, above the 60 s `ActionDeadline` that bounds the whole stream. No heartbeat is needed while the deadline stays at or below about 60 s. If it ever grows past NPM's 90 s, the host must send something (a message) at least every 90 s.
5. `Cache-Control: no-store` is harmless but not needed: `proxy_cache` is off outside Cache Assets.
6. **Surprises for the spec:**
   - Shutdown cuts a stream after 5 s (`httpShutdownTimeout`), well inside `ActionDeadline`. The browser then sees a broken stream, even though a detached Action may still finish. The spec should say what the browser shows then (likely the same as closing the Dialog: invalidate queries, rely on the Audit Log).
   - nginx closes the upstream when the browser leaves (`proxy_ignore_client_abort off`), so writes start failing mid-Action; the detached Action must not run under `r.Context()`, and the router must treat a failed write as "stop relaying", not as the Action's outcome.
   - The router today only accepts `application/json` (`core/router/router.go:75-77`); a Connect server-streaming request arrives as `application/connect+json`, so that check must widen for `ddash.action` methods.
