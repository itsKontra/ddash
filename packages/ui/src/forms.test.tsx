import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { Button, Checkbox, Field, FormActions, SearchField, Segmented, Select, Switch, TextArea, TextInput } from "./index";

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
