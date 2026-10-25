import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Button, Checkbox, Field, FormActions, IconPicker, SearchField, Segmented, Select, Switch, TextArea, TextInput } from "./index";

afterEach(cleanup);

it("Field names its control and points it at the hint", () => {
  render(
    <Field label="Hostname" hint="Without the scheme">
      <TextInput defaultValue="nas.lan" />
    </Field>,
  );

  const input = screen.getByRole("textbox", { name: "Hostname" });
  const hint = screen.getByText("Without the scheme");
  expect(input.getAttribute("aria-invalid")).toBeNull();
  expect(hint.id).not.toBe("");
  expect(input.getAttribute("aria-describedby")).toBe(hint.id);
});

it("Field with an error marks its control invalid and describes it by the error instead of the hint", () => {
  render(
    <Field label="Notes" hint="Shown on the card" error="Too long">
      <TextArea defaultValue="…" />
    </Field>,
  );

  const area = screen.getByRole("textbox", { name: "Notes" });
  expect(area.tagName).toBe("TEXTAREA");
  expect(area.getAttribute("aria-invalid")).toBe("true");
  expect(area.getAttribute("aria-describedby")).toBe(screen.getByText("Too long").id);
  expect(screen.queryByText("Shown on the card")).toBeNull();
});

it("Select inside a Field is named by the Field and reports the picked option", () => {
  const picked: string[] = [];
  render(
    <Field label="Kind" hint="How the Check probes">
      <Select value="http" onChange={(e) => picked.push(e.target.value)}>
        <option value="http">HTTP</option>
        <option value="tcp">TCP</option>
      </Select>
    </Field>,
  );

  const select = screen.getByRole("combobox", { name: "Kind" });
  expect(select.getAttribute("aria-describedby")).toBe(screen.getByText("How the Check probes").id);
  fireEvent.change(select, { target: { value: "tcp" } });
  expect(picked).toEqual(["tcp"]);
});

it("Select with a label and no Field is the compact toolbar select, named by its label", () => {
  render(
    <Select label="Outcome" defaultValue="any">
      <option value="any">Any outcome</option>
      <option value="failed">Failed</option>
    </Select>,
  );

  expect(screen.getByRole("combobox", { name: "Outcome" })).toBeTruthy();
  expect(screen.queryByText("Outcome")).toBeNull();
});

it("Checkbox is a native checkbox named by its label that reports each toggle", () => {
  const changes: boolean[] = [];
  render(<Checkbox label="Blocked only" checked={false} onChange={(e) => changes.push(e.target.checked)} />);

  const box = screen.getByRole("checkbox", { name: "Blocked only" });
  expect((box as HTMLInputElement).checked).toBe(false);
  fireEvent.click(box);
  expect(changes).toEqual([true]);
});

it("SearchField is a named search box that reports each edit", () => {
  const typed: string[] = [];
  render(<SearchField label="Search Checks" placeholder="Name or target" value="" onChange={(v) => typed.push(v)} />);

  const box = screen.getByRole("searchbox", { name: "Search Checks" });
  expect(box.getAttribute("placeholder")).toBe("Name or target");
  fireEvent.change(box, { target: { value: "nas" } });
  expect(typed).toEqual(["nas"]);
});

it("SearchField clears on Escape", () => {
  const typed: string[] = [];
  render(<SearchField label="Search Widgets" value="clock" onChange={(v) => typed.push(v)} />);

  fireEvent.keyDown(screen.getByRole("searchbox", { name: "Search Widgets" }), { key: "Escape" });
  expect(typed).toEqual([""]);
});

