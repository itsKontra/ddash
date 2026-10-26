# Connect server-streaming over JSON: what the host and connect-web require

Research for [#163](https://github.com/itsKontra/ddash/issues/163), under the map [#162](https://github.com/itsKontra/ddash/issues/162). Baseline: `main` at `ef4f067`.

## Sources

- **Spec**: Connect protocol, <https://connectrpc.com/docs/protocol>, read from the site source `connectrpc/connectrpc.com@1d8bc63`, `src/content/docs/docs/protocol.md`. Sections: [Streaming-Request](https://connectrpc.com/docs/protocol#streaming-request), [Streaming-Response](https://connectrpc.com/docs/protocol#streaming-response), [Error and EndStreamResponse](https://connectrpc.com/docs/protocol#error-end-stream), [HTTP to Error Code](https://connectrpc.com/docs/protocol#http-to-error-code).
- **Client**: connect-es **v2.2.0**, the version pinned in `packages/plugin/package.json` and `pnpm-lock.yaml` for both `@connectrpc/connect` and `@connectrpc/connect-web` (tag `v2.2.0`, commit `378bb8f`). Paths below are relative to <https://github.com/connectrpc/connect-es/blob/v2.2.0/packages/>.
- **Server reference**: connect-go **v2.0.1** (`connectrpc.com/connect/v2`, pinned in `go.mod`; tag `v2.0.1`, commit `f86de81`). Paths relative to <https://github.com/connectrpc/connect-go/blob/v2.0.1/>.
- **Go**: `net/http` docs for Go 1.26 (`go.mod` says `go 1.26.9`).
- **nginx**: <https://nginx.org/en/docs/http/ngx_http_proxy_module.html>. ddash sits behind an nginx Proxy Host (`docs/deployment.md`, step 3).
- **ddash**: `core/router/router.go`, `core/router/errors.go`, `core/host.go`, `cmd/ddash/main.go`, `core/appstate/appstate.go`, `packages/plugin/src/transport.ts`, `packages/plugin/src/action.ts`, `web/src/reach.ts`, `docs/adr/0002-host-routes-component-calls-from-descriptors.md`.

## Answer in brief

A server-streaming call is a POST with `Content-Type: application/connect+json` and a body of **one enveloped message** (5-byte header: flags byte `0`, 4-byte big-endian length, then the JSON). connect-web still sends `Connect-Protocol-Version: 1`, so the CSRF check in `core/host.go` holds unchanged. The response is **always HTTP 200** with `Content-Type: application/connect+json`. Its body is zero or more data envelopes (flags `0`), each flushed as written, and then exactly one end-of-stream envelope (flags `2`) whose JSON is `{}` on success or `{"error":{"code","message","details":[{"type","value"}]}, "metadata":{...}}` on failure. The error object is the shape `WriteError` already writes for unary. **Every error the router produces for a streaming method, including refusals before the first message, must go in that end-of-stream envelope under a 200.** connect-web turns a non-200 stream response into a bare `HTTP <status>` error with no message and no details, and maps 400 to `internal`. On the client side, an end-of-stream error surfaces as a `ConnectError` with `details` intact, so `findDetails` works as it does for unary. An `AbortSignal` (or a client timeout) cancels the fetch and rejects the iterator with `canceled` (or `deadline_exceeded`). Breaking out of `for await` does **not** cancel the fetch. The Plugin transport's `stream()` only needs to route like `unary()` does: `forService(method.parent.typeName).stream(...)`. The existing interceptors (`requireConfirm`, `naming`) already run on streams.

## 1. Request

| Item | Requirement | Source |
| --- | --- | --- |
| Method | `POST`. Path `/<prefix>/<package.Service>/<Method>`, as for unary. | Spec, Streaming-Request (`Method-Post Path`) |
| Content-Type | `application/connect+json`; the spec's grammar allows `application/connect+<codec>`. connect-es matches `^application\/connect\+?(?:json(?:; ?charset=utf-?8)?|proto)$`. | Spec, Streaming-Request; `connect/src/protocol-connect/content-type.ts` (`contentTypeStreamJson`, `contentTypeStreamRegExp`); `connect/src/protocol-connect/request-header.ts` L50-60 |
| Wrong Content-Type | "If Streaming-Content-Type does not begin with `application/connect+`, servers should respond with an HTTP status of 415", so a client unaware of Connect can't read a 200-with-error as success. connect-go keys each procedure's accepted content types by stream type, so a unary procedure 415s `application/connect+json` and a streaming one 415s `application/json` (`connecthttp/handler.go` `ServeHTTP`, `protocol_connect.go` `CanHandlePayload`). | Spec, Streaming-Request |
| `Connect-Protocol-Version: 1` | Optional in the grammar for streaming as for unary. connect-es **always** sets it, for every method kind (`request-header.ts` L61). connect-go enforces it only for unary (`protocol_connect.go`, `NewConn`: `required := h.RequireConnectProtocolHeader && (h.spec.StreamType == connect.StreamTypeUnary)`), but that is connect-go's choice. ddash's `requireConnectHeader` (`core/host.go`) can keep requiring it on every `/api` call. `application/connect+json` isn't a CORS-safelisted type either, so a cross-site stream preflights twice over. | Spec; connect-es; connect-go |
| `Connect-Timeout-Ms` | Sent only when the caller passes `timeoutMs` (or the transport has `defaultTimeoutMs`; ddash sets neither). Servers "may clamp timeouts". The router ignores it today and applies its own `Deadline(md)`, which is allowed. | Spec, Unary-Request (shared rules); `request-header.ts` L48-50 |
| Compression | `Connect-Content-Encoding` / `Connect-Accept-Encoding` (not `Content-Encoding`). connect-web sends neither, so identity applies and flag bit 0 must be 0. connect-web throws `internal` "received unsupported compressed output" if a response envelope has it set. | Spec, Streaming-Request; `connect-web/src/connect-transport.ts` L270-275 |
| Body | Zero or more `Enveloped-Message`s: `Envelope-Flags` (1 byte), `Message-Length` (4 bytes, big-endian, unsigned), `Message`. For server streaming, connect-web sends **exactly one** envelope with flags `0` and the request as JSON (`createRequestBody`, `connect-transport.ts` L308-319). The fetch API can't stream request bodies, so connect-web throws (a string, which becomes an `unknown` `ConnectError`) for client and bidi streaming before any request is made (L311-313). | Spec; connect-es |
| End-stream bit in requests | "Request streams must always leave this bit unset." | Spec, Streaming-Request |

What the router has to change on the request side: today it 415s anything but `application/json` (`core/router/router.go`, `ServeHTTP`) and `io.ReadAll`s the body straight into `protojson`. For a streaming method it must accept `application/connect+json`, read one 5-byte header, check flags `== 0` and the length against `maxRequestBytes`, read that many bytes, and unmarshal them as it does now. It should reject a unary method called with `application/connect+json` and a streaming method called with `application/json` (415, as connect-go does). Bytes after the first envelope can be refused as `invalid_argument`. connect-web never sends them.

## 2. Response

### Framing and headers

- `:status 200` and the streaming Content-Type (`Streaming-Response-Headers → ":status 200" Streaming-Content-Type ...`). "Streaming responses always have an HTTP status of 200 OK." (Spec, Streaming-Response.) connect-go echoes the request's Content-Type (`protocol_connect.go`, `NewConn`: `header[headerContentType] = []string{contentType}`).
- connect-web checks the 200's Content-Type: it must parse as `stream: true, binary: false`, i.e. `application/connect+json`. Otherwise it throws `unsupported content type` with `internal` (`connect/src/protocol-connect/validate-response.ts` L59-71).
- Body: `1*Enveloped-Message`. Each data message is `<0x00><uint32 BE len><protojson bytes>`. The **final** envelope has flag `0x02` (the second-least-significant bit) and carries the EndStreamResponse. The server "must send an EndStreamResponse as the final message in the stream, and must not send an EndStreamResponse earlier". (Spec, Streaming-Request flags and Streaming-Response.) gRPC-Web uses `0x80` instead, and the spec calls out the difference.
- connect-go writes `Connect-Accept-Encoding` on streaming responses; it is optional (`[Streaming-Accept-Encoding]`) and connect-web doesn't read it.

### End-of-stream JSON

From the spec's [Error and EndStreamResponse](https://connectrpc.com/docs/protocol#error-end-stream):

```json
{"error": {"code": "failed_precondition", "message": "…", "details": [{"type": "ddash.apps.RunActionResponse", "value": "<unpadded std base64>"}]},
 "metadata": {"some-key": ["v1"]}}
```

- Success: `error` omitted. `{}` is a valid success end-stream. `{"error": null}`, `{"error": {}}` and `{"error": {"code": null}}` are invalid.
- `error` is exactly the unary error object: `code` (snake_case string), optional `message`, optional `details[]` of `{type, value, debug?}`, where `type` is the fully-qualified message name and `value` is unpadded base64 of the binary protobuf. `core/router/errors.go` already builds this object (`WriteError`: `base64.RawStdEncoding`, type name stripped of the `type.googleapis.com/` prefix, "as connect-go writes them"). The streaming path should reuse that serialisation and wrap it as `{"error": …}` in a flag-2 envelope, not write it as a bare body with a non-200 status.
- `metadata` is optional: an object of header-name → array of strings. connect-go writes it from the response trailer (`connectEndStreamMessage{Error, Trailer \`json:"metadata,omitempty"\`}`, `connecthttp/protocol_connect.go` L1333). ddash has no trailers to send and can omit it.
- connect-go builds the end-stream the same way (`MarshalEndStream`, `protocol_connect.go` L884; `newConnectWireError` L1279). If the end-stream exceeds the send limit, connect-go retries with the details dropped. ddash's details are small.

### HTTP status when an error comes before or after the first message

- **Both cases: 200 with an end-stream error.** The spec has no non-200 streaming response. connect-go's `ErrorWriter`, given a Connect-streaming request, writes `200` + the request's Content-Type + one end-stream envelope even when nothing else was sent (`connecthttp/error_writer.go` L143, `writeConnectStreaming`). Its normal handler path also turns any handler error into `Close(err)` → `MarshalEndStream` (`protocol_connect.go` L859).
- **Why it matters for ddash**: connect-web, on a non-200 streaming response, **does not read the body**. It throws `new ConnectError("HTTP <status>", codeFromHttpStatus(status))` (`validate-response.ts` L47-58; only *unary* JSON non-200s are parsed for an error body). `codeFromHttpStatus` maps 400 → `internal`, 404 → `unimplemented`, 403 → `permission_denied`, 409/412/499/5xx → `unknown`/`unavailable` (`http-status.ts`; spec's HTTP-to-Error-Code table). So if the router answered a streaming refusal (scope, Lock, Protected, decode error, `Unimplemented`, "changed since you looked") with today's `WriteError`, the Plugin would lose the message, the code and every detail.
- Responses produced **before the router** stay non-200 and that's fine: the session guard's 401 (connect-web gives `unauthenticated`, and `web/src/reach.ts` reloads on any 401 regardless of body) and `requireConnectHeader`'s 400 (only a non-connect-web caller can hit it; it would read as `internal` "HTTP 400").
- `web/src/reach.ts` (`HostWatch`) marks ddash unreachable on a 5xx from `/api/c/` unless the body is `application/json`. A streaming error is a 200, so it never trips this. If the stream dies mid-body, the fetch promise has already resolved, so `HostWatch` doesn't see that either.

### Flushing

- Go's `ResponseWriter` buffers. Headers go out on the first `Write` or `Flush` and data is held "under a few KB" until a flush (`go doc net/http.ResponseWriter`). The handler must **flush after every envelope**, data and end-stream alike. connect-go does exactly this: `Send` and `Close` both `defer flushResponseWriter(...)` (`protocol_connect.go` L843, L859). It refuses to serve a server-stream at all if the writer isn't an `http.Flusher` (`connecthttp/protocol.go` L356, `checkServerStreamsCanFlush`). ddash's middleware chain (`forwarded`, the auth guard, `requireConnectHeader`) passes `w` through unwrapped, so `w.(http.Flusher)` holds today. `http.NewResponseController(w).Flush()` survives future wrappers that implement `Unwrap`.
- Flushing the 200 header early (before the Component's first message) is allowed and lets `transport.stream()` resolve sooner: the fetch resolves on headers. If the host does that, every later error must be an end-stream envelope. It must be one anyway (above).
- **nginx in front**: `proxy_buffering` is on by default. With buffering on, nginx reads the upstream response into its buffers. "When buffering is disabled, the response is passed to a client synchronously, immediately as it is received." Buffering "can also be enabled or disabled by passing `yes` or `no` in the `X-Accel-Buffering` response header field". Otherwise progress may arrive in one lump at the end. The host should set `X-Accel-Buffering: no` on streaming responses rather than depend on the Proxy Host's advanced config. `proxy_read_timeout` defaults to 60 s **between two reads**, not for the whole response. That equals `ActionDeadline`, so a Component that stays silent for the whole 60 s gets nginx's cut and the host's deadline at about the same moment. Sending progress keeps the timer reset. (nginx `ngx_http_proxy_module` docs.)
- `cmd/ddash/main.go` sets only `ReadHeaderTimeout` on the `http.Server`, with no `WriteTimeout`, so Go itself won't cut a 60 s stream.

### Missing or truncated end-stream

If the body ends without a flag-2 envelope, connect-web throws the string `"missing EndStreamResponse"`, which `runStreamingCall` wraps as `unknown` (`connect-transport.ts` L304; `ConnectError.from` in `connect/src/connect-error.ts` L121). A body that ends mid-envelope gives `invalid_argument` "protocol error: incomplete envelope" (`connect/src/protocol/envelope.ts` L180-187). So the host must always finish with an end-stream envelope, including on Component failure and deadline.

## 3. Client: connect-web v2.2.0 behaviour

- **Entry**: `transport.stream(method, signal, timeoutMs, header, input, contextValues)` returns `Promise<StreamResponse>` whose `.message` is an `AsyncIterable` (`connect/src/transport.ts` L47-54). `createClient(...)`'s server-streaming method wraps the single request in `createAsyncIterable([input])` and calls `transport.stream` (`connect/src/promise-client.ts` L108-127). connect-web's `stream` POSTs with `redirect: "error"` and the transport's `fetch` (`connect-transport.ts` L128, L346-354), so ddash's `HostWatch.fetch` sees it.
- **Interceptors** run on streams too: `runStreamingCall` → `applyInterceptors` (`connect/src/protocol/run-call.ts` L90-100). The request object has `method`, `header` and `contextValues`, so `requireConfirm` and `naming` in `packages/plugin/src/transport.ts` work unchanged for a streaming Action.
- **Data envelopes** are parsed with `fromJson(method.output, …)` and yielded one by one (`parseResponseBody`, `connect-transport.ts` L256-305).
- **End-stream error**: `endStreamFromJson` → `errorFromJson`, which builds `new ConnectError(message, code, metadata)` with `isWireError = true` and pushes each detail as `{type, value: base64Decode(value), debug}` (`connect/src/protocol-connect/end-stream.ts` L50; `error-json.ts` L32-80). The response headers are appended to `error.metadata`, and the error is **thrown from the iterator's `next()`**, after any messages already yielded (`connect-transport.ts` L276-285). `run-call.ts`'s `abort` passes a `ConnectError` through `ConnectError.from` unchanged, so `err.findDetails(RunActionResponseSchema)` works exactly as for a unary error: it matches `details[].type` against the schema's `typeName` (`connect-error.ts` L172).
- **End-stream success**: its `metadata` becomes the response `trailer`; iteration ends.
- **Abort**: the caller's `signal` is linked with a deadline signal into one `AbortController` whose signal goes to `fetch` (`run-call.ts` `setupSignal` L173-200; `signals.ts`). After an abort, the next `next()` rejects with `ConnectError.from(signal.reason, Code.Canceled)`. With no reason, or the browser's `AbortError`, that's `canceled` (`connect-error.ts` L125-129). A `timeoutMs` fires `deadline_exceeded` "the operation timed out" locally (`signals.ts` L73) and sends `Connect-Timeout-Ms`. Aborting closes the fetch, which closes the connection (or the HTTP/2 stream) to nginx. nginx then closes its upstream connection (`proxy_ignore_client_abort off` is the default), and the host's `r.Context()` is cancelled.
- **Early `break`**: the wrapped iterator "deliberately omit[s] throw/return" (`run-call.ts` L151). Leaving a `for await` loop early does **not** abort the fetch, so the hook must hold its own `AbortController` and abort on unmount or Dialog close.
- On normal completion `done()` aborts the internal controller, which releases the fetch. That is harmless.
- **No compression**: connect-web refuses compressed response envelopes (above), so the host must never set flag bit 0.

## 4. The ddash Plugin transport's `stream()`

Today (`packages/plugin/src/transport.ts`):

```ts
async stream() {
  throw new ConnectError("ddash methods are unary only", Code.Unimplemented);
},
```

What it must do, mirroring `unary`:

```ts
async stream(method, ...rest) {
  return forService(method.parent.typeName).stream(method, ...rest);
},
```

- Routing and the `Ddash-Plugin` header come from `forService` and the `naming` interceptor, unchanged. `createConnectTransport` sets the streaming Content-Type and `Connect-Protocol-Version` itself.
- Confirm: `requireConfirm` already sees `req.method` and `req.contextValues` on a `StreamRequest`, so a disruptive streaming Action without a confirm text is refused in the browser, as for unary. `useStreamingAction` passes `contextValues` (with `confirmed`) as the sixth argument, as `useAction` does for `unary`.
- Optional local refusals, to match the map's scope: refuse `method.methodKind !== "server_streaming"` with `Unimplemented`. connect-web would otherwise throw an `unknown` string error for client and bidi streams. Possibly also refuse a method without the `ddash.action` option. The host must refuse both anyway, as `Unimplemented` inside a 200 end-stream.
- `@connectrpc/connect-query` 2.3.1 has no streaming hooks of its own: its dist exports only `useQuery`, `useInfiniteQuery`, `useMutation` and `useTransport`, and nothing in it calls `transport.stream`. So `useStreamingAction` takes the transport from `useTransport()` and calls `transport.stream(...)` (or `createClient`). It owns an `AbortController`, passes no `timeoutMs` so the host's `ActionDeadline` governs, sets `last` per yielded message, resolves `run` to the last one, and treats a `canceled` error from its own abort as "cancelled" rather than an error.
- `coreTransport()` needs no change; no core method streams.

## 5. Host side: what the router needs (facts, not design)

- **Kind check**: `md.IsStreamingServer()` / `md.IsStreamingClient()` from `protoreflect.MethodDescriptor` give the kind. Server-streaming plus `appstate.IsAction(md)` is the only streaming kind the map allows. The rest are `Unimplemented`, written as an end-stream under 200 when the request was `application/connect+json`.
- **Upstream**: the Component call becomes `ep.Conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, fullMethod)`, then `SendMsg(req)`, `CloseSend()`, and a loop of `RecvMsg` until `io.EOF`. Each message is marshalled with `protojson.MarshalOptions{Resolver: ep.Types}` into a flag-0 envelope and flushed. The terminal error, a gRPC status with details, goes through the same code-and-detail mapping as `WriteError` into the flag-2 envelope. `done(err)` (the gate's Audit Log entry) is called once with the terminal error.
- **Proxy** (`core/router/proxy.go`): Component-to-Component streaming is out of scope; its `UnknownServiceHandler` already sees streams and should refuse them as `Unimplemented`.

## 6. Constraints on detaching an Action once the browser has gone

These bear on the map's "Not yet specified" item.

1. **The request context is the wrong parent.** Per `go doc net/http.Request.Context`: "For incoming server requests, the context is canceled when the client's connection closes, the request is canceled (with HTTP/2), or when the ServeHTTP method returns." Today `ep.Conn.Invoke` runs under `context.WithTimeout(onward(r.Context(), …), Deadline(md))` (`router.go`), which is why a closed tab cancels an Action. A detached Action needs `context.WithoutCancel(r.Context())` (Go 1.21+; keeps values, drops cancellation) wrapped in `ActionDeadline`. The gate's `done` already records under `context.WithoutCancel(ctx)` (`core/appstate/appstate.go`, `Admit`), so the Audit Log write survives a gone browser.
2. **The ResponseWriter dies with ServeHTTP.** Per `go doc net/http.Handler`: "it is not valid to use the ResponseWriter or read from the Request.Body after or concurrently with the completion of the ServeHTTP call." The simplest valid shape is to **keep draining inside the handler**: keep calling `RecvMsg` until the Component's stream ends, and stop writing (or ignore write and flush errors) once the first write fails or `r.Context()` is done. Draining in a separate goroutine means ServeHTTP must never write after it returns.
3. **Writes to a gone client fail, they don't block forever.** Once nginx closes the upstream connection, `Write` and `Flush` return errors. With nginx in front, the host learns the browser left only through nginx: `proxy_ignore_client_abort off`, the default, closes upstream on client abort.
4. **Read the request body before anything else.** `go doc net/http.Handler` warns that reading the body after writing may not work. The router already reads the whole request before invoking. Keep that order: read the one envelope, then write.
5. **Shutdown.** `cmd/ddash/main.go` `drain` runs `srv.Shutdown` (5 s `httpShutdownTimeout`, then `srv.Close()`) in parallel with `StopComponents` (`runtime.StopTimeout`, 10 s, then kill), then closes the core DB. `Server.Close` closes connections but doesn't stop handler goroutines. A handler still draining after 5 s therefore keeps running until the Component's stream ends, which `StopComponents` forces within 10 s. If `done(err)` runs after `host.Close()`, the Audit Log write fails silently (`_, _ = g.db.ExecContext`). Before closing the DB, the host has to wait for in-flight admitted Actions, e.g. a WaitGroup the router holds. Otherwise an Action cut short by shutdown can go unaudited. Whatever the spec decides, the outcome an interrupted Action records is the Component's error (most likely `unavailable` or `canceled` from the killed connection).
6. **Unary Actions too.** The map says every admitted Action runs to its end, unary or streaming. Points 1, 2 and 5 apply equally to the unary `Invoke` path in `router.go`.
