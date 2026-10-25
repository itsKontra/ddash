import { act, render, waitFor } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope } from "@ddash/plugin";
import { MemoryRouter, Route, Routes, useRoutes } from "react-router";
import { expect, vi } from "vitest";
import plugin from "./index";

/** A call the Page made: the method's name and its decoded JSON body. */
export interface Call {
  method: string;
  url: string;
  body: unknown;
}

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

function PluginPages() {
  return useRoutes(plugin.pages!);
}

/** Lets queries and Actions settle. */
export const settle = () => act(() => new Promise((r) => setTimeout(r, 50)));

/** An answer that never comes, to see what shows while a call is in flight. */
export const pending = () => new Promise<Response>(() => {}) as unknown as Response;

/**
 * Shows the proxmox Plugin at path (e.g. "/proxmox") with its Components
 * answering by method name (Actions {} unless given), and returns the calls.
 * It waits for every answer unless one is pending.
 */
export async function show(path: string, answers: Record<string, () => Response> = {}) {
  const calls: Call[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://ddash.test").pathname;
    const method = url.split("/").pop()!;
    const raw = init?.body;
    calls.push({ method, url, body: raw ? JSON.parse(typeof raw === "string" ? raw : new TextDecoder().decode(raw as Uint8Array)) : undefined });
    const all: Record<string, () => Response> = {
      ListWorkloads: () => Response.json(listed),
      ListPermissions: () => Response.json(permissions),
      ListPowerTasks: () => Response.json({}),
      ...answers,
    };
    return all[method]?.() ?? Response.json({});
  });
  const client = createQueryClient();
  render(
    <PluginHost client={client}>
      <PluginScope plugin={{ name: "proxmox", requires: ["proxmox", "proxmox-api"] }} fetch={fetch}>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="/proxmox/*" element={<PluginPages />} />
          </Routes>
        </MemoryRouter>
      </PluginScope>
    </PluginHost>,
  );
  // Wait for the first answers however long a loaded machine takes, then
  // for what they render.
  if (!Object.values(answers).includes(pending)) await waitFor(() => expect(client.isFetching()).toBe(0));
  await settle();
  return calls;
}

/** The bodies of the calls to method. */
export const bodies = (calls: Call[], method: string) => calls.filter((c) => c.method === method).map((c) => c.body);
