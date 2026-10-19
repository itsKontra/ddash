# ddash

A self-hosted admin dashboard for one home network: it shows and controls the apps, hosts, and network services running on it.

## Language

### Apps and what runs them

**App**:
Something the operator opens in a browser, identified by its URL (e.g. Jellyfin, Grafana). Backed by zero or one Workload.
_Avoid_: Service, container, link

**Workload**:
A unit of compute that runs software: a Docker container, a Proxmox LXC, a Proxmox VM, or an external host. One Workload can back zero or many Apps.
_Avoid_: Container (when the kind is not specifically Docker), guest, instance

### Monitoring

**Check**:
A recurring probe of a target (an App or any address) that yields up or down plus a latency.
_Avoid_: Monitor, health check, ping
