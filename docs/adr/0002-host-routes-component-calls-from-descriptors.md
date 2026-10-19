# The host routes Component calls from descriptors, not generated code

Components are downloaded at runtime (ADR 0001), so the core binary has no compiled-in Go types for their protos. Each Component's `Describe` therefore returns its `FileDescriptorSet`, and the host routes every call from it. A Component→Component call is a **transparent gRPC proxy** over go-plugin's `GRPCBroker`: an `UnknownServiceHandler` with a raw-bytes codec that checks the caller's `requires`, applies the deadline, passes Actions through the gate and forwards the bytes unchanged. A UI→Component call at `/api/c/<component>/<Method>` is **transcoded by the host** from Connect unary JSON to protobuf with `dynamicpb` + `protojson`. The descriptors also give the host each method's kind (getter or Action) and `disruptive` flag. We chose this so the host sees every method's kind at one seam, Components contain no JSON handling, and Plugins use stock `@connectrpc/connect-web` clients.

## Considered Options

- **connect-go handlers in the host** (as the core's own API uses): needs the Components' generated types compiled into the core, which ADR 0001's runtime download rules out.
- **An SDK-level `Invoke(method, json)` on every Component**: simpler for the host, but every Component would handle JSON, and the host would have no view of method kinds to audit Actions or check the Lock.

## Consequences

- The host must implement the Connect unary JSON protocol faithfully (POST, `application/json`, `{code, message}` errors). `Connect-Protocol-Version: 1` doubles as the required CSRF header.
- A `FileDescriptorSet` must include its imports (e.g. well-known types), or transcoding fails.
- Details: [Host architecture: core module layout, collectors and the HTTP API](https://github.com/itsKontra/ddash/issues/12).