it("FormActions holds the form's buttons in order, after its status line", () => {
  render(
    <FormActions status="3 Widgets on the Overview">
      <Button variant="outline">Cancel</Button>
      <Button type="submit">Save</Button>
    </FormActions>,
  );

  const status = screen.getByText("3 Widgets on the Overview");
  const [cancel, save] = screen.getAllByRole("button");
  expect([cancel.textContent, save.textContent]).toEqual(["Cancel", "Save"]);
  expect(status.compareDocumentPosition(cancel) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});

it("Segmented is a named radio group with the picked option checked, picking on click", () => {
  const picked: string[] = [];
  render(<Segmented label="Size" value="M" options={["S", "M", "L"]} onChange={(v) => picked.push(v)} />);

  screen.getByRole("radiogroup", { name: "Size" });
  expect(screen.getByRole("radio", { name: "M" }).getAttribute("aria-checked")).toBe("true");
  expect(screen.getByRole("radio", { name: "S" }).getAttribute("aria-checked")).toBe("false");
  fireEvent.click(screen.getByRole("radio", { name: "L" }));
  expect(picked).toEqual(["L"]);
});

it("Segmented in a Field is named by the Field's label and described by its hint, leaving no label pointing nowhere", () => {
  const { container } = render(
    <Field label="Maintenance length" hint="Ends on its own">
      <Segmented label="For" value="1 hour" options={["1 hour", "1 day"]} onChange={() => {}} />
    </Field>,
  );

  const group = screen.getByRole("radiogroup", { name: "Maintenance length" });
  expect(group.getAttribute("aria-describedby")).toBe(screen.getByText("Ends on its own").id);
  for (const label of container.querySelectorAll("label[for]")) {
    const target = document.getElementById((label as HTMLLabelElement).htmlFor);
    expect(target?.matches("input, select, textarea, button"), `label "${label.textContent}"`).toBe(true);
  }
});

it("Segmented takes one tab stop and picks with the arrow keys, wrapping at the ends", () => {
  const picked: string[] = [];
  const { rerender } = render(<Segmented label="Window" value="24h" options={["24h", "7d", "30d"]} onChange={(v) => picked.push(v)} />);

  expect(screen.getAllByRole("radio").map((r) => r.tabIndex)).toEqual([0, -1, -1]);

  fireEvent.keyDown(screen.getByRole("radio", { name: "24h" }), { key: "ArrowLeft" });
  expect(picked).toEqual(["30d"]);
  expect(document.activeElement).toBe(screen.getByRole("radio", { name: "30d" }));

  rerender(<Segmented label="Window" value="30d" options={["24h", "7d", "30d"]} onChange={(v) => picked.push(v)} />);
  expect(screen.getAllByRole("radio").map((r) => r.tabIndex)).toEqual([-1, -1, 0]);
  fireEvent.keyDown(screen.getByRole("radio", { name: "30d" }), { key: "ArrowRight" });
  fireEvent.keyDown(screen.getByRole("radio", { name: "24h" }), { key: "ArrowDown" });
  expect(picked).toEqual(["30d", "24h", "7d"]);
});

it("a control outside a Field takes only its own attributes", () => {
  render(<TextInput aria-label="Filter" placeholder="Type to filter" required />);

  const input = screen.getByRole("textbox", { name: "Filter" });
  expect(input.getAttribute("placeholder")).toBe("Type to filter");
  expect(input.hasAttribute("required")).toBe(true);
  expect(input.hasAttribute("aria-describedby")).toBe(false);
  expect(input.hasAttribute("aria-invalid")).toBe(false);
});

it("Switch is a named switch reporting its state and asking for the other", () => {
  const changes: boolean[] = [];
  render(<Switch checked={false} label="Direct TV" onChange={(v) => changes.push(v)} />);

  const s = screen.getByRole("switch", { name: "Direct TV" });
  expect(s.getAttribute("aria-checked")).toBe("false");
  s.click();
  expect(changes).toEqual([true]);
});

it("Switch inside a Field is described by the Field's hint", () => {
  render(
    <Field label="Markdown" hint="Formats the message body">
      <Switch label="Markdown" checked={false} onChange={() => {}} />
    </Field>,
  );

  const s = screen.getByRole("switch", { name: "Markdown" });
  expect(s.getAttribute("aria-describedby")).toBe(screen.getByText("Formats the message body").id);
  expect(screen.getByText("Markdown", { selector: "label" }).getAttribute("for")).toBe(s.id);
});

it("Switch is a named switch that reports the flipped value, and does nothing while disabled", () => {
  const changes: boolean[] = [];
  const { rerender } = render(<Switch label="Ad-blocking" checked onChange={(v) => changes.push(v)} />);

  const sw = screen.getByRole("switch", { name: "Ad-blocking" });
  expect(sw.getAttribute("aria-checked")).toBe("true");
  fireEvent.click(sw);
  expect(changes).toEqual([false]);

  rerender(<Switch label="Ad-blocking" checked={false} disabled onChange={(v) => changes.push(v)} />);
  fireEvent.click(screen.getByRole("switch", { name: "Ad-blocking" }));
  expect(changes).toEqual([false]);
});

describe("IconPicker", () => {
  // What GET /api/icons/search answers, by query.
  const found: Record<string, string[]> = {
    house: ["house", "house-cog", "house-heart", "house-plug", "house-plus", "house-wifi", "houses", "birdhouse", "lighthouse", "warehouse"],
  };
  let searches: string[];

  beforeEach(() => {
    searches = [];
    vi.stubGlobal("fetch", async (url: string) => {
      const u = new URL(url, "http://ddash");
      if (u.pathname === "/api/icons/search") {
        searches.push(u.searchParams.get("q")!);
        return Response.json(found[u.searchParams.get("q")!] ?? []);
      }
      return Response.json(Object.fromEntries(u.searchParams.get("names")!.split(",").map((n) => [n, [["path", { d: "M5 12h14" }]]])));
    });
  });

  afterEach(() => vi.unstubAllGlobals());

  const open = (name: string) => fireEvent.click(screen.getByRole("button", { name: new RegExp(`^${name}`) }));
  const search = () => screen.getByRole("combobox", { name: "Search icons" });
  const type = (q: string) => fireEvent.change(search(), { target: { value: q } });

  it("inside a Field is named by the Field and shows the picked icon", () => {
    render(
      <Field label="Icon" hint="Drawn on the card">
        <IconPicker label="Icon" value="server" onChange={() => {}} />
      </Field>,
    );

    const trigger = screen.getByRole("button", { name: /^Icon/ });
    expect(trigger.textContent).toContain("server");
    expect(trigger.getAttribute("aria-describedby")).toBe(screen.getByText("Drawn on the card").id);
  });

  it("searches the core once typing pauses, with the last query", async () => {
    render(<IconPicker label="Icon" value={null} onChange={() => {}} />);
    open("Icon");

    type("h");
    type("ho");
    type("house");
    expect(searches).toEqual([]);

    expect(await screen.findByRole("option", { name: "house-cog" })).toBeTruthy();
    expect(searches).toEqual(["house"]);
    expect(screen.getAllByRole("option").map((o) => o.getAttribute("aria-label"))).toEqual(found.house);
  });

  it("moves through the grid with the arrow keys, rows of eight, and picks with Enter", async () => {
    const picked: string[] = [];
    render(<IconPicker label="Icon" value={null} onChange={(n) => picked.push(n)} />);
    open("Icon");
    type("house");
    await screen.findByRole("option", { name: "house" });

    const active = () => document.getElementById(search().getAttribute("aria-activedescendant")!)?.getAttribute("aria-label");
    expect(active()).toBe("house");
    fireEvent.keyDown(search(), { key: "ArrowRight" });
    expect(active()).toBe("house-cog");
    fireEvent.keyDown(search(), { key: "ArrowDown" });
    expect(active()).toBe("warehouse");
    fireEvent.keyDown(search(), { key: "ArrowLeft" });
    fireEvent.keyDown(search(), { key: "ArrowUp" });
    expect(active()).toBe("house");
    fireEvent.keyDown(search(), { key: "ArrowLeft" });
    expect(active()).toBe("house");
    fireEvent.keyDown(search(), { key: "ArrowRight" });
    fireEvent.keyDown(search(), { key: "Enter" });

    expect(picked).toEqual(["house-cog"]);
    expect(screen.queryByRole("combobox")).toBeNull();
  });

  it("picks an icon on click", async () => {
    const picked: string[] = [];
    render(<IconPicker label="Icon" value="server" onChange={(n) => picked.push(n)} />);
    open("Icon");
    type("house");

    fireEvent.click(await screen.findByRole("option", { name: "warehouse" }));

    expect(picked).toEqual(["warehouse"]);
  });

  it("says so when nothing matches", async () => {
    render(<IconPicker label="Icon" value={null} onChange={() => {}} />);
    open("Icon");
    type("zzqx");

    expect(await screen.findByText(/No icons match/)).toBeTruthy();
    expect(screen.queryAllByRole("option")).toEqual([]);
  });

  it("closes on Escape without picking, and only the picker closes", async () => {
    const picked: string[] = [];
    const outer: string[] = [];
    render(
      <div onKeyDown={(e) => outer.push(e.key)}>
        <IconPicker label="Icon" value={null} onChange={(n) => picked.push(n)} />
      </div>,
    );
    open("Icon");

    fireEvent.keyDown(search(), { key: "Escape" });

    expect(screen.queryByRole("combobox")).toBeNull();
    expect(picked).toEqual([]);
    expect(outer).toEqual([]);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: /^Icon/ })));
  });
});
