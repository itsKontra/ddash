import { create, toBinary } from "@bufbuild/protobuf";
import { ConfigErrorsSchema } from "@ddash/proto/ddash/core/core_pb";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { createQueryClient } from "@ddash/plugin";
import { MemoryRouter } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "./app";

// Config forms as GetConfigForm answers them in JSON, by owner.
const ntfyForm = () => ({
  fields: [
    { name: "server", kind: "KIND_STRING", label: "Server", format: "url", default: "https://ntfy.sh", placeholder: "https://ntfy.sh" },
    { name: "topic", kind: "KIND_STRING", label: "Topic", help: "Where Notifications go.", required: true },
    { name: "token", kind: "KIND_STRING", label: "Token", secret: true },
    { name: "priority", kind: "KIND_INT", label: "Priority", min: "1", max: "5", default: "3", placeholder: "3" },
    { name: "markdown", kind: "KIND_BOOL", label: "Markdown", default: "false", placeholder: "false" },
    {
      name: "tone",
      kind: "KIND_ENUM",
      label: "Tone",
      default: "TONE_WARM",
      placeholder: "TONE_WARM",
      options: [
        { value: "TONE_WARM", label: "Warm" },
        { value: "TONE_COLD", label: "Cold" },
      ],
    },
    { name: "tags", kind: "KIND_STRING", label: "Tags", repeated: true },
  ],
  values: { topic: "ddash", priority: "4", tags: ["a", "b"] },
  secretsSet: ["token"],
});

const dockerForm = () => ({
  fields: [
    {
      name: "hosts",
      kind: "KIND_TABLE",
      label: "Docker hosts",
      repeated: true,
      columns: [
        { name: "name", kind: "KIND_STRING", label: "Name", required: true },
        { name: "url", kind: "KIND_STRING", label: "Socket-proxy URL", required: true, format: "url" },
      ],
    },
  ],
  values: { hosts: [{ name: "media", url: "tcp://198.18.2.10:2375" }] },
});

type Comp = { name: string; state?: string; message?: string; configurable?: boolean; needsConfig?: boolean };

let components: Comp[];
let forms: Record<string, ReturnType<typeof ntfyForm> | ReturnType<typeof dockerForm>>;
let saves: Record<string, unknown>[];
let saveAnswer: () => Response;
let installAnswer: Record<string, unknown>;
let testRuns: Record<string, unknown>[];
let testAnswer: () => Response;

beforeEach(() => {
  saves = [];
  components = [
    { name: "ntfy-api", state: "ok", configurable: true },
    { name: "docker-api", state: "ok", configurable: true },
    { name: "checks", state: "ok" },
  ];
  forms = { "ntfy-api": ntfyForm(), "docker-api": dockerForm() };
  saveAnswer = () => Response.json({ status: { name: "ntfy-api", state: "ok", configurable: true } });
  installAnswer = {};
  testRuns = [];
  testAnswer = () => Response.json({});
  window.localStorage.clear();
});

afterEach(cleanup);

const fetchFake = async (input: RequestInfo | URL, init?: RequestInit) => {
  const method = String(input).split("/").pop()!;
  const body = JSON.parse(new TextDecoder().decode(init?.body as Uint8Array));
  switch (method) {
    case "ListCatalogue":
      return Response.json({
        release: "v1",
        plugins: [
          { name: "checks", title: "Checks", installed: false, components: [{ name: "checks" }, { name: "ntfy-api" }] },
          { name: "docker", title: "Docker", installed: true, components },
        ],
      });
    case "GetConfigForm":
      return Response.json(forms[body.owner]);
    case "SaveConfig":
      saves.push(body);
      return saveAnswer();
    case "Install":
      return Response.json(installAnswer);
    case "RunTestAction":
      testRuns.push(body);
      return testAnswer();
    default:
      return Response.json({});
  }
};

const settle = () => act(() => new Promise((r) => setTimeout(r, 20)));

async function boot() {
  render(
    <MemoryRouter initialEntries={["/plugins"]}>
      <App client={createQueryClient()} fetch={vi.fn(fetchFake)} importBundle={async () => ({})} />
    </MemoryRouter>,
  );
  await settle();
}

async function configure(component: string) {
  fireEvent.click(within(healthRow(component)).getByRole("button", { name: "Configure" }));
  await settle();
  return screen.getByRole("dialog", { name: component });
}

const healthRow = (component: string) =>
  within(within(screen.getByRole("listitem", { name: "Docker" })).getByRole("list", { name: "Component health" })).getByRole("listitem", { name: component });

const save = async (form: HTMLElement) => {
  fireEvent.click(within(form).getByRole("button", { name: "Save and restart" }));
  await settle();
};

