import { act, cleanup, fireEvent, screen, within } from "@testing-library/react";
import type { Concern } from "@ddash/plugin";
import { pending, settle, streamed, unavailable, type Answers } from "@ddash/plugin/testing";
import { useState } from "react";
import { useLocation } from "react-router";
import { afterEach, expect, it } from "vitest";
import plugin from "./index";
import { appCheck, renderScoped } from "./testing";

afterEach(cleanup);

const ago = (mins: number) => new Date(Date.now() - mins * 60_000).toISOString();

const app = (id: string, name: string, workload?: object) => ({ id, name, url: `https://${id}.home.example`, reach: "REACH_PROXIED", workload });
const lxc = (vmid: string, name: string, state: string) => ({ ref: { kind: "KIND_LXC", host: "pve", id: vmid }, name, state });

/** N8n on a stopped LXC, Paperless on an exited container, Grafana on a running LXC, Vault on a stopped Protected one, NAS without a Workload. */
const apps = {
  apps: [
    app("n8n", "N8n", lxc("206", "n8n", "stopped")),
    app("paperless", "Paperless", { ref: { kind: "KIND_DOCKER", host: "apps", id: "paperless-webserver-1" }, name: "paperless-webserver-1", state: "exited" }),
    app("grafana", "Grafana", lxc("204", "grafana", "running")),
    app("vault", "Vault", lxc("207", "vault", "stopped")),
    app("nas", "NAS"),
    app("wiki", "Wiki", lxc("208", "wiki", "running")),
  ],
};

const down = (id: number, appId: string, extra: Record<string, unknown> = {}) => appCheck(id, appId, "STATE_DOWN", { reason: "HTTP 502", since: ago(6), ...extra });

const checks = {
  checks: [
    down(1, "n8n"),
    down(2, "paperless"),
    down(3, "grafana", { reason: "timeout" }),
    down(4, "vault"),
    down(5, "nas", { reason: "", since: ago(20), muted: true }),
    appCheck(6, "wiki", "STATE_UP"),
    { id: "7", spec: { name: "router", type: "CHECK_TYPE_TCP", target: "198.18.0.1:22" }, state: "STATE_DOWN" },
  ],
};

const container = (name: string, state: string, status: string) => ({ container: { name, state, status } });

/** compose's Projects: paperless's webserver exited out of memory. */
const projects = {
  hosts: [
    {
      name: "apps",
      projects: [
        {
          host: "apps",
          name: "paperless",
          state: "PROJECT_STATE_PARTIAL",
          services: [
            { name: "db", containers: [container("paperless-db-1", "STATE_RUNNING", "Up 3 hours")] },
            { name: "webserver", containers: [container("paperless-webserver-1", "STATE_EXITED", "Exited (137) 6 minutes ago")] },
          ],
        },
      ],
    },
  ],
};

/** proxmox-api's guests and ddash's privileges: 207 is Protected. */
const guests = {
  workloads: [
    { kind: "KIND_LXC", vmid: 206, node: "pve", name: "n8n", status: "stopped" },
    { kind: "KIND_LXC", vmid: 204, node: "pve", name: "grafana", status: "running" },
    { kind: "KIND_LXC", vmid: 207, node: "pve", name: "vault", status: "stopped" },
  ],
};
const permissions = {
  workloads: [
    { vmid: 204, privileges: ["VM.Audit", "VM.PowerMgmt"] },
    { vmid: 206, privileges: ["VM.Audit", "VM.PowerMgmt"] },
    { vmid: 207, privileges: ["VM.Audit"] },
  ],
};

// What useConcerns reported last, its Actions as labels, "(not a fix)" after a secondary one.
let reported: Concern[] | undefined;
const plain = () => reported?.map(({ actions, ...c }) => ({ ...c, actions: actions?.map((a) => (a.secondary ? `${a.label} (not a fix)` : a.label)) }));
let path = "";

// Runs the apps Plugin's useConcerns as the Overview does, drawing the
// Sheet of the Action asked for with open(id, label).
let open: (id: string, label: string) => void = () => {};
function Probe() {
  const concerns = plugin.useConcerns!();
  const [shown, setShown] = useState<{ id: string; label: string } | null>(null);
  const location = useLocation();
  path = location.pathname + location.search;
  reported = concerns;
  open = (id, label) => setShown({ id, label });
  const action = shown && concerns?.find((c) => c.id === shown.id)?.actions?.find((a) => a.label === shown.label);
  return action && "sheet" in action ? action.sheet(() => setShown(null)) : null;
}

async function show(answers: Answers = {}, installed = ["apps", "checks", "proxmox", "docker"]) {
  const calls = renderScoped(
    <Probe />,
    {
      ListApps: () => Response.json(apps),
      ListChecks: () => Response.json(checks),
      ListProjects: () => Response.json(projects),
      // proxmox-api's, not apps' own: useConcerns reads no other.
      ListWorkloads: () => Response.json(guests),
      ListPermissions: () => Response.json(permissions),
      ...answers,
    },
    installed,
  );
  await settle();
  return calls;
}

