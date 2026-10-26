import { PlanSchema, RunActionResponseSchema } from "@ddash/proto/apps/apps_pb";
import { act, cleanup, fireEvent, screen, within } from "@testing-library/react";
import { useLocation } from "react-router";
import { afterEach, expect, it } from "vitest";
import { pending, settle } from "@ddash/plugin/testing";
import { planDelay } from "./add-app";
import plugin from "./index";
import { actions, certificates, failing, renderScoped, split } from "./testing";

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

/** PlanAction's request, as the fake core got it. */
interface PlanRequest {
  action?: { add?: { url?: string; reach?: string } };
}

/** The Plan of a proxied Add of wiki.home.example (Connect JSON). */
const wikiPlan = {
  steps: [
    {
      system: "SYSTEM_NGINX_PROXY_MANAGER",
      title: "Create Proxy Host wiki.home.example",
      values: [{ label: "Upstream", after: "http://198.18.2.11:3000" }],
    },
    {
      system: "SYSTEM_PFSENSE",
      title: "Add alias wiki.home.example to nginx.home.example",
      values: [{ label: "Host Override", before: "nginx.home.example → 198.18.1.210", after: "nginx.home.example → 198.18.1.210" }],
    },
    { system: "SYSTEM_DDASH", title: "Create the App Wiki" },
  ],
};

/** PlanAction answering plan for any URL, and blocking an empty one as the server does. */
const planning = (plan: object) => (body: unknown) =>
  Response.json({ plan: (body as PlanRequest).action?.add?.url ? plan : { blocked: ["Give the App's URL."] } });

/** Shows the Page adding an App, with the Plugins named installed. */
const openAdd = async (answers = {}, installed = ["apps"], list: object = split) => {
  const calls = renderScoped(<Page />, { ListApps: () => Response.json(list), PlanAction: planning(wikiPlan), ...answers }, installed, "/?app=new");
  await settle();
  return calls;
};

const form = () => within(screen.getByRole("region", { name: "Add App" }));
const will = () => within(screen.getByRole("region", { name: "ddash will" }));
const radio = (name: string) => form().getByRole("radio", { name }) as HTMLButtonElement;
const box = (name: string) => form().getByRole("checkbox", { name }) as HTMLInputElement;
const addButton = () => form().getByRole("button", { name: "Add App" }) as HTMLButtonElement;

/** Waits out the Plan's debounce, then for its answer. */
const debounced = async () => {
  await act(() => new Promise((r) => setTimeout(r, planDelay + 50)));
  await settle();
};

const change = (label: string, value: string) => fireEvent.change(form().getByLabelText(label), { target: { value } });

/** Types into the form's fields, then waits for the Plan. */
const fill = async (fields: Record<string, string>) => {
  for (const [label, value] of Object.entries(fields)) change(label, value);
  await debounced();
};

const plans = (calls: { method: string; body: unknown }[]) => calls.filter((c) => c.method === "PlanAction").map((c) => c.body as PlanRequest);

/** The "ddash will" steps as "title · system · state". */
const steps = () =>
  will()
    .getAllByRole("listitem")
    .filter((li) => li.parentElement?.tagName === "OL")
    .map((li) => li.textContent);

/** A step's state as it says it; undefined before Add. */
const state = (step: string | null) => step?.match(/running…|done|failed|not run/)?.[0];

