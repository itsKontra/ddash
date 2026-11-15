# One `-api` Component per interface of an external system

Each interface of an external system is reached by exactly one Component, named `<system>-api`: `pfsense-api` for pfSense's REST API, `proxmox-api` for Proxmox's, `docker-api` for the Docker socket. Every other Component that needs that interface calls the `-api` Component and never talks to the system itself. A system with several interfaces may have one `-api` Component for each, as long as none of them reaches another's interface: a Component that manages only Compose files beside `docker-api` is allowed, and when it needs the Docker socket it calls `docker-api`. We chose this so a system's credentials, its timeouts and caching, and the quirks of its answers live in one place, and so the Operator sets up each system once.

## Considered Options

- **Any Component may call any system it needs**: fewer Components, but the same credentials are entered and stored several times, each caller caches and rate-limits on its own, and a change in the system's API is fixed in several places.
- **Exactly one `-api` Component per system, whatever its interfaces**: simpler to state, but it forces unrelated interfaces with different access (a socket, files on disk) into one Component and one set of permissions.

## Consequences

- A Component that is not an `-api` Component makes no network calls of its own. `checks` is the exception by purpose: probing an address is what a Check is.
- Two Components break the rule today and need an `-api` Component split out of them: `compose` talks to container registries (`components/compose/images.go`), and `ai-helper` talks to Anthropic (`components/ai-helper/ai_helper.go`).
- A new integration starts with its `-api` Component, with a fake beside it (`<system>fake`), before the Component that holds a Plugin's logic.
