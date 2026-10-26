import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it } from "vitest";
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from "react-router";
import { ActionStrip, Button, DataTable, Facts, Notice, SearchField, Sheet, StatusList, Tabs, useQueryState, type Column } from "./index";
import { stubAnimations, unstubAnimations } from "./test-animations";

afterEach(() => {
  cleanup();
  unstubAnimations();
});

/** A search box kept in ?q=, with the URL shown and a way to leave the Page and come Back. */
function Explorer() {
  const [q, setQ] = useQueryState("q", "");
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <input aria-label="Search" value={q} onChange={(e) => setQ(e.target.value)} />
      <output aria-label="URL">{location.pathname + location.search}</output>
      <button onClick={() => navigate("/checks/nas")}>Open nas</button>
    </>
  );
}

function routed(url: string) {
  function Detail() {
    const navigate = useNavigate();
    return <button onClick={() => navigate(-1)}>Back</button>;
  }
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/checks" element={<Explorer />} />
        <Route path="/checks/nas" element={<Detail />} />
      </Routes>
    </MemoryRouter>,
  );
}

const url = () => screen.getByRole("status", { name: "URL" }).textContent;

it("useQueryState starts from its URL search param, as after a reload or from a link", () => {
  routed("/checks?q=nas&kind=http");
  expect((screen.getByRole("textbox", { name: "Search" }) as HTMLInputElement).value).toBe("nas");
});

it("useQueryState writes its param, keeps the others, and drops it at the default", () => {
  routed("/checks?kind=http");
  const box = screen.getByRole("textbox", { name: "Search" });

  fireEvent.change(box, { target: { value: "nas" } });
  expect(url()).toBe("/checks?kind=http&q=nas");

  fireEvent.change(box, { target: { value: "" } });
  expect(url()).toBe("/checks?kind=http");
});

it("useQueryState's value survives leaving the Page and coming Back", () => {
  routed("/checks");
  fireEvent.change(screen.getByRole("textbox", { name: "Search" }), { target: { value: "dns" } });
  fireEvent.click(screen.getByRole("button", { name: "Open nas" }));
  fireEvent.click(screen.getByRole("button", { name: "Back" }));

  expect((screen.getByRole("textbox", { name: "Search" }) as HTMLInputElement).value).toBe("dns");
  expect(url()).toBe("/checks?q=dns");
});

it("Tabs is a tablist whose selected tab carries its count, and a click picks another", () => {
  const picked: string[] = [];
  render(
    <Tabs
      label="Kind"
      value="all"
      options={[
        { id: "all", label: "All", count: 12 },
        { id: "http", label: "HTTP", count: 7 },
        { id: "dns", label: "DNS" },
      ]}
      onChange={(v) => picked.push(v)}
    />,
  );

  const list = screen.getByRole("tablist", { name: "Kind" });
  const tabs = within(list).getAllByRole("tab");
  expect(tabs.map((t) => t.getAttribute("aria-selected"))).toEqual(["true", "false", "false"]);
  expect(tabs[0].textContent).toContain("12");
  expect(screen.queryByRole("radiogroup")).toBeNull();

  fireEvent.click(screen.getByRole("tab", { name: /HTTP/ }));
  expect(picked).toEqual(["http"]);
});

it("Tabs' arrow keys move the focus along and switch, wrapping at the ends", () => {
  const picked: string[] = [];
  const options = [
    { id: "a", label: "Operators" },
    { id: "b", label: "Core" },
  ];
  render(<Tabs label="Actor" value="a" options={options} onChange={(v) => picked.push(v)} />);

  const [first, second] = screen.getAllByRole("tab");
  expect([first.tabIndex, second.tabIndex]).toEqual([0, -1]);
  fireEvent.keyDown(first, { key: "ArrowRight" });
  expect(document.activeElement).toBe(second);
  fireEvent.keyDown(first, { key: "ArrowLeft" });
  expect(picked).toEqual(["b", "b"]);
});

