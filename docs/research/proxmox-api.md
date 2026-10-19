# Proxmox VE API: listing and controlling guests with a least-privilege token

Research for [#4](https://github.com/itsKontra/ddash/issues/4). Checked 2026-10-19 against
the live Proxmox VE API schema (current release: PVE 9.2), the `pveum` admin guide, the
Perl sources of `pve-manager`, `pve-access-control`, `pve-container` and `qemu-server`
(git.proxmox.com, `HEAD`), and `luthermonson/go-proxmox` v0.8.2.

## Answer in brief

- **List every Workload in one call:** `GET /cluster/resources?type=vm` returns both QEMU VMs
  and LXCs (`type` is `qemu` or `lxc`) with `vmid`, `name`, `node`, `status`, `cpu`/`maxcpu`,
  `mem`/`maxmem`, `disk`/`maxdisk`, `netin`/`netout`, `uptime`, `tags`, `template`, `lock`.
  The list is silently filtered to guests on which the caller has `VM.Audit`.
- **Control:** `POST /nodes/{node}/{qemu|lxc}/{vmid}/status/{start|stop|shutdown|reboot}`.
  Each needs `VM.PowerMgmt` on `/vms/{vmid}` and returns a task UPID, not a result; poll
  `GET /nodes/{node}/tasks/{upid}/status` until `status=stopped`, then read `exitstatus`
  (`OK` on success). A token may read its own tasks without extra privileges.
- **IPs:** LXC: `GET /nodes/{node}/lxc/{vmid}/interfaces` (needs `VM.Audit`). QEMU:
  `GET /nodes/{node}/qemu/{vmid}/agent/network-get-interfaces` (needs `VM.GuestAgent.Audit`
  on PVE 9; it was `VM.Monitor` on PVE 8). Both only work while the guest runs; QEMU also
  needs `agent: 1` in the VM config and qemu-guest-agent running inside the guest.
- **Least privilege:** a custom role with exactly `VM.Audit VM.PowerMgmt VM.GuestAgent.Audit`,
  granted on `/vms` (or a pool) to a dedicated user and to its privilege-separated token.
  The built-in `PVEVMUser` role is too broad (console, backup, guest-agent file read/write).

## API basics

- Base URL `https://<host>:8006/api2/json/`. Token auth is the header
  `Authorization: PVEAPIToken=USER@REALM!TOKENID=UUID`; API tokens need no CSRF token for
  POST/PUT/DELETE. ([Proxmox VE API wiki](https://pve.proxmox.com/wiki/Proxmox_VE_API))
- Responses are wrapped in `{"data": ...}`.
- Every endpoint used below is marked `allowtoken: 1` in the schema. Console endpoints
  are the exception: they "require user privileges and cannot be accessed via an API
  token" ([pveum: API Tokens](https://pve.proxmox.com/pve-docs/chapter-pveum.html#pveum_tokens)).
  ddash does not need them.
- PVE ships a self-signed certificate by default, so the Go client must either pin it or
  trust a CA that the operator configured. go-proxmox has `WithRootCAs` and
  `WithInsecureSkipVerify` (`options.go`); the second is labelled "for lab use".

## Listing guests with status and resources

Source: the API viewer schema, [`apidoc.js`](https://pve.proxmox.com/pve-docs/api-viewer/apidoc.js)
(rendered at <https://pve.proxmox.com/pve-docs/api-viewer/>).

| Endpoint | Permission (from schema) | Notes |
|---|---|---|
| `GET /cluster/resources?type=vm` | any user; entries filtered | Cluster-wide, both kinds. Fields: `id` (`qemu/100`, `lxc/101`), `type`, `vmid`, `name`, `node`, `status`, `cpu`, `maxcpu`, `mem`, `maxmem`, `memhost`, `disk`, `maxdisk`, `diskread`, `diskwrite`, `netin`, `netout`, `uptime`, `tags`, `template`, `lock`, `pool`, `hastate`. |
| `GET /nodes/{node}/qemu` | "Only list VMs where you have VM.Audit" | Per node. Adds `pid`, `qmpstatus`, `running-qemu`, `running-machine`, pressure stall (`pressure*`) metrics. |
| `GET /nodes/{node}/lxc` | "Only list CTs where you have VM.Audit" | Per node. Adds `maxswap`, pressure metrics. |
| `GET /nodes/{node}/{qemu\|lxc}/{vmid}/status/current` | `VM.Audit` on `/vms/{vmid}` | One guest. For QEMU it includes `agent` (guest agent enabled in config) and `qmpstatus`. |

The filtering of `/cluster/resources` is in `pve-manager` `PVE/API2/Cluster.pm`:
`next if !$rpcenv->check($authuser, "/vms/$vmid", ['VM.Audit'], 1);` (about line 588).
Node entries are still returned without `Sys.Audit` on `/nodes/{node}`, but with fewer
details (`extract_node_stats(..., !$can_audit)`, about line 622). `pool` is removed
from a guest entry unless the caller has `Pool.Audit`.

Recommendation: poll `/cluster/resources?type=vm` as ddash's one source for the Workload
list. It covers every node in one request, and `node` in each entry gives the path for the
control and IP calls. `cpu` is a fraction (0..1) of `maxcpu`, and memory and disk are in bytes.

## Controlling guests

All of these need `VM.PowerMgmt` on `/vms/{vmid}` and return a UPID string (schema):

| Action | QEMU | LXC |
|---|---|---|
| start | `POST /nodes/{node}/qemu/{vmid}/status/start` | `POST /nodes/{node}/lxc/{vmid}/status/start` |
| shutdown (clean) | `.../qemu/{vmid}/status/shutdown`: sends ACPI power button; params `timeout`, `forceStop`, `keepActive` | `.../lxc/{vmid}/status/shutdown`: clean shutdown; params `timeout`, `forceStop` |
| stop (hard) | `.../qemu/{vmid}/status/stop`: "akin to pulling the power plug"; param `overrule-shutdown` | `.../lxc/{vmid}/status/stop`: "abruptly stop all processes"; param `overrule-shutdown` |
| reboot | `.../qemu/{vmid}/status/reboot`: shutdown, then start; applies pending changes | `.../lxc/{vmid}/status/reboot`: same |

Note that QEMU "reboot" is a full shutdown and start that goes through ACPI, so it needs a
cooperating guest OS. The hard alternative is `status/reset`, which is also `VM.PowerMgmt`.

**Task completion.** `GET /nodes/{node}/tasks/{upid}/status` returns `status`
(`running`/`stopped`) and, once stopped, `exitstatus`. The schema requires
"'Sys.Audit' permissions on '/nodes/<node>' if they are not the owner of the task".
`pve-manager` `PVE/API2/Tasks.pm` (`$check_task_user`, about lines 27-33) shows a token
owns the tasks it started: "token only sees token tasks, user sees user + token tasks".
ddash therefore needs no `Sys.Audit` to follow its own actions.

## Reading guest IP addresses (to link a Workload to an App)

### LXC: `GET /nodes/{node}/lxc/{vmid}/interfaces`

- Permission: `VM.Audit` on `/vms/{vmid}` (schema).
- Implementation (`pve-container` `src/PVE/LXC.pm`, `sub get_interfaces`): it enters the
  container's network namespace and runs `ip --json a`. It returns nothing if the
  container has no PID, that is, when it is stopped.
- Returns one entry per interface, `lo` included: `name`, `hwaddr`,
  `ip-addresses: [{ip-address-type, ip-address, prefix}]`, plus deprecated `inet`/`inet6`
  that hold only one address per family ("TODO: remove on next major release").
- Gotcha: `ip-address-type` here is the `ip` family, **`inet`/`inet6`**, not `ipv4`/`ipv6`
  as in the QEMU agent output.

### QEMU: `GET /nodes/{node}/qemu/{vmid}/agent/network-get-interfaces`

- Permission: any of `VM.GuestAgent.Audit` or `VM.GuestAgent.Unrestricted` on
  `/vms/{vmid}` (schema). Do **not** use the generic `POST /nodes/{node}/qemu/{vmid}/agent`
  (`command=...`): it requires `VM.GuestAgent.Unrestricted`.
- Returns `{"result": [{name, hardware-address, ip-addresses: [{ip-address-type: ipv4|ipv6,
  ip-address, prefix}], statistics}]}` (QEMU guest agent `GuestNetworkInterface`; mirrored
  by go-proxmox `AgentNetworkIface`).
- Preconditions (`qemu-server` `src/PVE/QemuServer/Agent.pm`, about lines 110-112), with
  the error each one gives:
  `No QEMU guest agent configured` (no `agent` option in the VM config),
  `VM <id> is not running`, `QEMU guest agent is not running`. ddash should treat these as
  "IPs unknown", not as failures. `status/current` exposes `agent` so ddash can skip VMs
  that have no agent configured.
- Version note: [PVE 9.0](https://pve.proxmox.com/wiki/Roadmap#Proxmox_VE_9.0) (released
  2025-08-05) "dropped [VM.Monitor] in favor of Sys.Audit for QEMU HMP monitor access and
  more fine-grained privileges for QEMU guest agent commands". On PVE 8.x the same call
  needed `VM.Monitor`. The role below targets PVE 9. A PVE 8 host would use
  `VM.Audit VM.PowerMgmt VM.Monitor`.

### Fallbacks when the guest is stopped or has no agent

- The guest config (`GET /nodes/{node}/{qemu|lxc}/{vmid}/config`, `VM.Audit`) has the
  MAC in `netN` and, for LXC `net[n]` `ip=` or QEMU cloud-init `ipconfig[n]`, any static IP.
- The MAC can be matched against the firewall/DHCP leases (other map tickets) to find a
  DHCP IP.
- Linking to an App: match an IP from the above against the host part of the App's URL.
  This is a ddash design choice, not something PVE provides.

## Least-privilege token

Privilege semantics ([pveum: Privileges](https://pve.proxmox.com/pve-docs/chapter-pveum.html#pveum_privileges)):
`VM.Audit` "view VM config"; `VM.PowerMgmt` "power management (start, stop, reset,
shutdown, ...)"; `VM.GuestAgent.Audit` "issue informational QEMU guest agent commands".

Built-in roles come from `pve-access-control` `src/PVE/AccessControl.pm` (`$privgroups`,
`create_roles`):

- `PVEAuditor` = every `audit` privilege, which includes `VM.Audit` and
  `VM.GuestAgent.Audit`. It lacks `VM.PowerMgmt`, so it allows reading but no control.
- `PVEVMUser` = VM `user` + `audit`, which adds `VM.Console`, `VM.Backup`,
  `VM.Config.CDROM`, `VM.Config.Cloudinit`, `VM.GuestAgent.FileRead`,
  `VM.GuestAgent.FileWrite` and `VM.GuestAgent.FileSystemMgmt`. ddash needs none of these,
  and file write inside guests is a large blast radius, so don't use it.

Token model ([pveum: API Tokens](https://pve.proxmox.com/pve-docs/chapter-pveum.html#pveum_tokens)):
with privilege separation (the default), the token's "effective permissions are calculated
by intersecting user and token permissions". So the ACL must be on **both** the user and the
token. Tokens can carry an expiry, and the secret "is only displayed/returned once".

Setup on the PVE host:

```sh
pveum role add DdashOperator --privs "VM.Audit VM.PowerMgmt VM.GuestAgent.Audit"
pveum user add ddash@pve --comment "ddash dashboard"          # pve realm, no password needed
pveum acl modify /vms --users ddash@pve --roles DdashOperator
pveum user token add ddash@pve dashboard --privsep 1          # prints the secret once
pveum acl modify /vms --tokens 'ddash@pve!dashboard' --roles DdashOperator
pveum user token permissions ddash@pve dashboard              # verify
```

The ACL can be narrowed by granting on `/pool/<pool>` or on single `/vms/<vmid>` paths
instead of `/vms`. ACLs propagate by default. Guests outside the grant then simply drop out
of `/cluster/resources`. `Sys.Audit` on `/nodes` is optional: it only enriches node stats
and lets ddash see tasks it did not start. The pveum guide's own "Limited API Token for
Monitoring" example uses `-privsep 1` with `PVEAuditor` on `/vms` in the same way.

## Go client: luthermonson/go-proxmox

[github.com/luthermonson/go-proxmox](https://github.com/luthermonson/go-proxmox), latest tag
v0.8.2 (2026-09-30), `go 1.25`. It covers everything above:

| Need | go-proxmox |
|---|---|
| token auth | `proxmox.NewClient("https://pve:8006/api2/json", proxmox.WithAPIToken("ddash@pve!dashboard", secret))`, which sends `Authorization: PVEAPIToken=...` (`proxmox.go`) |
| list all | `cluster.Resources(ctx, "vm")` → `ClusterResources` |
| per node | `node.VirtualMachines(ctx)`, `node.Containers(ctx)`, `node.VirtualMachine(ctx, id)`, `node.Container(ctx, id)` |
| power | `vm.Start/Stop/Shutdown/Reboot/Reset(ctx)` and `ct.Start/Stop/Reboot(ctx)`, `ct.Shutdown(ctx, force, timeout)`; all return `*Task` |
| wait | `task.Wait(ctx, interval, max)` / `task.WaitFor(ctx, seconds)` |
| QEMU IPs | `vm.AgentGetNetworkIFaces(ctx)`: drops `lo` itself |
| LXC IPs | `ct.Interfaces(ctx)` |

Caveats from the source:

- `ContainerInterface` (`types.go`) maps only `name`, `hwaddr`, `inet` and `inet6`, the
  deprecated single-address fields. It does **not** decode `ip-addresses`, so ddash will see
  at most one IPv4 and one IPv6 address per LXC interface. The fix is a raw
  `client.Get(ctx, "/nodes/%s/lxc/%d/interfaces", &custom)` or an upstream PR.
- `client.Node(ctx, name)` first calls `/nodes/{node}/status` (needs `Sys.Audit`) and
  ignores a not-authorized error. With this token, expect a 403 per lookup, which is
  harmless. Building `&proxmox.Node{}` through `Node.New(client, name)` avoids it.

## Sources

- API schema: <https://pve.proxmox.com/pve-docs/api-viewer/apidoc.js> (viewer: <https://pve.proxmox.com/pve-docs/api-viewer/>)
- User management/privileges/tokens: <https://pve.proxmox.com/pve-docs/chapter-pveum.html>
- API overview: <https://pve.proxmox.com/wiki/Proxmox_VE_API>
- Release notes (VM.Monitor removal in 9.0): <https://pve.proxmox.com/wiki/Roadmap>
- `pve-manager` `PVE/API2/Cluster.pm`, `PVE/API2/Tasks.pm`: <https://git.proxmox.com/?p=pve-manager.git>
- `pve-access-control` `src/PVE/AccessControl.pm`: <https://git.proxmox.com/?p=pve-access-control.git>
- `pve-container` `src/PVE/LXC.pm`: <https://git.proxmox.com/?p=pve-container.git>
- `qemu-server` `src/PVE/QemuServer/Agent.pm`: <https://git.proxmox.com/?p=qemu-server.git>
- go-proxmox v0.8.2: <https://github.com/luthermonson/go-proxmox>