const input = (form: HTMLElement, label: string) => within(form).getByLabelText(label) as HTMLInputElement;

it("opens a Component's form in a dialog titled by it, saying that saving restarts it", async () => {
  await boot();

  const form = await configure("ntfy-api");

  expect(within(form).getByText("Configure")).toBeTruthy();
  const note = document.getElementById(form.getAttribute("aria-describedby")!);
  expect(note?.textContent).toMatch(/Saving restarts ntfy-api/);
});

it("closes the form on Cancel without saving", async () => {
  await boot();
  const form = await configure("ntfy-api");

  fireEvent.change(input(form, "Topic"), { target: { value: "other" } });
  fireEvent.click(within(form).getByRole("button", { name: "Cancel" }));
  await settle();

  expect(screen.queryByRole("dialog")).toBeNull();
  expect(saves).toEqual([]);
});

it("renders each field from the schema: labels, help, defaults as placeholders, stored values", async () => {
  await boot();

  const form = await configure("ntfy-api");

  expect(input(form, "Server").value).toBe("");
  expect(input(form, "Server").placeholder).toBe("https://ntfy.sh");
  expect(input(form, "Topic").value).toBe("ddash");
  expect(input(form, "Topic").getAttribute("aria-describedby")).toBe(within(form).getByText("Where Notifications go.").id);
  expect(input(form, "Priority").value).toBe("4");
  expect(within(form).getByRole("switch", { name: "Markdown" }).getAttribute("aria-checked")).toBe("false");
  const tone = within(form).getByLabelText("Tone") as HTMLSelectElement;
  expect([...tone.options].map((o) => o.textContent)).toEqual(["Default (Warm)", "Warm", "Cold"]);
  expect(within(form).getAllByRole("textbox", { name: /^Tags/ }).map((t) => (t as HTMLInputElement).value)).toEqual(["a", "b"]);
});

it("never shows a secret: only set or unset, with Replace and Clear", async () => {
  await boot();
  const form = await configure("ntfy-api");

  const token = within(form).getByRole("group", { name: "Token" });
  expect(token.textContent).toMatch(/set/);
  expect(within(token).queryByRole("textbox")).toBeNull();
  expect(within(token).getByRole("button", { name: "Replace" })).toBeTruthy();
  expect(within(token).getByRole("button", { name: "Clear" })).toBeTruthy();
});

it("saves only what is set, leaving an unchanged secret out, and shows the health polled after Start", async () => {
  saveAnswer = () => Response.json({ status: { name: "ntfy-api", state: "down", message: "401 Unauthorized", configurable: true } });
  await boot();
  const form = await configure("ntfy-api");

  fireEvent.change(input(form, "Server"), { target: { value: "https://ntfy.home.example" } });
  await save(form);

  expect(saves).toEqual([
    { owner: "ntfy-api", values: { server: "https://ntfy.home.example", topic: "ddash", priority: "4", tags: ["a", "b"] } },
  ]);
  expect(screen.getByRole("status").textContent).toMatch(/down.*401 Unauthorized/);
});

it("replaces or clears a secret", async () => {
  await boot();
  let form = await configure("ntfy-api");

  fireEvent.click(within(within(form).getByRole("group", { name: "Token" })).getByRole("button", { name: "Replace" }));
  fireEvent.change(input(form, "Token"), { target: { value: "tk_new" } });
  await save(form);
  expect(saves.at(-1)).toMatchObject({ values: { token: "tk_new" } });

  form = screen.getByRole("dialog", { name: "ntfy-api" });
  fireEvent.click(within(within(form).getByRole("group", { name: "Token" })).getByRole("button", { name: "Clear" }));
  await save(form);
  expect(saves.at(-1)).toMatchObject({ clearSecrets: ["token"] });
  expect(saves.at(-1)?.values).not.toHaveProperty("token");
});

it("resets a field to its default by unsetting it", async () => {
  await boot();
  const form = await configure("ntfy-api");

  fireEvent.click(within(form).getByRole("button", { name: "Reset Priority" }));
  await save(form);

  expect(saves[0].values).not.toHaveProperty("priority");
  expect(input(form, "Priority").placeholder).toBe("3");
});