it("reports each down App, its stopped Workload as the cause with its fix, else the Check's reason", async () => {
  await show();

  expect(plain()).toEqual([
    {
      id: "n8n",
      tone: "down",
      subject: "N8n",
      title: "N8n is down",
      cause: "LXC 206 n8n is stopped · 6 min ago",
      quiet: undefined,
      icon: undefined,
      href: "/apps?app=n8n",
      actions: ["Start LXC", "Mute (not a fix)"],
    },
    {
      id: "paperless",
      tone: "down",
      subject: "Paperless",
      title: "Paperless is down",
      cause: "Compose Project paperless exited (137) · 6 min ago",
      quiet: undefined,
      icon: undefined,
      href: "/apps?app=paperless",
      actions: ["Restart", "Mute (not a fix)"],
    },
    // Its LXC runs: the Check's own reason.
    { id: "grafana", tone: "down", subject: "Grafana", title: "Grafana is down", cause: "timeout · 6 min ago", quiet: undefined, icon: undefined, href: "/apps?app=grafana", actions: ["Mute (not a fix)"] },
    // Protected in Proxmox: no Start.
    { id: "vault", tone: "down", subject: "Vault", title: "Vault is down", cause: "LXC 207 vault is stopped · 6 min ago", quiet: undefined, icon: undefined, href: "/apps?app=vault", actions: ["Mute (not a fix)"] },
    // No Workload, muted: quiet, with neither a fix nor Mute.
    { id: "nas", tone: "down", subject: "NAS", title: "NAS is down", cause: "down · 20 min ago", quiet: "muted", icon: undefined, href: "/apps?app=nas", actions: undefined },
  ]);
});

it("keeps the fix of a muted or in-Maintenance App, quiet", async () => {
  const quiet = { checks: [down(1, "n8n", { muted: true }), down(2, "paperless", { maintenance: true })] };
  await show({ ListChecks: () => Response.json(quiet) });

  expect(plain()?.map((c) => [c.id, c.quiet, c.icon, c.actions])).toEqual([
    ["n8n", "muted", undefined, ["Start LXC"]],
    ["paperless", "in Maintenance", "wrench", ["Restart"]],
  ]);
});

it("starts a stopped guest on its Proxmox detail, which asks first; without the proxmox Plugin it offers no Start", async () => {
  await show();
  const start = reported![0].actions![0];
  act(() => void ("run" in start && start.run()));
  expect(path).toBe("/proxmox/workloads/206?start");

  cleanup();
  await show({}, ["apps", "checks"]);
  expect(plain()?.[0].actions).toEqual(["Mute (not a fix)"]);
});

it("restarts an exited Compose Project through its Plan, and drops the App once its Check is up, without a reload", async () => {
  let listed = checks;
  const steps = [{ system: "SYSTEM_DOCKER_AGENT", title: "Restart paperless", values: [{ label: "Runs", after: "docker compose restart" }] }];
  const calls = await show({
    ListChecks: () => Response.json(listed),
    PlanAction: () => Response.json({ plan: { steps } }),
    RunAction: () => {
      // compose has the containers' Checks probed once the run ended: paperless answers.
      listed = { checks: checks.checks.filter((c) => c.id !== "2") };
      return streamed([{ run: { id: "r1", steps, states: ["STEP_STATE_DONE"], details: [""], ended: new Date().toISOString(), outcome: "done" } }]);
    },
  });

  act(() => open("paperless", "Restart"));
  await settle();
  const dialog = screen.getByRole("dialog", { name: "Restart paperless" });
  expect(calls.find((c) => c.method === "PlanAction")?.body).toEqual({ action: { restart: { host: "apps", project: "paperless" } } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Restart" }));
  await settle();

  expect(calls.find((c) => c.method === "RunAction")?.body).toMatchObject({ action: { restart: { host: "apps", project: "paperless" } }, plan: { steps } });
  expect(reported?.map((c) => c.id)).toEqual(["n8n", "grafana", "vault", "nas"]);
});

it("mutes a down App's Check as the Checks Page does", async () => {
  const calls = await show();

  const mute = reported![2].actions![0];
  await act(async () => void ("run" in mute && (await mute.run())));
  await settle();

  expect(calls.filter((c) => c.method === "SetMuted").map((c) => c.body)).toEqual([{ id: "3", muted: true }]);
});

it("reports nothing until it knows, and nothing while checks can't be read: checks says so", async () => {
  await show({ ListProjects: () => pending() });
  expect(reported).toBeUndefined();

  cleanup();
  await show({ ListChecks: () => unavailable("checks is restarting") });
  expect(reported).toEqual([]);
});

it("reads neither compose nor proxmox-api while no down App runs on them", async () => {
  const calls = await show({ ListChecks: () => Response.json({ checks: [down(3, "nas")] }) });

  expect(reported?.map((c) => c.cause)).toEqual(["HTTP 502 · 6 min ago"]);
  expect(calls.filter((c) => ["ListProjects", "ListWorkloads", "ListPermissions"].includes(c.method))).toEqual([]);
});

it("names a down App by its Check while apps can't be read", async () => {
  await show({ ListApps: () => unavailable("apps is restarting"), ListChecks: () => Response.json({ checks: [down(3, "grafana")] }) });

  expect(plain()?.map((c) => [c.title, c.cause])).toEqual([["grafana is down", "HTTP 502 · 6 min ago"]]);
});