it("adds a proxied App by default: the debounced Plan beside the form, and Add sending the Plan on screen", async () => {
  const calls = await openAdd({ RunAction: () => Response.json({ appId: "proxy:wiki.home.example", done: 3 }) });

  expect(radio("Reverse proxy").getAttribute("aria-checked")).toBe("true");
  await fill({ URL: "https://wiki.home.example", Name: "Wiki", "Upstream host": "198.18.2.11", Port: "3000" });

  // One Plan on open, one once the typing stopped.
  expect(plans(calls)).toHaveLength(2);
  expect(plans(calls)[1]).toEqual({
    action: {
      add: {
        url: "https://wiki.home.example",
        name: "Wiki",
        reach: "REACH_PROXIED",
        proxyHost: { forwardScheme: "http", forwardHost: "198.18.2.11", forwardPort: 3000, certificateId: "3", blockExploits: true, forceSsl: true, http2: true },
      },
    },
  });
  expect(steps()).toEqual([
    expect.stringMatching(/^Create Proxy Host wiki\.home\.exampleNginx Proxy Manager.*Upstream: http:\/\/198\.18\.2\.11:3000/),
    expect.stringMatching(/^Add alias wiki\.home\.example to nginx\.home\.examplepfSense/),
    expect.stringMatching(/^Create the App Wikiddash/),
  ]);

  expect(addButton().disabled).toBe(false);
  fireEvent.click(addButton());
  await settle();

  const [run] = actions(calls);
  expect(run).toEqual({ method: "RunAction", body: { action: plans(calls)[1].action, plan: wikiPlan } });
  expect(search).toBe("?app=proxy%3Awiki.home.example");
});

it("picks the Reach: Host Override asks for an Address, Link only for nothing more, a URL outside the home domain forces Link only", async () => {
  const calls = await openAdd();

  fireEvent.click(radio("Host Override"));
  await fill({ URL: "https://printer.home.example", Address: "198.18.1.60" });
  expect(form().queryByLabelText("Upstream host")).toBeNull();
  expect(plans(calls).at(-1)).toEqual({ action: { add: { url: "https://printer.home.example", reach: "REACH_NOT_PROXIED", address: "198.18.1.60" } } });

  fireEvent.click(radio("Link only"));
  await debounced();
  expect(form().queryByLabelText("Address")).toBeNull();
  expect(form().queryByLabelText("Upstream host")).toBeNull();
  expect(plans(calls).at(-1)).toEqual({ action: { add: { url: "https://printer.home.example", reach: "REACH_LINK_ONLY" } } });

  fireEvent.click(radio("Reverse proxy"));
  await fill({ URL: "https://wiki.example.org" });
  expect(radio("Link only").getAttribute("aria-checked")).toBe("true");
});

it("disables the Reaches writing to a source whose last read failed, saying so", async () => {
  await openAdd({}, ["apps"], { ...split, hostOverrides: { error: "pfsense-api unavailable: timeout" } });

  expect(radio("Reverse proxy").disabled).toBe(true);
  expect(radio("Host Override").disabled).toBe(true);
  expect(radio("Link only").getAttribute("aria-checked")).toBe("true");
  expect(form().getByRole("radiogroup", { name: "Reach" }).parentElement!.textContent).toContain("Can't check pfSense right now: its last read failed.");

  cleanup();
  await openAdd({}, ["apps"], { ...split, proxyHosts: { error: "sign-in refused" } });
  expect(radio("Reverse proxy").disabled).toBe(true);
  expect(radio("Host Override").disabled).toBe(false);
  expect(radio("Host Override").getAttribute("aria-checked")).toBe("true");
});

it("follows Nginx Proxy Manager's toggle rules: no certificate, no Force SSL or HTTP/2; no Force SSL, no HSTS", async () => {
  const calls = await openAdd();
  await fill({ URL: "https://wiki.home.example", "Upstream host": "198.18.2.11" });
  expect([box("Force SSL").checked, box("HTTP/2").checked, box("HSTS").disabled]).toEqual([true, true, false]);

  fireEvent.click(box("HSTS"));
  change("Certificate", "0");
  await debounced();
  for (const name of ["Force SSL", "HTTP/2", "HSTS"]) {
    expect(box(name).checked, name).toBe(false);
    expect(box(name).disabled, name).toBe(true);
  }
  expect(plans(calls).at(-1)?.action?.add).toMatchObject({ proxyHost: { forwardScheme: "http", forwardHost: "198.18.2.11", forwardPort: 80, blockExploits: true } });
  expect(JSON.stringify(plans(calls).at(-1))).not.toMatch(/forceSsl|http2|hsts|certificateId/);

  change("Certificate", "3");
  fireEvent.click(box("Force SSL"));
  expect([box("Force SSL").checked, box("HTTP/2").checked, box("HSTS").checked, box("HSTS").disabled]).toEqual([false, true, false, true]);
  expect(box("HSTS subdomains").disabled).toBe(true);
});

