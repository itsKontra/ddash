import { act, cleanup, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope } from "@ddash/plugin";
import { afterEach, expect, it, vi } from "vitest";
import plugin from "./index";

afterEach(cleanup);

const GiB = 1024 ** 3;

// What proxmox-api answers ListWorkloads with (Connect JSON: 64-bit
// integers are strings, zero values left out).
const listed = {
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
  ],
};

async function showPage(answer: () => Response) {
  const requests: Request[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    requests.push(new Request(new URL(String(input), "http://ddash.test"), init));
    return answer();
  });
  const Page = plugin.pages![0].Component!;
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "proxmox", requires: ["proxmox-api"] }} fetch={fetch}>
        <Page />
      </PluginScope>
    </PluginHost>,
  );
  await act(() => new Promise((r) => setTimeout(r, 50)));
  return requests;
}

it("is the proxmox Nav Entry at order 40 (its icon, server, is in plugin.yaml)", () => {
  expect(plugin.nav?.order).toBe(40);
});

it("lists every LXC and VM with its status, resources, tags and uptime", async () => {
  const requests = await showPage(() => Response.json(listed));

  expect(new URL(requests[0].url).pathname).toBe("/api/c/proxmox-api/ddash.proxmoxapi.ProxmoxApiService/ListWorkloads");
  const nginx = screen.getByRole("row", { name: /nginx/ });
  expect(within(nginx).getByRole("img", { name: "running" })).toBeTruthy();
  expect(within(nginx).getByText("101")).toBeTruthy();
  expect(within(nginx).getByText("LXC")).toBeTruthy();
  expect(within(nginx).getByText("infra")).toBeTruthy();
  expect(within(nginx).getByText("web")).toBeTruthy();
  expect(within(nginx).getByText("1.3 % of 2 CPUs")).toBeTruthy();
  expect(within(nginx).getByText("256 MiB / 1.0 GiB")).toBeTruthy();
  expect(within(nginx).getByText("2.0 GiB / 8.0 GiB")).toBeTruthy();
  expect(within(nginx).getByText("1.0 MiB in · 2.0 MiB out")).toBeTruthy();
  expect(within(nginx).getByText("1 d 1 h")).toBeTruthy();
  const win = screen.getByRole("row", { name: /win-test/ });
  expect(within(win).getByRole("img", { name: "stopped" })).toBeTruthy();
  expect(within(win).getByText("VM")).toBeTruthy();
});

it("shows IPs read from the running guest, and marks those from a stopped one's config", async () => {
  await showPage(() => Response.json(listed));

  const nginx = screen.getByRole("row", { name: /nginx/ });
  expect(within(nginx).getByText("198.18.1.10")).toBeTruthy();
  expect(within(nginx).getByText("198.18.1.11")).toBeTruthy();
  expect(within(nginx).queryByText("from config")).toBeNull();
  const win = screen.getByRole("row", { name: /win-test/ });
  expect(within(win).getByText("198.18.1.111")).toBeTruthy();
  expect(within(win).getByText("from config")).toBeTruthy();
  // On DHCP and stopped: only its MAC is known.
  const dhcp = screen.getByRole("row", { name: /dhcp-box/ });
  expect(within(dhcp).getByText("02:5e:1a:00:01:04")).toBeTruthy();
  const locked = screen.getByRole("row", { name: /locked-out/ });
  expect(within(locked).getByText("Proxmox refused the API token")).toBeTruthy();
});

it("shows why proxmox-api can't list the Workloads", async () => {
  await showPage(() => Response.json({ code: "unavailable", message: "Proxmox refused the API token" }, { status: 503 }));

  expect(screen.getByText("Proxmox refused the API token")).toBeTruthy();
});
