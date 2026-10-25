import { cleanup, screen, within } from "@testing-library/react";
import { metricValue, pending, showWidget, unavailable, type Answer } from "@ddash/plugin/testing";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { proxmox } from "./testing";

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
async function show(answer: Answer, size?: "S" | "M") {
  const { reported } = await showWidget(proxmox, guests(), { size, answers: { ListWorkloads: answer } });
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
  expect(metricValue("running", s)).toBe("3");
  expect(metricValue("guests", s)).toBe("5");
  expect(within(s).queryByText("LXCs")).toBeNull();
  cleanup();

  await show(() => Response.json(listed), "M");
  const m = screen.getByRole("region", { name: "widget" });
  expect(within(m).getByText("LXCs").nextElementSibling?.textContent).toBe("2/3 running");
  expect(within(m).getByText("VMs").nextElementSibling?.textContent).toBe("1/2 running");
});

it("draws its counts as skeletons until proxmox-api first answers", async () => {
  await show(pending);
  const s = screen.getByRole("region", { name: "widget" });

  expect(metricValue("running", s)).toBeUndefined();
  expect(metricValue("guests", s)).toBeUndefined();
});

it("reports warn when proxmox-api is unavailable", async () => {
  const reported = await show(() => unavailable("Proxmox refused the API token"));

  expect(reported.status).toEqual({ tone: "warn", summary: "proxmox-api unavailable: Proxmox refused the API token" });
});

it("reports nothing until proxmox-api first answers", async () => {
  const reported = await show(pending);

  expect(reported.status).toBeUndefined();
});