it("defaults the certificate to the one covering the hostname, until one is picked", async () => {
  const exact = { id: "5", provider: "other", niceName: "wiki.home.example", domainNames: ["wiki.home.example"], expiresOn: certificates.certificates[0].expiresOn };
  await openAdd({ ListCertificates: () => Response.json({ certificates: [...certificates.certificates, exact] }) });
  const cert = () => (form().getByLabelText("Certificate") as HTMLSelectElement).value;

  await fill({ URL: "https://wiki.home.example" });
  expect(cert()).toBe("5");
  await fill({ URL: "https://grafana.home.example" });
  expect(cert()).toBe("3");
  expect(form().getByRole("option", { name: "*.home.example (covers it)" })).toBeTruthy();
  await fill({ URL: "https://a.b.home.example" });
  expect(cert()).toBe("0");

  change("Certificate", "3");
  await fill({ URL: "https://wiki.home.example" });
  expect(cert()).toBe("3");
});

it("says why the name isn't free, one message linking what took it, and keeps Add disabled", async () => {
  await openAdd({
    PlanAction: planning({
      ...wikiPlan,
      names: [
        {
          hostname: "grafana.home.example",
          kind: "NAME_NOTE_KIND_APP",
          text: "grafana.home.example is taken by Grafana (Hidden).",
          appId: "proxy:grafana.home.example",
          name: "Grafana",
          hidden: true,
        },
      ],
    }),
  });
  await fill({ URL: "https://grafana.home.example" });

  const url = form().getByLabelText("URL").closest("div")!;
  expect(url.textContent).toContain("grafana.home.example is taken by Grafana (Hidden).");
  expect(within(url).getByRole("link", { name: "Grafana" }).getAttribute("href")).toBe("/?app=proxy%3Agrafana.home.example");
  expect(addButton().disabled).toBe(true);
});

it("names the Device whose name it is, linking to it", async () => {
  await openAdd(
    {
      PlanAction: planning({
        ...wikiPlan,
        names: [
          {
            hostname: "homeassistant.home.example",
            kind: "NAME_NOTE_KIND_DEVICE",
            text: "homeassistant.home.example is the Device homeassistant's name. To link to it as it is, choose Link only.",
            name: "homeassistant",
            deviceMac: "02:61:c0:a3:8a:16",
          },
        ],
      }),
    },
    ["apps", "network"],
  );
  await fill({ URL: "https://homeassistant.home.example" });

  const url = form().getByLabelText("URL").closest("div")!;
  expect(url.textContent).toContain("homeassistant.home.example is the Device homeassistant's name. To link to it as it is, choose Link only.");
  expect(within(url).getByRole("link", { name: "homeassistant" }).getAttribute("href")).toBe("/network/devices/02:61:c0:a3:8a:16");
  expect(addButton().disabled).toBe(true);
});

it("notes the orphaned App settings Add reattaches, without blocking it", async () => {
  const note = { hostname: "paperless.home.example", kind: "NAME_NOTE_KIND_REATTACH", text: "Settings were kept for Documents: adding it reattaches them, with its Check history.", appId: "proxy:paperless.home.example", name: "Documents" };
  await openAdd({ PlanAction: planning({ ...wikiPlan, names: [note] }) });
  await fill({ URL: "https://paperless.home.example", "Upstream host": "198.18.1.30" });

  const url = form().getByLabelText("URL").closest("div")!;
  expect(url.textContent).toContain("Settings were kept for Documents: adding it reattaches them, with its Check history.");
  expect(within(url).getByRole("link", { name: "Documents" })).toBeTruthy();
  expect(addButton().disabled).toBe(false);
});

