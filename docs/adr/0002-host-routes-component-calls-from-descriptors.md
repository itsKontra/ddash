# The host routes Component calls from descriptors, not generated code

Components are downloaded at runtime (ADR 0001), so the core binary has no compiled-in Go types for their protos. Each Component's `Describe` therefore returns its `FileDescriptorSet`, and the host routes every call from it. A Component→Component call is a **transparent gRPC proxy** over go-plugin's `GRPCBroker`: an `UnknownServiceHandler` with a raw-bytes codec that checks the caller's `requires`, applies the deadline, passes Actions through the gate and forwards the bytes unchanged; it carries unary calls only. A UI→Component call at `/api/c/<component>/<Method>` is **transcoded by the host** from Connect JSON to protobuf with `dynamicpb` + `protojson`: unary for every method, and server-streaming for an Action that streams its progress, one message at a time. The descriptors also give the host each method's kind (getter or Action) and `disruptive` flag. We chose this so the host sees every method's kind at one seam, Components contain no JSON handling, and Plugins use stock `@connectrpc/connect-web` clients.

## Considered Options

- **connect-go handlers in the host** (as the core's own API uses): needs the Components' generated types compiled into the core, which ADR 0001's runtime download rules out.
- **An SDK-level `Invoke(method, json)` on every Component**: simpler for the host, but every Component would handle JSON, and the host would have no view of method kinds to audit Actions or check the Lock.
- **Progress by polling a getter** (instead of server-streaming): what App Actions first did. It needs a second RPC and a run id per Action, and shows each step up to half a second late.

## Consequences

- The host must implement the Connect unary JSON protocol faithfully (POST, `application/json`, `{code, message}` errors). `Connect-Protocol-Version: 1` doubles as the required CSRF header.
- It must implement Connect server-streaming JSON for Actions just as faithfully: POST with `application/connect+json` and one enveloped request; always HTTP 200, even for a refusal, so connect-web keeps the error's details; each message flushed in its own envelope; and an end-of-stream envelope holding `{}` or the same error JSON a unary call gets. It sends `X-Accel-Buffering: no` so Nginx Proxy Manager doesn't buffer the stream.
- Only an Action may stream, and only from the server. The SDK refuses a streaming getter and client or bidirectional streaming when a Component describes itself, and the Component→Component proxy refuses a streaming method with `Unimplemented`.
- A `FileDescriptorSet` must include its imports (e.g. well-known types), or transcoding fails.
- Details: [Host architecture: core module layout, collectors and the HTTP API](https://github.com/itsKontra/ddash/issues/12); streaming in [Wayfinder: stream Action progress (RunAction off polling)](https://github.com/itsKontra/ddash/issues/162).
