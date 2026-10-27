import { PlanSchema, RunActionResponseSchema, System } from "@ddash/proto/apps/apps_pb";
import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { useLocation } from "react-router";
import { afterEach, describe, expect, it } from "vitest";
import { pending, settle, streamed, type Answers, type Call } from "@ddash/plugin/testing";
import plugin from "./index";
import { actions, failing, ranThrough, renderScoped, split, step, watch } from "./testing";

afterEach(cleanup);

let search = "";
function Where() {
  search = useLocation().search;
  return null;
}

const Page = () => {
  const P = plugin.pages![0].Component!;
  return (
    <>
      <P />
      <Where />
    </>
  );
};

/** A Plan of two steps (Connect JSON). */
const plan = {
  steps: [
    { system: "SYSTEM_NGINX_PROXY_MANAGER", title: "Update Proxy Host grafana.home.example", values: [{ label: "Upstream", before: "http://198.18.1.204:3000", after: "http://198.18.1.204:3001" }] },
    { system: "SYSTEM_DDASH", title: "Update the App Grafana" },
  ],
};

const open = async (id: string, answers: Answers = {}) => {
  const calls = renderScoped(<Page />, { ListApps: () => Response.json(split), PlanAction: () => Response.json({ plan }), ...answers }, ["apps"], `/?app=${encodeURIComponent(id)}`);
  await settle();
  return calls;
};

const pane = () => screen.getByRole("article");
const panel = (name: string) => within(pane()).getByRole("region", { name });
const dialog = () => within(screen.getByRole("dialog"));
/** The Dialog's steps as "title · system · state". */
const steps = () =>
  dialog()
    .getAllByRole("listitem")
    .filter((li) => li.parentElement?.tagName === "OL")
    .map((li) => li.textContent);
const state = (step: string | null) => step?.match(/running…|done|failed|not run/)?.[0];
const plans = (calls: { method: string; body: unknown }[]) => calls.filter((c) => c.method === "PlanAction").map((c) => c.body);
const runs = (calls: Call[]) => actions(calls).filter((c) => c.method === "RunAction");