it("StatusList names each item's status and shows its title, meta, aside and body", () => {
  render(
    <StatusList
      items={[
        { tone: "down", title: "nas.lan", meta: "timed out", aside: "2m", body: <pre>GET /health</pre> },
        { tone: "up", title: "router" },
      ]}
    />,
  );

  const items = within(screen.getByRole("list")).getAllByRole("listitem");
  expect(items).toHaveLength(2);
  const first = within(items[0]);
  expect(first.getByRole("img", { name: "down" })).toBeTruthy();
  expect(first.getByText("nas.lan")).toBeTruthy();
  expect(first.getByText("timed out")).toBeTruthy();
  expect(first.getByText("2m")).toBeTruthy();
  expect(first.getByText("GET /health").tagName).toBe("PRE");
  expect(within(items[1]).getByRole("img", { name: "up" })).toBeTruthy();
});

it("StatusList timeline is an ordered list with the connecting rail; a plain one has none", () => {
  const items = [{ tone: "up" as const, title: "Hop 1" }, { tone: "down" as const, title: "Hop 2" }];
  const { unmount } = render(<StatusList items={items} timeline />);
  expect(screen.getByRole("list").tagName).toBe("OL");
  expect(screen.getByRole("list").hasAttribute("data-timeline")).toBe(true);
  unmount();

  render(<StatusList items={items} />);
  expect(screen.getByRole("list").tagName).toBe("UL");
  expect(screen.getByRole("list").hasAttribute("data-timeline")).toBe(false);
});

it("Facts is a description list pairing each label with its value", () => {
  render(
    <Facts
      items={[
        { label: "Host", value: <a href="/n">nas</a> },
        { label: "IP", value: "10.0.0.2", mono: true },
      ]}
    />,
  );

  const terms = screen.getAllByRole("term");
  const defs = screen.getAllByRole("definition");
  expect(terms.map((t) => t.textContent)).toEqual(["Host", "IP"]);
  expect(within(defs[0]).getByRole("link", { name: "nas" })).toBeTruthy();
  expect(defs[1].textContent).toBe("10.0.0.2");
  expect(defs[1].closest("dl")).toBe(terms[0].closest("dl"));
  expect(defs[1].hasAttribute("data-mono")).toBe(true);
  expect(defs[0].hasAttribute("data-mono")).toBe(false);
});

interface Check {
  name: string;
  target: string;
  latency: number;
}

const checks: Check[] = [
  { name: "nas", target: "10.0.0.2", latency: 40 },
  { name: "router", target: "10.0.0.1", latency: 3 },
  { name: "dns", target: "10.0.0.53", latency: 12 },
];

const columns: Column<Check>[] = [
  { id: "name", header: "Name", cell: (c) => c.name, rowHeader: true, sortable: true, sortValue: (c) => c.name },
  { id: "target", header: "Target", cell: (c) => c.target, mono: true, hideBelow: "sm" },
  { id: "latency", header: "Latency", cell: (c) => `${c.latency} ms`, align: "right", sortable: true, sortValue: (c) => c.latency },
];

/** Each body row's cells' text, in order. */
const bodyRows = () =>
  screen
    .getAllByRole("row")
    .slice(1)
    .map((r) => Array.from(r.children, (c) => c.textContent));

it("DataTable draws a header row and one row per item, its rowHeader column as the row's header", () => {
  render(<DataTable rows={checks} rowKey={(c) => c.name} columns={columns} />);

  expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Target", "Latency"]);
  expect(bodyRows()).toEqual([
    ["nas", "10.0.0.2", "40 ms"],
    ["router", "10.0.0.1", "3 ms"],
    ["dns", "10.0.0.53", "12 ms"],
  ]);
  expect(screen.getByRole("rowheader", { name: "router" }).tagName).toBe("TH");
});

it("DataTable rows hover in the primary tint, the sticky first cell staying opaque over it", () => {
  render(<DataTable rows={checks} rowKey={(c) => c.name} columns={columns} />);

  // Spelled at run time, so this file generates no classes of its own.
  const tint = ["primary", "tint"].join("-");
  const row = screen.getByRole("rowheader", { name: "nas" }).closest("tr")!;
  expect(row.className.split(" ")).toContain(`hover:bg-${tint}`);
  const sticky = screen.getByRole("rowheader", { name: "nas" }).className.split(" ");
  expect(sticky).toContain("bg-card");
  expect(sticky.some((c) => c.startsWith("group-hover:") && c.endsWith(tint))).toBe(true);
  expect(sticky.some((c) => c.startsWith("group-hover:bg-muted"))).toBe(false);
});

