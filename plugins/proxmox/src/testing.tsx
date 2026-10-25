import { showPlugin, type Answers, type Call } from "@ddash/plugin/testing";
import plugin from "./index";

/** The proxmox Plugin's scope in tests. */
export const proxmox = { name: "proxmox", requires: ["proxmox", "proxmox-api"] };

const GiB = 1024 ** 3;

// What proxmox-api answers ListWorkloads with (Connect JSON: 64-bit
// integers are strings, zero values left out).
export const listed = {
  workloads: [
    {
      kind: "KIND_LXC",
      vmid: 101,
      node: "pve",
      name: "nginx",
      status: "running",
      tags: ["infra", "web"],
      cpu: 0.0125,
      maxCpu: 2,
      mem: String(GiB / 4),
      maxMem: String(GiB),
      disk: String(2 * GiB),
      maxDisk: String(8 * GiB),
      netIn: "1048576",
      netOut: "2097152",
      uptimeSeconds: "90000",
      nics: [{ name: "eth0", mac: "02:5e:1a:00:01:01", ips: ["198.18.1.10", "198.18.1.11"] }],
      addresses: "ADDRESS_SOURCE_LIVE",
    },
    {
      kind: "KIND_LXC",
      vmid: 102,
      node: "pve",
      name: "media-stack",
      status: "running",
      cpu: 0.34,
      maxCpu: 4,
      mem: String(3 * GiB),
      maxMem: String(8 * GiB),
      disk: String(20 * GiB),
      maxDisk: String(100 * GiB),
      uptimeSeconds: "172800",
      nics: [{ name: "eth0", mac: "02:5e:1a:00:01:02", ips: ["198.18.2.10"] }],
      addresses: "ADDRESS_SOURCE_LIVE",
    },
    {
      kind: "KIND_LXC",
      vmid: 104,
      node: "pve",
      name: "dhcp-box",
      status: "stopped",
      maxCpu: 1,
      maxMem: String(GiB / 2),
      maxDisk: String(4 * GiB),
      nics: [{ name: "eth0", mac: "02:5e:1a:00:01:04" }],
      addresses: "ADDRESS_SOURCE_CONFIG",
    },
    {
      kind: "KIND_VM",
      vmid: 111,
      node: "pve",
      name: "win-test",
      status: "stopped",
      tags: ["lab"],
      maxCpu: 2,
      maxMem: String(8 * GiB),
      maxDisk: String(64 * GiB),
      nics: [{ name: "net0", mac: "02:5e:1a:00:01:11", ips: ["198.18.1.111"] }],
      addresses: "ADDRESS_SOURCE_CONFIG",
    },
    { kind: "KIND_VM", vmid: 112, node: "pve", name: "locked-out", status: "stopped", addressesError: "Proxmox refused the API token" },
    { kind: "KIND_VM", vmid: 9000, node: "pve", name: "debian-template", status: "stopped", template: true },
  ],
};

const control = ["VM.Audit", "VM.GuestAgent.Audit", "VM.PowerMgmt"];
const audit = ["VM.Audit", "VM.GuestAgent.Audit"];

/** ListPermissions: nginx (101) is Protected; the rest are controllable. */
export const permissions = {
  workloads: [
    { vmid: 101, privileges: audit },
    { vmid: 102, privileges: control },
    { vmid: 104, privileges: control },
    { vmid: 111, privileges: control },
    { vmid: 112, privileges: control },
    { vmid: 9000, privileges: control },
  ],
};

/**
 * Shows the proxmox Plugin at path (e.g. "/proxmox") with its Components
 * answering by method name (Actions {} unless given), and returns the calls.
 */
export const show = (path: string, answers: Answers = {}) =>
  showPlugin(plugin, proxmox, path, {
    answers: {
      ListWorkloads: () => Response.json(listed),
      ListPermissions: () => Response.json(permissions),
      ListPowerTasks: () => Response.json({}),
      ...answers,
    },
  });

/** The bodies of the calls to method. */
export const bodies = (calls: Call[], method: string) => calls.filter((c) => c.method === method).map((c) => c.body);
