# ddash

A self-hosted admin dashboard for one home network: it shows and controls the apps, hosts, and network services running on it.

## Language

### Apps and what runs them

**App**:
Something the operator opens in a browser, identified by its URL (e.g. Jellyfin, Grafana). Either discovered from a Proxy Host or added manually. Backed by zero or one Workload.
_Avoid_: Service, container, link

**Workload**:
A unit of compute that runs software: a Docker container, a Proxmox LXC, a Proxmox VM, or an external host. One Workload can back zero or many Apps.
_Avoid_: Container (when the kind is not specifically Docker), guest, instance

**Proxy Host**:
An nginx server block that forwards requests to an upstream. Each Proxy Host is the source of exactly one discovered App.
_Avoid_: Virtual host, site, vhost

### Monitoring

**Check**:
A recurring probe of a target (an App or any address) that yields up or down plus a latency.
_Avoid_: Monitor, health check, ping

### Network

**VPN Tunnel**:
The outbound tunnel from the firewall to a VPN provider, used as a gateway for selected traffic.
_Avoid_: VPN (unqualified), VPN service

**Tunnelled**:
Said of a network device whose traffic the firewall routes through the VPN Tunnel, by its membership in the tunnel's alias.
_Avoid_: On the VPN

**Remote Access**:
WireGuard connectivity that lets the operator's own devices reach the home network from outside it.
_Avoid_: VPN (unqualified), road warrior
