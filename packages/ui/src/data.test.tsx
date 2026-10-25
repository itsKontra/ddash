import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from "react-router";
import { DataTable, Facts, StatusList, Tabs, useQueryState, type Column } from "./index";

afterEach(cleanup);

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

it("DataTable sorts by a sortable column's header, ascending then descending, and marks it with aria-sort", () => {
  render(<DataTable rows={checks} rowKey={(c) => c.name} columns={columns} />);
  const header = (name: string) => screen.getByRole("columnheader", { name: new RegExp(name) });
  const names = () => bodyRows().map((r) => r[0]);

  expect(within(header("Target")).queryByRole("button")).toBeNull();
  expect(header("Latency").getAttribute("aria-sort")).toBeNull();

  fireEvent.click(within(header("Latency")).getByRole("button"));
  expect(header("Latency").getAttribute("aria-sort")).toBe("ascending");
  expect(names()).toEqual(["router", "dns", "nas"]);

  fireEvent.click(within(header("Latency")).getByRole("button"));
  expect(header("Latency").getAttribute("aria-sort")).toBe("descending");
  expect(names()).toEqual(["nas", "dns", "router"]);

  fireEvent.click(within(header("Name")).getByRole("button"));
  expect(header("Name").getAttribute("aria-sort")).toBe("ascending");
  expect(header("Latency").getAttribute("aria-sort")).toBeNull();
  expect(names()).toEqual(["dns", "nas", "router"]);
});

it("DataTable with sortKey keeps its sort in the URL, so a reload or link starts sorted", () => {
  function Sorted() {
    const location = useLocation();
    return (
      <>
        <DataTable rows={checks} rowKey={(c) => c.name} columns={columns} sortKey="sort" />
        <output aria-label="URL">{location.search}</output>
      </>
    );
  }
  render(
    <MemoryRouter initialEntries={["/checks?sort=-latency"]}>
      <Sorted />
    </MemoryRouter>,
  );
  const header = (name: string) => screen.getByRole("columnheader", { name: new RegExp(name) });

  expect(header("Latency").getAttribute("aria-sort")).toBe("descending");
  expect(bodyRows().map((r) => r[0])).toEqual(["nas", "dns", "router"]);

  fireEvent.click(within(header("Name")).getByRole("button"));
  expect(url()).toBe("?sort=name");
  expect(bodyRows().map((r) => r[0])).toEqual(["dns", "nas", "router"]);
});