it("checks required, format and min/max before saving, showing each error under its field", async () => {
  await boot();
  const form = await configure("ntfy-api");

  fireEvent.change(input(form, "Topic"), { target: { value: "" } });
  fireEvent.change(input(form, "Server"), { target: { value: "ntfy.sh" } });
  fireEvent.change(input(form, "Priority"), { target: { value: "9" } });
  await save(form);

  expect(saves).toEqual([]);
  expect(within(form).getByText("is required").closest("[data-field]")?.getAttribute("data-field")).toBe("topic");
  expect(input(form, "Topic").getAttribute("aria-invalid")).toBe("true");
  expect(input(form, "Topic").getAttribute("aria-describedby")).toBe(within(form).getByText("is required").id);
  expect(within(form).getByText("must be a URL like https://host.example").closest("[data-field]")?.getAttribute("data-field")).toBe("server");
  expect(within(form).getByText("must be at most 5").closest("[data-field]")?.getAttribute("data-field")).toBe("priority");
});

it("edits a table of rows and shows Start's errors under their cells", async () => {
  const detail = toBinary(ConfigErrorsSchema, create(ConfigErrorsSchema, { errors: [{ path: "hosts[1].url", message: "must use tcp://" }] }));
  saveAnswer = () =>
    Response.json(
      { code: "invalid_argument", message: "invalid config", details: [{ type: "ddash.core.ConfigErrors", value: btoa(String.fromCharCode(...detail)) }] },
      { status: 400 },
    );
  await boot();
  const form = await configure("docker-api");

  const table = within(form).getByRole("table", { name: "Docker hosts" });
  expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Socket-proxy URL", ""]);
  fireEvent.click(within(form).getByRole("button", { name: "Add a row to Docker hosts" }));
  fireEvent.change(within(table).getByLabelText("Name, row 2"), { target: { value: "nas" } });
  fireEvent.change(within(table).getByLabelText("Socket-proxy URL, row 2"), { target: { value: "https://nas.home.example" } });
  await save(form);

  expect(saves[0]).toEqual({
    owner: "docker-api",
    values: { hosts: [{ name: "media", url: "tcp://198.18.2.10:2375" }, { name: "nas", url: "https://nas.home.example" }] },
  });
  expect(within(form).getByText("must use tcp://").closest("[data-field]")?.getAttribute("data-field")).toBe("hosts[1].url");

  fireEvent.click(within(table).getByRole("button", { name: "Remove row 1" }));
  expect((within(table).getByLabelText("Name, row 1") as HTMLInputElement).value).toBe("nas");
});

it("marks a Component needing config and opens its form with the stored config's errors", async () => {
  components[0] = { name: "ntfy-api", state: "unconfigured", configurable: true, needsConfig: true };
  forms["ntfy-api"] = { ...ntfyForm(), values: {}, errors: [{ path: "topic", message: "is required" }] } as never;
  await boot();

  expect(within(healthRow("ntfy-api")).getByText("needs config")).toBeTruthy();
  expect(within(healthRow("ntfy-api")).getByRole("button", { name: "Configure" }).className.split(" ")).toContain("bg-primary");
  const form = await configure("ntfy-api");

  expect(within(form).getByText("is required").closest("[data-field]")?.getAttribute("data-field")).toBe("topic");
});