it("shows why the Plan can't run, such as the Reverse Proxy's Host Override problem, and keeps Add disabled", async () => {
  const blocked = "No Host Override points at the Reverse Proxy (198.18.1.210): create one to add proxied Apps.";
  await openAdd({ PlanAction: planning({ ...wikiPlan, blocked: [blocked] }) });
  await fill({ URL: "https://wiki.home.example", "Upstream host": "198.18.2.11" });

  expect(form().getByText(blocked)).toBeTruthy();
  expect(addButton().disabled).toBe(true);

  cleanup();
  await openAdd({ PlanAction: planning({ ...wikiPlan, blocked: ["Give the App's URL."] }) });
  // Nothing typed yet: nothing to say.
  expect(form().queryByText("Give the App's URL.")).toBeNull();
});

it("shows each step's progress after Add, and where it stopped, nothing rolled back", async () => {
  await openAdd({ RunAction: () => pending() });
  await fill({ URL: "https://wiki.home.example", "Upstream host": "198.18.2.11" });
  fireEvent.click(addButton());
  await settle();
  expect(steps().map(state)).toEqual(["running…", "running…", "running…"]);
  expect(addButton().disabled).toBe(true);

  cleanup();
  const stopped = "Stopped at step 2 of 3, Add alias wiki.home.example to nginx.home.example: pfSense refused it. Done: Create Proxy Host wiki.home.example. Nothing was rolled back.";
  await openAdd({ RunAction: failing("unavailable", stopped, RunActionResponseSchema, { done: 1 }, 503) });
  await fill({ URL: "https://wiki.home.example", "Upstream host": "198.18.2.11" });
  fireEvent.click(addButton());
  await settle();
  expect(steps().map(state)).toEqual(["done", "failed", "not run"]);
  expect(form().getByRole("alert").textContent).toContain(stopped);
  expect(search).toBe("?app=new");
});

it("shows the new Plan when the systems changed since the Operator looked, and Add confirms it", async () => {
  const changed = { steps: [{ system: 2, title: "Create Proxy Host wiki.home.example" }, { system: 3, title: "Add alias wiki.home.example to rp.home.example" }, { system: 1, title: "Create the App Wiki" }] };
  let runs = 0;
  const calls = await openAdd({
    RunAction: () => {
      runs++;
      return runs === 1 ? failing("aborted", "changed since you looked: confirm the new Plan", PlanSchema, changed, 409)() : Response.json({ appId: "proxy:wiki.home.example", done: 3 });
    },
  });
  await fill({ URL: "https://wiki.home.example", "Upstream host": "198.18.2.11" });
  fireEvent.click(addButton());
  await settle();

  expect(form().getByRole("status").textContent).toMatch(/Changed since you looked/);
  expect(steps()[1]).toMatch(/^Add alias wiki\.home\.example to rp\.home\.example/);
  fireEvent.click(addButton());
  await settle();

  const runs2 = actions(calls).filter((c) => c.method === "RunAction");
  expect((runs2[1].body as { plan: { steps: { title: string }[] } }).plan.steps[1].title).toBe("Add alias wiki.home.example to rp.home.example");
  expect(search).toBe("?app=proxy%3Awiki.home.example");
});

it("reports what was added when the Apps couldn't be re-read, with a way to it", async () => {
  await openAdd({ RunAction: () => Response.json({ appId: "proxy:wiki.home.example", done: 3, reread: "couldn't re-read pfsense-api: timeout" }) });
  await fill({ URL: "https://wiki.home.example", "Upstream host": "198.18.2.11" });
  fireEvent.click(addButton());
  await settle();

  expect(steps().map(state)).toEqual(["done", "done", "done"]);
  expect(form().getByRole("status").textContent).toContain("couldn't re-read pfsense-api: timeout");
  fireEvent.click(form().getByRole("button", { name: "Open the App" }));
  expect(search).toBe("?app=proxy%3Awiki.home.example");
});