it("DataTable with no rows keeps its header row and shows empty", () => {
  render(<DataTable rows={[]} rowKey={(c: Check) => c.name} columns={columns} empty="No Checks yet" />);

  expect(screen.getAllByRole("columnheader")).toHaveLength(3);
  expect(screen.getByText("No Checks yet")).toBeTruthy();
});

it("DataTable while loading draws skeleton rows under its header, not empty", () => {
  render(<DataTable rows={[]} rowKey={(c: Check) => c.name} columns={columns} empty="No Checks yet" loading />);

  expect(screen.getAllByRole("columnheader")).toHaveLength(3);
  expect(screen.getAllByRole("status", { name: "Loading" }).length).toBeGreaterThan(0);
  expect(screen.queryByText("No Checks yet")).toBeNull();
});

it("DataTable rows open on a click, or on Enter or Space once focused", () => {
  const opened: string[] = [];
  render(<DataTable rows={checks} rowKey={(c) => c.name} columns={columns} onRowClick={(c) => opened.push(c.name)} />);
  const row = (name: string) => screen.getByRole("rowheader", { name }).closest("tr")!;

  fireEvent.click(screen.getByText("40 ms"));
  row("router").focus();
  expect(document.activeElement).toBe(row("router"));
  fireEvent.keyDown(row("router"), { key: "Enter" });
  fireEvent.keyDown(row("dns"), { key: " " });
  fireEvent.keyDown(row("dns"), { key: "a" });

  expect(opened).toEqual(["nas", "router", "dns"]);
});

it("DataTable actions put each row's Actions in its ⋯ Menu, without opening the row", () => {
  const done: string[] = [];
  render(
    <DataTable
      rows={checks}
      rowKey={(c) => c.name}
      columns={columns}
      onRowClick={(c) => done.push(`open ${c.name}`)}
      actions={(c) => [{ label: "Run now", onSelect: () => done.push(`run ${c.name}`) }]}
    />,
  );

  expect(screen.getAllByRole("columnheader")).toHaveLength(4);
  fireEvent.click(screen.getByRole("button", { name: "Actions for router" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "Run now" }));

  expect(done).toEqual(["run router"]);
});

it("DataTable leaves Enter on a link in the row header to the link, not the row", () => {
  const opened: string[] = [];
  const linked: Column<Check>[] = [{ ...columns[0], cell: (c) => <a href={`/checks/${c.name}`}>{c.name}</a> }, columns[2]];
  render(<DataTable rows={checks} rowKey={(c) => c.name} columns={linked} onRowClick={(c) => opened.push(c.name)} />);

  const link = within(screen.getByRole("rowheader", { name: "nas" })).getByRole("link", { name: "nas" });
  fireEvent.keyDown(link, { key: "Enter" });

  expect(opened).toEqual([]);
});

const header = (name: string) => screen.getByRole("columnheader", { name: new RegExp(name) });
const names = () => bodyRows().map((r) => r[0]);

it("DataTable sorts by a sortable column's header, toggling ascending and descending, and marks it with aria-sort", () => {
  render(<DataTable rows={checks} rowKey={(c) => c.name} columns={columns} />);

  expect(within(header("Target")).queryByRole("button")).toBeNull();
  expect(header("Latency").getAttribute("aria-sort")).toBeNull();

  fireEvent.click(within(header("Latency")).getByRole("button"));
  expect(header("Latency").getAttribute("aria-sort")).toBe("ascending");
  expect(names()).toEqual(["router", "dns", "nas"]);

  fireEvent.click(within(header("Latency")).getByRole("button"));
  expect(header("Latency").getAttribute("aria-sort")).toBe("descending");
  expect(names()).toEqual(["nas", "dns", "router"]);

  // A third click goes back to ascending: once sorted, never unsorted.
  fireEvent.click(within(header("Latency")).getByRole("button"));
  expect(header("Latency").getAttribute("aria-sort")).toBe("ascending");
  expect(names()).toEqual(["router", "dns", "nas"]);

  fireEvent.click(within(header("Name")).getByRole("button"));
  expect(header("Name").getAttribute("aria-sort")).toBe("ascending");
  expect(header("Latency").getAttribute("aria-sort")).toBeNull();
  expect(names()).toEqual(["dns", "nas", "router"]);
});