/** Each way into a Plan Dialog: what opens it on which App, and the App Action it plans. */
const ways: { name: string; app: string; confirm: string; open: () => unknown; action: object }[] = [
  {
    name: "Delete…",
    app: "proxy:grafana.home.example",
    confirm: "Delete",
    open: () => fireEvent.click(within(pane()).getByRole("button", { name: "Delete…" })),
    action: { delete: { id: "proxy:grafana.home.example" } },
  },
  {
    name: "the Address Panel's Edit",
    app: "proxy:jellyfin.home.example",
    confirm: "Save",
    open: () => {
      fireEvent.click(screen.getByRole("button", { name: "Edit Address and DNS" }));
      const form = within(panel("Address and DNS"));
      fireEvent.change(form.getByLabelText("URL"), { target: { value: "https://films.home.example" } });
      fireEvent.change(form.getByLabelText("Aliases"), { target: { value: "media.home.example, tv.home.example jellyfin.home.example" } });
      fireEvent.click(form.getByRole("button", { name: "Save" }));
    },
    action: { changeAddress: { id: "proxy:jellyfin.home.example", url: "https://films.home.example", aliases: ["media.home.example", "tv.home.example", "jellyfin.home.example"] } },
  },
  {
    name: "the Reverse proxy Panel's Edit",
    app: "proxy:grafana.home.example",
    confirm: "Save",
    open: () => {
      fireEvent.click(screen.getByRole("button", { name: "Edit Reverse proxy" }));
      const form = within(panel("Reverse proxy"));
      fireEvent.change(form.getByLabelText("Port"), { target: { value: "3001" } });
      fireEvent.click(form.getByRole("checkbox", { name: "Websockets" }));
      fireEvent.click(form.getByRole("button", { name: "Save" }));
    },
    action: {
      changeProxyHost: {
        id: "proxy:grafana.home.example",
        proxyHost: { forwardScheme: "http", forwardHost: "198.18.1.204", forwardPort: 3001, certificateId: "3", blockExploits: true, websockets: true, forceSsl: true, http2: true },
      },
    },
  },
  {
    name: "Remove reverse proxy…",
    app: "proxy:grafana.home.example",
    confirm: "Remove reverse proxy",
    open: () => {
      fireEvent.click(screen.getByRole("button", { name: "Edit Reverse proxy" }));
      fireEvent.click(within(panel("Reverse proxy")).getByRole("button", { name: "Remove reverse proxy…" }));
    },
    action: { removeReverseProxy: { id: "proxy:grafana.home.example", address: "198.18.1.204" } },
  },
  {
    name: "Add reverse proxy…",
    app: "app:5a5a5a5a5a5a5a5a",
    confirm: "Add reverse proxy",
    open: async () => {
      fireEvent.click(within(panel("Reverse proxy")).getByRole("button", { name: "Add reverse proxy…" }));
      // The certificates load with the form.
      await settle();
      const form = within(panel("Reverse proxy"));
      fireEvent.change(form.getByLabelText("Scheme"), { target: { value: "https" } });
      fireEvent.change(form.getByLabelText("Port"), { target: { value: "5001" } });
      fireEvent.click(form.getByRole("button", { name: "Save" }));
    },
    action: {
      addReverseProxy: {
        id: "app:5a5a5a5a5a5a5a5a",
        proxyHost: { forwardScheme: "https", forwardHost: "198.18.1.20", forwardPort: 5001, certificateId: "3", blockExploits: true, forceSsl: true, http2: true },
      },
    },
  },
  {
    name: "a Warning's Save again",
    app: "proxy:jellyfin.home.example",
    confirm: "Save again",
    open: () => fireEvent.click(within(within(pane()).getByRole("region", { name: "Proxy Host offline" })).getByRole("button", { name: "Save again" })),
    action: { fix: { id: "proxy:jellyfin.home.example", fix: { kind: "FIX_KIND_SAVE_AGAIN", proxyHostId: "7" } } },
  },
];

