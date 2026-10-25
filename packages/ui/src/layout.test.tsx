import { cleanup, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it } from "vitest";
import { HeroCard, MetricTile, PageHeader, PageTitle, Panel, Ring, SectionHeading, WidgetCard } from "./index";

afterEach(cleanup);

it("PageHeader shows the title as the Page's heading, with subtitle and actions", () => {
  render(<PageHeader title="Docker" subtitle="Containers per host" actions={<button>Refresh</button>} />);

  expect(screen.getByRole("heading", { level: 1, name: "Docker" })).toBeTruthy();
  expect(screen.getByText("Containers per host")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Refresh" })).toBeTruthy();
});

it("PageTitle names the Page with its title alone, the kicker and subtitle around it", () => {
  render(<PageTitle kicker="System" title="Settings" subtitle="How ddash looks and runs" actions={<button>Save</button>} />);

  expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("Settings");
  expect(screen.getByText("System")).toBeTruthy();
  expect(screen.getByText("How ddash looks and runs")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
});

it("PageTitle's back is a link to where the Page came from", () => {
  render(
    <MemoryRouter initialEntries={["/network/devices/aa"]}>
      <PageTitle title="aa:bb" back={{ href: "/network", label: "Network" }} />
    </MemoryRouter>,
  );

  expect(screen.getByRole("link", { name: "Network" }).getAttribute("href")).toBe("/network");
});

it("Panel with a title is a section named by it, with its description, actions and toolbar", () => {
  render(
    <Panel kicker="Docker" title="media" description="12 containers" actions={<button>Refresh</button>} toolbar={<input aria-label="Search" />}>
      <p>body</p>
    </Panel>,
  );

  const panel = screen.getByRole("region", { name: "media" });
  expect(within(panel).getByRole("heading", { level: 2 }).textContent).toBe("media");
  expect(within(panel).getByText("Docker")).toBeTruthy();
  expect(within(panel).getByText("12 containers")).toBeTruthy();
  expect(within(panel).getByRole("button", { name: "Refresh" })).toBeTruthy();
  expect(within(panel).getByRole("textbox", { name: "Search" })).toBeTruthy();
  expect(within(panel).getByText("body")).toBeTruthy();
});

it("Panel with only a kicker is still a section, named by the kicker", () => {
  render(<Panel kicker="Maintenance">none planned</Panel>);

  expect(screen.getByRole("region", { name: "Maintenance" }).textContent).toContain("none planned");
});

it("Panel without a title or kicker is a plain surface, not a landmark", () => {
  const { container } = render(<Panel>an App card</Panel>);

  expect(screen.queryByRole("region")).toBeNull();
  expect(screen.queryByRole("heading")).toBeNull();
  expect(container.firstElementChild?.tagName).toBe("DIV");
  expect(container.textContent).toBe("an App card");
});

it("SectionHeading is a heading between Panels named by its title, its kicker and actions beside it", () => {
  render(<SectionHeading kicker="3 of 6" title="Installed" actions={<button>Check for updates</button>} />);

  expect(screen.getByRole("heading", { level: 2 }).textContent).toBe("Installed");
  expect(screen.getByText("3 of 6")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Check for updates" })).toBeTruthy();
});

it("MetricTile is a group named by its label, holding the value and its description", () => {
  render(<MetricTile label="Down" value={2} icon="circle-x" tone="down" desc="since 09:12" />);

  const tile = screen.getByRole("group", { name: "Down" });
  expect(tile.textContent).toContain("2");
  expect(tile.textContent).toContain("since 09:12");
  expect(within(tile).queryByRole("status")).toBeNull();
});

it("MetricTile shows a skeleton while its value is undefined", () => {
  render(<MetricTile label="Containers" value={undefined} />);

  const tile = screen.getByRole("group", { name: "Containers" });
  expect(within(tile).getByRole("status", { name: "Loading" })).toBeTruthy();
});

it("MetricTile shows a zero as a value, not as loading", () => {
  render(<MetricTile label="Down" value={0} />);

  const tile = screen.getByRole("group", { name: "Down" });
  expect(within(tile).queryByRole("status")).toBeNull();
  expect(tile.textContent).toBe("Down0");
});

it("HeroCard is a section named by its title, with kicker, caption, foot and aside", () => {
  render(
    <HeroCard
      kicker="Status"
      title="2 things need attention"
      caption={<p>Proxmox is down</p>}
      foot={<span>9 up · 2 down</span>}
      aside={<Ring ratio={0.8} value="9 / 11" label="Widgets up" />}
    />,
  );

  const hero = screen.getByRole("region", { name: "2 things need attention" });
  expect(within(hero).getByRole("heading", { level: 2 }).textContent).toBe("2 things need attention");
  expect(within(hero).getByText("Status")).toBeTruthy();
  expect(within(hero).getByText("Proxmox is down")).toBeTruthy();
  expect(within(hero).getByText("9 up · 2 down")).toBeTruthy();
  expect(within(hero).getByRole("meter", { name: "Widgets up" })).toBeTruthy();
});

it("A Ring draws its arc in nav-accent on a HeroCard and in primary elsewhere", () => {
  render(
    <>
      <HeroCard kicker="Status" title="All up" aside={<Ring ratio={1} value="11" label="On the hero" />} />
      <Ring ratio={1} value="11" label="On a Panel" />
    </>,
  );

  const arc = (label: string) => screen.getByRole("meter", { name: label }).querySelector("[data-colour]")?.getAttribute("data-colour");
  expect(arc("On the hero")).toBe("nav-accent");
  expect(arc("On a Panel")).toBe("primary");
});

it("WidgetCard's header writes the summary out when the Widget isn't up", () => {
  render(
    <>
      <WidgetCard title="Docker" status={{ tone: "down", summary: "media unreachable" }}>
        body
      </WidgetCard>
      <WidgetCard title="Checks" status={{ tone: "up", summary: "12 up" }}>
        body
      </WidgetCard>
    </>,
  );

  const header = (name: string) => screen.getByRole("heading", { name }).closest("header")!;
  expect(header("Docker").textContent).toContain("media unreachable");
  expect(within(header("Docker")).getByRole("img", { name: "down: media unreachable" })).toBeTruthy();
  expect(header("Checks").textContent).not.toContain("12 up");
  expect(within(header("Checks")).getByRole("img", { name: "up: 12 up" })).toBeTruthy();
});
