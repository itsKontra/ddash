# An admitted Action runs to its end

Once the gate admits an Action and writes its Audit Log entry, the Action runs to its end under `ActionDeadline`, whatever its caller does. A browser that leaves (a closed tab, a closed Plan Dialog, an aborted stream, a dropped connection) only stops the host relaying the answer. The Action keeps running, and its entry records how it really ended. This holds host-wide, for unary and streaming Actions, wherever the gate admits one: UI calls through the router, Component→Component hops, the core API's own Actions, and `RunTestAction`. Getters are unchanged: a getter whose caller left is cancelled. We chose this because an Action cancelled between steps leaves the systems half-done on purpose, and its entry then says "canceled" about a change that partly happened. A browser leaving isn't a reliable "stop" either: Nginx Proxy Manager closes the upstream connection whenever the browser goes, so the request's context ends for reasons the Operator never chose.

## Considered Options

- **Cancel the Action with the request** (what ddash did first): closing the tab mid-Action stops it at whatever step it reached, with nothing rolled back.
- **A Cancel that stops a running Action between steps**: lets the Operator stop a mistake, but App Actions already stop only at a failed step with nothing rolled back, so it would add half-done states on purpose.
- **Background jobs the browser can reattach to**: needs a job store and a reattach UI for Actions that last seconds; the Audit Log and the next sync already say how one ended.

## Consequences

- Every admission site runs the Action under `context.WithoutCancel` of the request's context plus `ActionDeadline`, keeping its values (actor, chain). A streamed Action's handler keeps reading the Component's stream after a failed write, and only stops writing.
- An Operator who closed the Plan Dialog sees the outcome through the next read and the Audit Log. There is no toast and no reattach.
- **Shutdown** lets admitted Actions finish. On SIGTERM ddash stops accepting HTTP and waits for in-flight requests, and with them every admitted Action, for up to `ActionDeadline` + 5 s. Only then does it stop Components (`runtime.StopTimeout`), and the core DB closes last. An idle ddash still stops at once. compose's `stop_grace_period` is 90 s, and the Operator copies that into their own compose file.
- **Killed anyway** (SIGKILL, a second interrupt, power loss): the entry is marked Interrupted at the next start, as before. There is no resume and no rollback. The App's Warnings show whatever is half-done.
- Details: [Wayfinder: stream Action progress (RunAction off polling)](https://github.com/itsKontra/ddash/issues/162), shutdown in [What happens to a running Action and its Audit Log entry when ddash shuts down?](https://github.com/itsKontra/ddash/issues/166).
