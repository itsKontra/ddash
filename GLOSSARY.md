# ddash

A self-hosted admin dashboard for one home network: it shows and controls the apps, hosts, and network services running on it.

## Language

### Extending ddash

**Plugin**:
What the operator installs to add a feature: its Widgets, Nav Entries and Pages, plus the Components it requires. Has no backend logic of its own, and is never required by anything.
_Avoid_: Module, extension, add-on

**Component**:
A single-purpose backend unit, such as access to one external system or one Plugin's logic and state. Never installed on its own: it arrives because a Plugin or another Component requires it. Shared by everything that requires it.
_Avoid_: Plugin (for backend units), service, module

**Widget**:
A card a Plugin offers for the startpage.
_Avoid_: Tile, card (as the defined term)

**Nav Entry**:
A link a Plugin adds to the left navigation, leading to one of its Pages.
_Avoid_: Menu item, route

**Page**:
A full screen a Plugin provides, the target of a Nav Entry.
_Avoid_: View, screen

**Lock**:
An app-wide exclusive claim one Plugin holds during disruptive work (e.g. a firewall update), which keeps everything else from interfering until it is released.
_Avoid_: Maintenance (that is a Check term), mutex

**Release**:
One version of ddash as a whole: the core together with every Plugin and Component. Installed Plugins always run the same Release as the core, and updating ddash updates them all at once.
_Avoid_: Plugin version, Component version

**Theme**:
A named set of look-and-feel values (colours for light and dark, corner radius, font family) the operator picks for the whole UI. Defined by the core only, and applied to every Plugin alike.
_Avoid_: Skin, style

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
A recurring probe of a target (an App or any address) that yields up or down plus a latency. Every App has one automatically; any other target needs a manual Check. Workloads have no Checks of their own.
_Avoid_: Monitor, health check, ping

**Maintenance**:
A period during which a Check's results don't count toward uptime and trigger no notifications. Started by the operator, or automatically when ddash itself restarts or stops a Workload.
_Avoid_: Downtime, pause

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
