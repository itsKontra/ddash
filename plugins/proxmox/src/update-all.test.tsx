import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { settle } from "@ddash/plugin/testing";
import { bodies, daysAgo, show } from "./testing";

afterEach(cleanup);

const ref = (vmid: number, kind = "KIND_LXC") => ({ kind, host: "pve", id: String(vmid) });

const planned = {
  workloads: [
    { workload: ref(102), label: "LXC 102 media-stack", packages: [{ name: "libc6", current: "1", new: "2" }], snapshot: true, snapshotSupported: true },
    { workload: ref(105), label: "LXC 105 grafana", packages: [{ name: "a" }, { name: "b" }, { name: "c" }], snapshotSupported: false },
  ],
  skipped: [
    { workload: ref(101), label: "LXC 101 nginx", reason: "SKIP_REASON_PROTECTED" },
    { workload: ref(104), label: "LXC 104 dhcp-box", reason: "SKIP_REASON_STOPPED" },
    { workload: ref(111, "KIND_VM"), label: "VM 111 win-test", reason: "SKIP_REASON_NO_HOST_AGENT" },
    { workload: ref(106), label: "LXC 106 influx", reason: "SKIP_REASON_AGENT_NOT_READY", detail: "unreachable: connection refused" },
  ],
};

it("plans Update all running: who it updates in order, and who it skips and why", async () => {
  const calls = await show("/proxmox", { PlanUpdateAll: () => Response.json(planned) });

  fireEvent.click(screen.getByRole("button", { name: "Update all running" }));
  await settle();
  const dialog = screen.getByRole("dialog");
  expect(bodies(calls, "PlanUpdateAll")).toEqual([{}]);

  const [order, skipped] = within(dialog).getAllByRole("list");
  expect(within(order).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
    "LXC 102 media-stack1 update · ddash Snapshot first",
    "LXC 105 grafana3 updates · no snapshot",
  ]);
  const why = within(skipped)
    .getAllByRole("listitem")
    .map((li) => li.textContent);
  expect(why).toEqual([
    "LXC 101 nginxProtected: ddash doesn't hold VM.PowerMgmt on it",
    "LXC 104 dhcp-boxnot running",
    "VM 111 win-testno Host Agent",
    "LXC 106 influxits Host Agent isn't ready: unreachable: connection refused",
  ]);

  // The Plan Dialog is the only confirmation.
  fireEvent.click(within(dialog).getByRole("button", { name: "Update all" }));
  await settle();
  expect(screen.queryByRole("alertdialog")).toBeNull();
  expect(bodies(calls, "RunUpdateAll")).toEqual([{ workloads: [ref(102), ref(105)] }]);
});

const failed = (id: string) => ({
  id,
  state: "STATE_FAILED",
  started: daysAgo(0),
  error: "LXC 105 grafana: apt-get failed",
  items: [
    { workload: ref(102), label: "LXC 102 media-stack", state: "STATE_DONE" },
    { workload: ref(105), label: "LXC 105 grafana", state: "STATE_FAILED" },
    { workload: ref(107), label: "LXC 107 telegraf", state: "STATE_SKIPPED" },
  ],
});

it("shows the Update all running started here, one Workload after another, and where it stopped, until the Page is left", async () => {
  let bulk = failed("b1");
  const answers = {
    PlanUpdateAll: () => Response.json(planned),
    ListUpdates: () => Response.json({ bulk }),
    RunUpdateAll: () => {
      bulk = failed("b2");
      return Response.json({ bulk: { id: "b2", state: "STATE_RUNNING" } });
    },
  };
  await show("/proxmox", answers);
  // The last run ended before the Page opened: its outcome is in the Audit Log.
  expect(screen.queryByRole("region", { name: "Update all running" })).toBeNull();

  fireEvent.click(screen.getByRole("button", { name: "Update all running" }));
  await settle();
  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Update all" }));
  await settle();
  fireEvent.click(within(screen.getByRole("dialog")).getAllByRole("button", { name: "Close" }).at(-1)!);
  await settle();

  const progress = screen.getByRole("region", { name: "Update all running" });
  expect(within(progress).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["LXC 102 media-stackdone", "LXC 105 grafanafailed", "LXC 107 telegrafnot run"]);
  expect(progress.textContent).toContain("Stopped: LXC 105 grafana: apt-get failed");

  // A reload drops it.
  cleanup();
  await show("/proxmox", answers);
  expect(screen.getByRole("heading", { name: "Proxmox" })).toBeTruthy();
  expect(screen.queryByRole("region", { name: "Update all running" })).toBeNull();
});

it("shows an Update all running in flight to whoever opens the Page", async () => {
  await show("/proxmox", { ListUpdates: () => Response.json({ bulk: { ...failed("b1"), state: "STATE_RUNNING", error: "" } }) });
  expect(screen.getByRole("region", { name: "Update all running" })).toBeTruthy();
});