it("opens the forms of the unconfigured Components an install adds, in turn", async () => {
  installAnswer = { components: ["ntfy-api", "checks", "docker-api"] };
  components = [
    { name: "ntfy-api", state: "unconfigured", configurable: true },
    { name: "checks", state: "ok" },
    { name: "docker-api", state: "unconfigured", configurable: true },
  ];
  await boot();

  fireEvent.click(within(screen.getByRole("listitem", { name: "Checks" })).getByRole("button", { name: "Install" }));
  await settle();
  await settle();

  expect(screen.getByRole("dialog", { name: "ntfy-api" })).toBeTruthy();
  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Close" }));
  await settle();
  expect(screen.getByRole("dialog", { name: "docker-api" })).toBeTruthy();
  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Close" }));
  await settle();
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("keeps a form an install opened until it is closed, to show the health after saving", async () => {
  installAnswer = { components: ["ntfy-api"] };
  components = [{ name: "ntfy-api", state: "unconfigured", configurable: true }];
  saveAnswer = () => {
    components = [{ name: "ntfy-api", state: "ok", configurable: true }];
    return Response.json({ status: { name: "ntfy-api", state: "ok", configurable: true } });
  };
  await boot();
  fireEvent.click(within(screen.getByRole("listitem", { name: "Checks" })).getByRole("button", { name: "Install" }));
  await settle();
  await settle();

  await save(screen.getByRole("dialog", { name: "ntfy-api" }));
  await settle();

  expect(within(screen.getByRole("dialog", { name: "ntfy-api" })).getByRole("status").textContent).toMatch(/ok/);
});

it("shows a started Component's setup hint, with a button copying its block", async () => {
  const line = 'restrict,from="198.18.1.5",command="/usr/sbin/nginx -T" ssh-ed25519 AAAAC3Nza ddash';
  forms["docker-api"] = { ...dockerForm(), setupHint: { text: "Add this line to root's authorized_keys.", block: line } } as ReturnType<typeof dockerForm>;
  const writeText = vi.fn<(text: string) => Promise<void>>(async () => {});
  Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
  await boot();

  const form = await configure("docker-api");

  expect(within(form).getByText("Add this line to root's authorized_keys.").closest("[role=status]")).toBeTruthy();
  const block = within(form).getByRole("region", { name: "Setup" });
  expect(block.tagName).toBe("PRE");
  expect(block.textContent).toBe(line);
  fireEvent.click(within(form).getByRole("button", { name: "Copy" }));
  await settle();
  expect(writeText).toHaveBeenCalledWith(line);
  expect(within(form).getByRole("button", { name: "Copied" })).toBeTruthy();
});

it("shows no setup hint for a Component without one", async () => {
  await boot();

  const form = await configure("ntfy-api");

  expect(within(form).queryByRole("region", { name: "Setup" })).toBeNull();
});

// A form whose bypass_alias takes its choices from a getter (choices_from).
const aliasForm = (field: Record<string, unknown>, values: Record<string, unknown> = {}) =>
  ({ fields: [{ name: "bypass_alias", kind: "KIND_STRING", label: "Bypass alias", required: true, ...field }], values }) as unknown as ReturnType<typeof dockerForm>;

it("offers a field's choices to pick from, and saves the one picked", async () => {
  forms["docker-api"] = aliasForm({
    choices: [
      { value: "VPN_BYPASS", label: "VPN_BYPASS — Leaves through the WAN" },
      { value: "KILL_SWITCH", label: "KILL_SWITCH" },
    ],
  });
  await boot();
  const form = await configure("docker-api");

  const pick = within(form).getByLabelText("Bypass alias") as HTMLSelectElement;
  expect(pick.tagName).toBe("SELECT");
  expect([...pick.options].map((o) => o.textContent)).toEqual(["—", "VPN_BYPASS — Leaves through the WAN", "KILL_SWITCH"]);
  fireEvent.change(pick, { target: { value: "VPN_BYPASS" } });
  await save(form);

  expect(saves).toEqual([{ owner: "docker-api", values: { bypass_alias: "VPN_BYPASS" } }]);
});

it("keeps a stored value that is no longer among the choices", async () => {
  forms["docker-api"] = aliasForm({ choices: [{ value: "KILL_SWITCH", label: "KILL_SWITCH" }] }, { bypass_alias: "GONE" });
  await boot();
  const form = await configure("docker-api");

  const pick = within(form).getByLabelText("Bypass alias") as HTMLSelectElement;
  expect(pick.value).toBe("GONE");
  expect([...pick.options].map((o) => o.textContent)).toContain("GONE (not on offer)");
});

it("falls back to free text with the note when the choices can't be read", async () => {
  forms["docker-api"] = aliasForm({ choicesNote: "pfsense-api can't offer choices (not configured); type the value." });
  await boot();
  const form = await configure("docker-api");

  const typed = within(form).getByLabelText("Bypass alias") as HTMLInputElement;
  expect(typed.tagName).toBe("INPUT");
  expect(within(form).getByText("pfsense-api can't offer choices (not configured); type the value.")).toBeTruthy();
  fireEvent.change(typed, { target: { value: "VPN_BYPASS" } });
  await save(form);

  expect(saves).toEqual([{ owner: "docker-api", values: { bypass_alias: "VPN_BYPASS" } }]);
});

it("runs a Component's test Action from its button and shows the result inline", async () => {
  forms["ntfy-api"] = { ...ntfyForm(), testAction: { method: "SendTest", label: "Send test" } } as ReturnType<typeof ntfyForm>;
  await boot();
  const form = await configure("ntfy-api");

  fireEvent.click(within(form).getByRole("button", { name: "Send test" }));
  await settle();

  expect(testRuns).toEqual([{ owner: "ntfy-api" }]);
  expect(within(form).getByRole("status", { name: "Send test result" }).textContent).toMatch(/Send test: done/);

  testAnswer = () => Response.json({ code: "unavailable", message: "ntfy answered 401: unauthorized" }, { status: 503 });
  fireEvent.click(within(form).getByRole("button", { name: "Send test" }));
  await settle();

  expect(within(form).getByRole("status", { name: "Send test result" }).textContent).toMatch(/Send test failed: ntfy answered 401: unauthorized/);
});

it("shows no test button for a Component without a test Action", async () => {
  await boot();

  const form = await configure("docker-api");

  expect(within(form).queryByRole("button", { name: /test/i })).toBeNull();
});
