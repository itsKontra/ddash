# ddash

A self-hosted admin dashboard for one home network: it shows and controls the apps, hosts, and network services running on it.

## Language

### People

**Operator**:
The one person who runs ddash and the home network, and the only kind of user ddash has.
_Avoid_: Admin, user

**Action**:
A change ddash makes to a home-network system or to its own state, such as Adopting a Device or restarting a Workload. A disruptive Action asks the Operator to confirm first.
_Avoid_: Command, mutation, write, operation

### Extending ddash

**Plugin**:
What the operator installs to add a feature: its Widgets, Nav Entries and Pages, plus the Components it requires. Has no backend logic of its own, and is never required by anything.
_Avoid_: Module, extension, add-on

**Component**:
A single-purpose backend unit, such as access to one external system or one Plugin's logic and state. Never installed on its own: it arrives because a Plugin or another Component requires it. Shared by everything that requires it.
_Avoid_: Plugin (for backend units), service, module

**Overview**:
The startpage: a status summary over a grid of Widgets the operator arranges. Owned by the core, not by any Plugin.
_Avoid_: Dashboard, home, startpage (as the defined term)

**Widget**:
A card a Plugin offers for the Overview. It declares the sizes it supports and reports a status that the Overview summarises.
_Avoid_: Tile, card (as the defined term)

**Nav Entry**:
A link a Plugin adds to the left navigation, leading to one of its Pages. May carry a live badge, such as a count of what is down.
_Avoid_: Menu item, route

**Page**:
A full screen a Plugin provides, the target of a Nav Entry.
_Avoid_: View, screen

**Lock**:
An app-wide exclusive claim one Component holds on the Operator's behalf during disruptive work (e.g. a firewall update), which keeps everything else from interfering until it is released.
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

**Notification**:
A message ddash pushes to the Operator's phone when something changes that they should know about, such as a Check going down or recovering.
_Avoid_: Alert, push

**Flapping**:
Said of a Check that keeps going down and recovering in a short time. It sends one Notification instead of one per change.
_Avoid_: Unstable, bouncing

### Network

**VPN Tunnel**:
The outbound path from the firewall to the VPN provider: a primary tunnel with a fallback tunnel that takes over when the primary fails. The default route for every Device's internet traffic.
_Avoid_: VPN (unqualified), VPN service, Proton

**Tunnelled**:
Said of a Device whose internet traffic leaves through the VPN Tunnel. The default for every Device.
_Avoid_: On the VPN

**Direct**:
Said of a Device exempted from the VPN Tunnel, whose internet traffic leaves straight through the WAN, by its membership in the firewall's bypass alias.
_Avoid_: USE_WAN, bypass, split-tunnel, untunnelled

**Remote Access**:
WireGuard connectivity that lets the operator's own devices reach the home network from outside it.
_Avoid_: VPN (unqualified), road warrior

**Device**:
A piece of hardware on the home network that the firewall hands addresses to, identified by its MAC address. Servers with fixed addresses and Remote Access peers are not Devices.
_Avoid_: Client, host, machine

**New Device**:
A Device holding only a temporary address lease, with no reserved address yet.
_Avoid_: Unknown device, unrecognised device

**Adopt**:
To give a New Device a name and a reserved address, so it stops being new.
_Avoid_: Register, approve, claim