function UrlSorted({ url, defaultSort }: { url: string; defaultSort?: string }) {
  function Sorted() {
    const location = useLocation();
    return (
      <>
        <DataTable rows={checks} rowKey={(c) => c.name} columns={columns} sortKey="sort" defaultSort={defaultSort} />
        <output aria-label="URL">{location.search}</output>
      </>
    );
  }
  return (
    <MemoryRouter initialEntries={[url]}>
      <Sorted />
    </MemoryRouter>
  );
}

it("DataTable with sortKey keeps its sort in the URL, so a reload or link starts sorted", () => {
  render(<UrlSorted url="/checks?sort=-latency" />);

  expect(header("Latency").getAttribute("aria-sort")).toBe("descending");
  expect(names()).toEqual(["nas", "dns", "router"]);

  fireEvent.click(within(header("Latency")).getByRole("button"));
  expect(url()).toBe("?sort=latency");
  expect(names()).toEqual(["router", "dns", "nas"]);

  fireEvent.click(within(header("Name")).getByRole("button"));
  expect(url()).toBe("?sort=name");
  expect(names()).toEqual(["dns", "nas", "router"]);
});

it("DataTable with defaultSort starts sorted by it, its header marked, until a header is clicked", () => {
  render(<DataTable rows={checks} rowKey={(c) => c.name} columns={columns} defaultSort="-latency" />);

  expect(header("Latency").getAttribute("aria-sort")).toBe("descending");
  expect(names()).toEqual(["nas", "dns", "router"]);

  fireEvent.click(within(header("Latency")).getByRole("button"));
  expect(header("Latency").getAttribute("aria-sort")).toBe("ascending");
  expect(names()).toEqual(["router", "dns", "nas"]);

  fireEvent.click(within(header("Name")).getByRole("button"));
  expect(header("Name").getAttribute("aria-sort")).toBe("ascending");
  expect(header("Latency").getAttribute("aria-sort")).toBeNull();
});

it("DataTable with sortKey and defaultSort sorts by the default while the URL has no sort, the URL staying clean", () => {
  render(<UrlSorted url="/checks" defaultSort="name" />);

  expect(header("Name").getAttribute("aria-sort")).toBe("ascending");
  expect(names()).toEqual(["dns", "nas", "router"]);
  expect(url()).toBe("");

  fireEvent.click(within(header("Name")).getByRole("button"));
  expect(header("Name").getAttribute("aria-sort")).toBe("descending");
  expect(url()).toBe("?sort=-name");
  expect(names()).toEqual(["router", "nas", "dns"]);

  fireEvent.click(within(header("Name")).getByRole("button"));
  expect(header("Name").getAttribute("aria-sort")).toBe("ascending");
  expect(names()).toEqual(["dns", "nas", "router"]);
});

it("DataTable with sortKey follows the URL's sort over its defaultSort", () => {
  render(<UrlSorted url="/checks?sort=latency" defaultSort="name" />);

  expect(header("Latency").getAttribute("aria-sort")).toBe("ascending");
  expect(header("Name").getAttribute("aria-sort")).toBeNull();
  expect(names()).toEqual(["router", "dns", "nas"]);
});