describe.each(ways)("$name", (way) => {
  it("shows the Plan in a Dialog, and runs that Plan once confirmed", async () => {
    const calls = await open(way.app, { RunAction: ranThrough({ appId: way.app, done: 2 }) });
    await way.open();
    await settle();

    expect(plans(calls).at(-1)).toEqual({ action: way.action });
    expect(steps()).toEqual([expect.stringMatching(/^Update Proxy Host grafana\.home\.exampleNginx Proxy Manager.*3000 → http:\/\/198\.18\.1\.204:3001/), expect.stringMatching(/^Update the App Grafanaddash/)]);
    fireEvent.click(dialog().getByRole("button", { name: way.confirm }));
    await settle();

    expect(runs(calls)).toEqual([{ method: "RunAction", body: { action: way.action, plan } }]);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("shows each step running, then where it stopped, nothing rolled back", async () => {
    await open(way.app, { RunAction: () => pending() });
    await way.open();
    await settle();
    fireEvent.click(dialog().getByRole("button", { name: way.confirm }));
    await settle();
    expect(steps().map(state)).toEqual(["running…", "running…"]);
    expect((dialog().getByRole("button", { name: way.confirm }) as HTMLButtonElement).disabled).toBe(true);

    cleanup();
    const stopped = "Stopped at step 2 of 2, Update the App Grafana: database is locked. Done: Update Proxy Host grafana.home.example. Nothing was rolled back.";
    const ran = [[step.running, step.pending], [step.done, step.pending], [step.done, step.running], [step.done, step.failed]].map((steps) => ({ steps }));
    await open(way.app, { RunAction: failing("unavailable", stopped, RunActionResponseSchema, { done: 1 }, ran) });
    await way.open();
    await settle();
    fireEvent.click(dialog().getByRole("button", { name: way.confirm }));
    const shown = watch(() => steps().map(state));
    await settle();
    shown.stop();
    expect(shown.seen).toEqual([
      [undefined, undefined],
      ["running…", "running…"],
      ["running…", undefined],
      ["done", undefined],
      ["done", "running…"],
      ["done", "failed"],
    ]);
    expect(dialog().getByRole("alert").textContent).toContain(stopped);
    expect((dialog().getByRole("button", { name: way.confirm }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows the new Plan when the systems changed since, and confirming again runs it", async () => {
    const changed = { steps: [{ system: System.PFSENSE, title: "Remove alias grafana.home.example from rp.home.example" }] };
    let n = 0;
    const calls = await open(way.app, {
      RunAction: () => (++n === 1 ? failing("aborted", "changed since you looked: confirm the new Plan", PlanSchema, changed) : ranThrough({ appId: way.app, done: 1 }))(),
    });
    await way.open();
    await settle();
    fireEvent.click(dialog().getByRole("button", { name: way.confirm }));
    await settle();

    expect(dialog().getByRole("status").textContent).toMatch(/Changed since you looked/);
    expect(steps()).toEqual([expect.stringMatching(/^Remove alias grafana\.home\.example from rp\.home\.example/)]);
    fireEvent.click(dialog().getByRole("button", { name: way.confirm }));
    await settle();
    expect((runs(calls)[1].body as { plan: object }).plan).toEqual({ steps: [{ system: "SYSTEM_PFSENSE", title: "Remove alias grafana.home.example from rp.home.example" }] });
  });

  it("says why a Plan can't run, and keeps it from running", async () => {
    const blocked = "ddash's user in Nginx Proxy Manager can only view Proxy Hosts: set them to Manage to change them.";
    await open(way.app, { PlanAction: () => Response.json({ plan: { ...plan, blocked: [blocked] } }) });
    await way.open();
    await settle();

    expect(dialog().getByText(blocked)).toBeTruthy();
    expect((dialog().getByRole("button", { name: way.confirm }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows the Plan's notes, and still lets it run", async () => {
    const note = "nasui.home.example stays an alias of the Reverse Proxy's Host Override, with no Proxy Host to serve it: remove it there, or add a reverse proxy again.";
    await open(way.app, { PlanAction: () => Response.json({ plan: { ...plan, notes: [note] } }) });
    await way.open();
    await settle();

    expect(dialog().getByText(note)).toBeTruthy();
    expect((dialog().getByRole("button", { name: way.confirm }) as HTMLButtonElement).disabled).toBe(false);
  });
});

it("shows each step's state as the run streams it, then done", async () => {
  const calls = await open("proxy:grafana.home.example", { RunAction: ranThrough({ appId: "proxy:grafana.home.example", done: 2 }) });
  fireEvent.click(within(pane()).getByRole("button", { name: "Delete…" }));
  await settle();
  fireEvent.click(dialog().getByRole("button", { name: "Delete" }));
  const shown = watch(() => (screen.queryByRole("dialog") ? steps().map(state) : "closed"));
  await settle();
  shown.stop();

  expect(shown.seen).toEqual([
    [undefined, undefined],
    ["running…", "running…"],
    ["running…", undefined],
    ["done", undefined],
    ["done", "running…"],
    ["done", "done"],
    "closed",
  ]);
  expect(calls.map((c) => c.method)).not.toContain("GetRun");
});

it("closing the Dialog mid-run stops following it and refetches, the Action running on", async () => {
  let signal: AbortSignal | undefined;
  const calls = await open("proxy:grafana.home.example", {
    RunAction: (_, init) => {
      signal = init?.signal ?? undefined;
      return streamed([{ steps: [step.done, step.running] }], { openUntil: signal });
    },
  });
  fireEvent.click(within(pane()).getByRole("button", { name: "Delete…" }));
  await settle();
  fireEvent.click(dialog().getByRole("button", { name: "Delete" }));
  await settle();
  expect(steps().map(state)).toEqual(["done", "running…"]);
  expect((dialog().getByRole("button", { name: "Cancel" }) as HTMLButtonElement).disabled).toBe(true);
  const lists = calls.filter((c) => c.method === "ListApps").length;

  fireEvent.click(dialog().getByRole("button", { name: "Close" }));
  await settle();

  expect(signal?.aborted).toBe(true);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(calls.filter((c) => c.method === "ListApps").length).toBeGreaterThan(lists);
  // Not done: the pane stays on the App.
  expect(search).toBe("?app=proxy%3Agrafana.home.example");
});

it("empties the pane once the App is deleted", async () => {
  await open("proxy:grafana.home.example", { RunAction: ranThrough({ appId: "proxy:grafana.home.example", done: 2 }) });
  fireEvent.click(within(pane()).getByRole("button", { name: "Delete…" }));
  await settle();
  fireEvent.click(dialog().getByRole("button", { name: "Delete" }));
  await settle();
  expect(search).toBe("");
});

it("notes a name kept for orphaned App settings, linking them", async () => {
  const note = {
    hostname: "paperless.home.example",
    kind: "NAME_NOTE_KIND_ORPHAN",
    text: "paperless.home.example is kept for the orphaned App settings Documents: reattach or delete them first.",
    appId: "proxy:paperless.home.example",
    name: "Documents",
  };
  await open("proxy:grafana.home.example", { PlanAction: () => Response.json({ plan: { ...plan, names: [note] } }) });
  fireEvent.click(screen.getByRole("button", { name: "Edit Address and DNS" }));
  fireEvent.change(within(panel("Address and DNS")).getByLabelText("Aliases"), { target: { value: "paperless.home.example" } });
  fireEvent.click(within(panel("Address and DNS")).getByRole("button", { name: "Save" }));
  await settle();

  expect(screen.getByRole("dialog").textContent).toContain(note.text);
  expect(dialog().getByRole("link", { name: "Documents" }).getAttribute("href")).toBe("/?app=proxy%3Apaperless.home.example");
  expect((dialog().getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(true);
});

it("opens Delete from a Warning's Delete App…, and the Reverse proxy Panel's Edit from Pick a certificate…", async () => {
  const printer = split.apps.find((a) => a.name === "Printer")!;
  const unignored = { ...split, apps: split.apps.map((a) => (a === printer ? { ...a, warnings: [{ kind: "WARNING_KIND_NO_TLS", fixes: [{ kind: "FIX_KIND_PICK_CERTIFICATE" }] }] } : a)) };
  await open("proxy:jellyfin.home.example");
  fireEvent.click(within(within(pane()).getByRole("region", { name: "Proxy Host offline" })).getByRole("button", { name: "Delete App…" }));
  await settle();
  expect(screen.getByRole("dialog", { name: "Delete Jellyfin" })).toBeTruthy();

  cleanup();
  await open("proxy:printer.home.example", { ListApps: () => Response.json(unignored) });
  fireEvent.click(within(within(pane()).getByRole("region", { name: "No TLS" })).getByRole("button", { name: "Pick a certificate…" }));
  expect(within(panel("Reverse proxy")).getByLabelText("Certificate")).toBeTruthy();
});

it("prefills Add reverse proxy… with the Host Override's address and the certificate covering the name", async () => {
  await open("app:5a5a5a5a5a5a5a5a");
  fireEvent.click(within(panel("Reverse proxy")).getByRole("button", { name: "Add reverse proxy…" }));
  await settle();
  const form = within(panel("Reverse proxy"));
  expect((form.getByLabelText("Upstream host") as HTMLInputElement).value).toBe("198.18.1.20");
  expect((form.getByLabelText("Certificate") as HTMLSelectElement).value).toBe("3");
});
