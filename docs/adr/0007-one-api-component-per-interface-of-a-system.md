# One `-api` Component per interface of an external system

Each interface of an external system is reached by exactly one Component, named `<system>-api`: `pfsense-api` for pfSense's REST API, `proxmox-api` for Proxmox's, `docker-api` for the Docker socket. Every other Component that needs that interface calls the `-api` Component and never talks to the system itself. A system with several interfaces may have one `-api` Component for each, as long as none of them reaches another's interface: a Component that manages only Compose files beside `docker-api` is allowed, and when it needs the Docker socket it calls `docker-api`. We chose this so a system's credentials, its timeouts and caching, and the quirks of its answers live in one place, and so the Operator sets up each system once.

## Considered Options

- **Any Component may call any system it needs**: fewer Components, but the same credentials are entered and stored several times, each caller caches and rate-limits on its own, and a change in the system's API is fixed in several places.
- **Exactly one `-api` Component per system, whatever its interfaces**: simpler to state, but it forces unrelated interfaces with different access (a socket, files on disk) into one Component and one set of permissions.

## Consequences

- A Component that is not an `-api` Component makes no network calls of its own. `checks` is the exception by purpose: probing an address is what a Check is.
- Two Components talked to a system themselves and were split: `compose` reads image digests through `registry-api`, and `ai-helper` asks Claude through `anthropic-api`. Each keeps what is its own: compose what a digest means for a container, ai-helper its prompt, its schema and what it makes of the answer.
- A split moves no config: the Operator enters a system's credentials again in its new `-api` Component.
- A new integration starts with its `-api` Component, with a fake beside it (`<system>fake`), before the Component that holds a Plugin's logic.
- Every `-api` Component that speaks JSON over HTTP makes its calls with `sdk/jsoncall` and answers with the same gRPC code for the same kind of failure, whatever the system:

  | The system… | Code | Example |
  |---|---|---|
  | didn't answer, or its answer broke off or was too large | `Unavailable` | connection refused, a timeout, a 502 from a proxy in front of it |
  | refused the credentials | `PermissionDenied` | 401 or 403; a wrong API key, an expired token |
  | refused the request as it stands | `FailedPrecondition` | 400 on a write: a value it doesn't accept |
  | has no such object | `NotFound` | 404 on a write to an object that is gone |
  | answered something that isn't its API | `Unavailable` | HTML where JSON was expected |
  | (the request couldn't be made at all) | `Internal` | a bug in the Component, never the system's doing |

  A refused read is `Unavailable` unless it is the credentials: a getter has nothing the Operator could change in the request.