it("ActionStrip announces its summary, holds the controls beside it, and puts the Notice under both", () => {
  render(
    <ActionStrip summary="Proxmox is restarting it" notice={<Notice tone="down">Proxmox refused the restart</Notice>}>
      <Button size="sm">Start</Button>
      <Button size="sm" variant="danger">
        Stop
      </Button>
    </ActionStrip>,
  );

  const summary = screen.getByRole("status");
  expect(summary.textContent).toBe("Proxmox is restarting it");
  const stop = screen.getByRole("button", { name: "Stop" });
  expect(summary.contains(stop)).toBe(false);
  expect(stop.parentElement).toBe(screen.getByRole("button", { name: "Start" }).parentElement);
  const notice = screen.getByRole("alert");
  // The Notice comes after the summary and the controls.
  expect(summary.compareDocumentPosition(notice) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(stop.compareDocumentPosition(notice) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(notice.contains(stop)).toBe(false);
});

it("ActionStrip without a notice draws the summary and the controls only", () => {
  render(
    <ActionStrip summary="running, up 3 d">
      <Button size="sm">Restart</Button>
    </ActionStrip>,
  );

  expect(screen.getByRole("status").textContent).toBe("running, up 3 d");
  expect(screen.queryByRole("alert")).toBeNull();
});

/** A Check's strip as a Plugin might write one: stateful, so it shows that only the open row's is mounted. */
function CheckStrip({ check, close }: { check: Check; close: () => void }) {
  const [runs, setRuns] = useState(0);
  return (
    <ActionStrip summary={`${check.latency} ms · run ${runs} times`}>
      <Button size="sm" onClick={() => setRuns(runs + 1)}>
        Run now
      </Button>
      <Button size="sm" onClick={close}>
        Done
      </Button>
    </ActionStrip>
  );
}

const stripped = {
  rowKey: (c: Check) => c.target,
  columns,
  rowLabel: (c: Check) => c.name,
  strip: (c: Check, close: () => void) => <CheckStrip check={c} close={close} />,
};

const arrow = (name: string) => screen.getByRole("button", { name: `Actions for ${name}` });
const strip = (name: string) => screen.queryByRole("group", { name: `Actions for ${name}` });
const rowOf = (name: string) => screen.getByRole("rowheader", { name }).closest("tr")!;

it("DataTable strip opens a row's strip right under it from the arrow at its end, a disclosure named by rowLabel", () => {
  render(<DataTable rows={checks} {...stripped} />);

  expect(screen.getAllByRole("columnheader")).toHaveLength(4);
  const button = arrow("router");
  expect(button.getAttribute("aria-expanded")).toBe("false");
  expect(strip("router")).toBeNull();

  button.focus();
  fireEvent.click(button);
  expect(button.getAttribute("aria-expanded")).toBe("true");
  const group = strip("router")!;
  expect(button.getAttribute("aria-controls")).toBe(group.id);
  expect(group.closest("tr")!.previousElementSibling).toBe(rowOf("router"));
  expect(within(group).getByRole("status").textContent).toBe("3 ms · run 0 times");
  expect(document.activeElement).toBe(button);

  fireEvent.click(button);
  expect(button.getAttribute("aria-expanded")).toBe("false");
  expect(strip("router")).toBeNull();
  expect(document.activeElement).toBe(button);
});

it("DataTable strip mounts only the open row's strip, and its close closes it", () => {
  render(<DataTable rows={checks} {...stripped} />);

  expect(screen.queryAllByRole("status")).toHaveLength(0);
  fireEvent.click(arrow("nas"));
  expect(screen.getAllByRole("status")).toHaveLength(1);

  fireEvent.click(within(strip("nas")!).getByRole("button", { name: "Done" }));
  expect(strip("nas")).toBeNull();
  expect(arrow("nas").getAttribute("aria-expanded")).toBe("false");
});

it("DataTable strip gives a row whose strip is null no arrow", () => {
  render(<DataTable rows={checks} {...stripped} strip={(c: Check, close: () => void) => (c.name === "dns" ? null : <CheckStrip check={c} close={close} />)} />);

  expect(screen.queryByRole("button", { name: "Actions for dns" })).toBeNull();
  expect(arrow("nas")).toBeTruthy();
  // The row still has its end cell, so the columns line up.
  expect(rowOf("dns").children).toHaveLength(4);
});

it("DataTable strip keeps one strip open: opening another slides the first shut as the new one opens", async () => {
  const exits = stubAnimations();
  render(<DataTable rows={checks} {...stripped} />);

  fireEvent.click(arrow("nas"));
  fireEvent.click(arrow("router"));

  expect(arrow("nas").getAttribute("aria-expanded")).toBe("false");
  expect(arrow("router").getAttribute("aria-expanded")).toBe("true");
  // The first is still there, closed and inert, until its slide ends.
  const leaving = screen.getByText("40 ms · run 0 times").closest("tr")!;
  expect(leaving.getAttribute("data-state")).toBe("closed");
  expect(leaving.hasAttribute("inert")).toBe(true);
  expect(strip("router")!.closest("tr")!.getAttribute("data-state")).toBe("open");

  await act(async () => exits.at(-1)!.finish());
  expect(screen.queryByText("40 ms · run 0 times")).toBeNull();
  expect(strip("router")).toBeTruthy();
});

it("DataTable strip slides its height only where motion is allowed, and goes at once with no animation running", () => {
  render(<DataTable rows={checks} {...stripped} />);

  fireEvent.click(arrow("nas"));
  const slide = [...strip("nas")!.closest("td")!.querySelectorAll("div")].find((d) => d.className.includes("grid-rows"))!;
  const transitions = slide.className.split(" ").filter((c) => c.includes("transition"));
  expect(transitions).not.toHaveLength(0);
  expect(transitions.every((c) => c.startsWith("motion-safe:"))).toBe(true);

  // Reduced motion: nothing animates, so Presence lets the strip go at once.
  fireEvent.click(arrow("nas"));
  expect(strip("nas")).toBeNull();
});

it("DataTable strip stays open, with its state, through a re-sort, moving with its row, and through a refetch", () => {
  const { rerender } = render(<DataTable rows={checks} {...stripped} />);
  fireEvent.click(arrow("router"));
  fireEvent.click(within(strip("router")!).getByRole("button", { name: "Run now" }));

  fireEvent.click(within(header("Latency")).getByRole("button"));
  expect(names()[0]).toBe("router");
  expect(strip("router")!.closest("tr")!.previousElementSibling).toBe(rowOf("router"));

  rerender(<DataTable rows={checks.map((c) => ({ ...c }))} {...stripped} />);
  expect(within(strip("router")!).getByRole("status").textContent).toBe("3 ms · run 1 times");
  expect(arrow("router").getAttribute("aria-expanded")).toBe("true");
});

it("DataTable strip forgets the open row once a filter hides it, so clearing the filter doesn't reopen it", () => {
  const { rerender } = render(<DataTable rows={checks} {...stripped} />);
  fireEvent.click(arrow("router"));

  rerender(<DataTable rows={checks.filter((c) => c.name !== "router")} {...stripped} />);
  expect(strip("router")).toBeNull();

  rerender(<DataTable rows={checks} {...stripped} />);
  expect(strip("router")).toBeNull();
  expect(arrow("router").getAttribute("aria-expanded")).toBe("false");
});

it("DataTable strip's arrow and strip keep their clicks from opening the row", () => {
  const opened: string[] = [];
  render(<DataTable rows={checks} {...stripped} onRowClick={(c) => opened.push(c.name)} />);

  fireEvent.click(arrow("nas"));
  fireEvent.click(within(strip("nas")!).getByRole("button", { name: "Run now" }));
  fireEvent.keyDown(arrow("nas"), { key: "Enter" });
  expect(opened).toEqual([]);

  fireEvent.click(screen.getByText("12 ms"));
  expect(opened).toEqual(["dns"]);
});

it("DataTable strip closes on Escape in the strip or on the open arrow, with the focus on the arrow", () => {
  render(<DataTable rows={checks} {...stripped} />);

  fireEvent.click(arrow("nas"));
  const run = within(strip("nas")!).getByRole("button", { name: "Run now" });
  run.focus();
  fireEvent.keyDown(run, { key: "Escape" });
  expect(strip("nas")).toBeNull();
  expect(document.activeElement).toBe(arrow("nas"));

  fireEvent.click(arrow("nas"));
  fireEvent.keyDown(arrow("nas"), { key: "Escape" });
  expect(strip("nas")).toBeNull();
  expect(document.activeElement).toBe(arrow("nas"));
});

it("DataTable strip leaves Escape to a field that handles it, and to an overlay opened from the strip", () => {
  function Searching({ check }: { check: Check }) {
    const [q, setQ] = useState("nas");
    const [sheet, setSheet] = useState(false);
    return (
      <ActionStrip summary={check.name}>
        <SearchField label="Filter" value={q} onChange={setQ} />
        <Button size="sm" onClick={() => setSheet(true)}>
          Edit
        </Button>
        {sheet && (
          <Sheet title="Edit" onClose={() => setSheet(false)}>
            <input aria-label="Name" />
          </Sheet>
        )}
      </ActionStrip>
    );
  }
  render(<DataTable rows={checks} {...stripped} strip={(c: Check) => <Searching check={c} />} />);
  fireEvent.click(arrow("nas"));

  const filter = screen.getByRole("searchbox", { name: "Filter" }) as HTMLInputElement;
  fireEvent.keyDown(filter, { key: "Escape" });
  expect(filter.value).toBe("");
  expect(strip("nas")).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  fireEvent.keyDown(screen.getByRole("textbox", { name: "Name" }), { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(strip("nas")).toBeTruthy();

  // With nothing inside left to handle it, the next Escape closes the strip.
  fireEvent.keyDown(filter, { key: "Escape" });
  expect(strip("nas")).toBeNull();
});

it("DataTable strip moves the focus to the row's arrow when the focused control inside goes away or turns disabled", async () => {
  function Renaming() {
    const [renaming, setRenaming] = useState(false);
    const [stopped, setStopped] = useState(false);
    return (
      <ActionStrip summary={renaming ? "renaming" : "nas"}>
        {!renaming && (
          <Button size="sm" onClick={() => setRenaming(true)}>
            Rename
          </Button>
        )}
        <Button size="sm" disabled={stopped} onClick={() => setStopped(true)}>
          Stop
        </Button>
      </ActionStrip>
    );
  }
  render(<DataTable rows={checks} {...stripped} strip={() => <Renaming />} />);
  fireEvent.click(arrow("nas"));

  const rename = screen.getByRole("button", { name: "Rename" });
  rename.focus();
  fireEvent.click(rename);
  await waitFor(() => expect(document.activeElement).toBe(arrow("nas")));

  const stop = screen.getByRole("button", { name: "Stop" });
  stop.focus();
  fireEvent.click(stop);
  await waitFor(() => expect(document.activeElement).toBe(arrow("nas")));
  expect(strip("nas")).toBeTruthy();
});

it("DataTable strip's busy row shows a maint Pill by its arrow, which can't open the strip but keeps the focus", () => {
  const busy = (c: Check) => (c.name === "nas" ? "restarting" : undefined);
  const { rerender } = render(<DataTable rows={checks} {...stripped} busy={busy} />);

  const button = arrow("nas");
  expect(button.getAttribute("aria-disabled")).toBe("true");
  expect((button as HTMLButtonElement).disabled).toBe(false);
  expect(document.getElementById(button.getAttribute("aria-describedby")!)!.textContent).toBe("restarting");
  fireEvent.click(button);
  expect(strip("nas")).toBeNull();
  expect(arrow("router").hasAttribute("aria-disabled")).toBe(false);

  // Busy once open: the arrow still closes it, then holds until the task ends.
  rerender(<DataTable rows={checks} {...stripped} />);
  fireEvent.click(arrow("nas"));
  rerender(<DataTable rows={checks} {...stripped} busy={busy} />);
  expect(arrow("nas").hasAttribute("aria-disabled")).toBe(false);
  arrow("nas").focus();
  fireEvent.click(arrow("nas"));
  expect(strip("nas")).toBeNull();
  expect(arrow("nas").getAttribute("aria-disabled")).toBe("true");
  expect(document.activeElement).toBe(arrow("nas"));
});

it("DataTable strip keeps the arrow and the strip's content in place while the table scrolls sideways", () => {
  render(<DataTable rows={checks} {...stripped} />);
  fireEvent.click(arrow("nas"));

  const sticky = (el: Element | null | undefined) => !!el?.className.split(" ").includes("sticky");
  expect(sticky(arrow("nas").closest("td"))).toBe(true);
  expect(sticky(screen.getAllByRole("columnheader").at(-1))).toBe(true);
  expect(sticky(strip("nas")!.closest("td")!.firstElementChild)).toBe(true);
});

it("DataTable needs rowLabel with strip", () => {
  // @ts-expect-error rowLabel names the arrow and the strip, so a strip needs one.
  const missing = <DataTable rows={checks} rowKey={(c) => c.name} columns={columns} strip={() => "x"} />;
  expect(missing).toBeTruthy();
});
