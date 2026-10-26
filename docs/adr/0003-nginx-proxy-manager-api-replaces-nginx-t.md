# Nginx Proxy Manager's API replaces `nginx -T` over SSH

ddash read nginx's effective config by running `nginx -T` over SSH and parsing it, which works with any nginx but can only read. Apps now creates, edits and deletes Proxy Hosts and picks their certificates, so ddash talks only to Nginx Proxy Manager's API (`nginx-proxy-manager-api`) and the `nginx-config` Component and its SSH key are removed. We chose writes over generality: the Operator's one Reverse Proxy is an Nginx Proxy Manager, and hand-editing its generated config files from ddash would fight Nginx Proxy Manager's own database.

## Considered Options

- **Keep `nginx -T` for reads and use the API only for writes**: two views of the same Proxy Hosts that can disagree, and two sets of credentials to set up.
- **Write nginx config files over SSH**: works with any nginx, but Nginx Proxy Manager regenerates its files from its database and would overwrite them.

## Consequences

- A plain nginx (without Nginx Proxy Manager) can no longer back Apps.
- Nothing in the API reports whether the whole config is healthy: a failed `nginx -t` shows only as `meta.nginx_online=false` on the write that caused it.
- Details: [Which Components own Nginx Proxy Manager access, Host Overrides, multi-step App Actions and Warnings?](https://github.com/itsKontra/ddash/issues/138)
