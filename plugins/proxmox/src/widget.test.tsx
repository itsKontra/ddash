import { act, cleanup, render, screen, within } from "@testing-library/react";
import { createQueryClient, PluginHost, PluginScope, type Status, type Widget } from "@ddash/plugin";
import { afterEach, expect, it, vi } from "vitest";
import plugin from "./index";

afterEach(cleanup);

const workload = (vmid: number, kind: "KIND_LXC" | "KIND_VM", status: string, extra: object = {}) => ({
  kind,
  vmid,
  node: "pve",
  name: `guest-${vmid}`,
  status,
  ...extra,
});

const listed = {
  workloads: [
    workload(101, "KIND_LXC", "running"),
    workload(102, "KIND_LXC", "running"),
    workload(103, "KIND_LXC", "stopped"),
    workload(110, "KIND_VM", "running"),
    workload(111, "KIND_VM", "stopped"),
    // A template never runs, so it isn't counted.
    workload(9000, "KIND_VM", "stopped", { template: true }),
  ],
};

const guests = () => plugin.widgets!.find((w) => w.id === "guests")!;

// Renders the Widget's body at size, against proxmox-api answering with
// answer, and returns what its useStatus reported last.
async function show(answer: () => Response, size: "S" | "M" = "S") {
  const w = guests();
  const fetch = vi.fn(async () => answer());
  const reported: { status?: Status } = {};
  function Probe({ widget }: { widget: Widget }) {
    reported.status = widget.useStatus();
    return null;
  }
  render(
    <PluginHost client={createQueryClient()}>
      <PluginScope plugin={{ name: "proxmox", requires: ["proxmox-api"] }} fetch={fetch}>
        <Probe widget={w} />
        <section aria-label="widget">
          <w.Body size={size} />
        </section>
      </PluginScope>
    </PluginHost>,
  );
  await act(() => new Promise((r) => setTimeout(r, 50)));
  return reported;
}

it("is the guests Widget, S or M, S by default", () => {
  const w = guests();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Guests", ["S", "M"], "S"]);
});

it("reports up with the counts in its summary", async () => {
  const reported = await show(() => Response.json(listed));

  expect(reported.status).toEqual({ tone: "up", summary: "3 of 5 running · 3 LXCs, 2 VMs" });
});

it("shows the counts at S, and each kind's at M", async () => {
  await show(() => Response.json(listed));
  const s = screen.getByRole("region", { name: "widget" });
  expect(within(s).getByText("running").previousSibling?.textContent).toBe("3");
  expect(within(s).getByText("guests").previousSibling?.textContent).toBe("5");
  cleanup();

  await show(() => Response.json(listed), "M");
  const m = screen.getByRole("region", { name: "widget" });
  expect(within(m).getByRole("row", { name: /LXCs/ }).textContent).toMatch(/LXCs.*2\/3 running/);
  expect(within(m).getByRole("row", { name: /VMs/ }).textContent).toMatch(/VMs.*1\/2 running/);
});

it("reports warn when proxmox-api is unavailable", async () => {
  const reported = await show(() => Response.json({ code: "unavailable", message: "Proxmox refused the API token" }, { status: 503 }));

  expect(reported.status).toEqual({ tone: "warn", summary: "proxmox-api unavailable: Proxmox refused the API token" });
});

it("reports nothing until proxmox-api first answers", async () => {
  const reported = await show(() => new Promise<Response>(() => {}) as unknown as Response);

  expect(reported.status).toBeUndefined();
});
