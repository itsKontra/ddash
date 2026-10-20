import { describe, expect, it } from "vitest";
import { checkDefinition } from "./contract.ts";

const Page = () => null;
const Body = () => null;
const useStatus = () => ({ tone: "up", summary: "ok" });
const widget = { id: "running", title: "Containers", sizes: ["S", "M"], defaultSize: "S", Body, useStatus };

describe("checkDefinition", () => {
  it("passes a Plugin with a Nav Entry leading to its index Page", () => {
    expect(checkDefinition("docker", { nav: { order: 50 }, pages: [{ index: true, Component: Page }] })).toEqual([]);
  });

  it("passes a Plugin with only Widgets", () => {
    expect(checkDefinition("docker", { widgets: [widget] })).toEqual([]);
  });

  it("fails a bundle whose default export isn't a definition", () => {
    expect(checkDefinition("docker", undefined)).toEqual(["the bundle's default export must be definePlugin({ nav, pages, widgets })"]);
  });

  it("fails more than one Nav Entry", () => {
    expect(checkDefinition("docker", { nav: [{ order: 1 }, { order: 2 }], pages: [{ index: true, Component: Page }] })).toEqual([
      "nav: at most one Nav Entry, not a list",
    ]);
  });

  it("passes Pages relative to the Plugin's path, or absolute under it", () => {
    const pages = [
      { index: true, Component: Page },
      { path: "hosts/:host", Component: Page, children: [{ path: "/docker/hosts/:host/logs", Component: Page }] },
      { path: "/docker", Component: Page },
    ];
    expect(checkDefinition("docker", { pages })).toEqual([]);
  });

  it("fails a Page outside /<name>/*", () => {
    const pages = [
      { path: "/settings", Component: Page },
      { path: "hosts", Component: Page, children: [{ path: "/dockerish/logs", Component: Page }] },
      { path: "../auth", Component: Page },
    ];
    expect(checkDefinition("docker", { pages })).toEqual([
      'pages[0].path: "/settings" is outside /docker/*',
      'pages[1].children[0].path: "/dockerish/logs" is outside /docker/*',
      'pages[2].path: "../auth" is outside /docker/*',
    ]);
  });

  it("fails a Nav Entry without an index Page to lead to", () => {
    expect(checkDefinition("docker", { nav: { order: 50 }, pages: [{ path: "hosts", Component: Page }] })).toEqual([
      "nav: the Nav Entry leads to the index Page, but pages has no index route",
    ]);
  });

  it("fails a Widget size other than S, M or L", () => {
    expect(checkDefinition("docker", { widgets: [{ ...widget, sizes: ["S", "XL"] }] })).toEqual([
      'widgets[0].sizes: "XL" isn\'t S, M or L',
    ]);
  });

  it("fails a Widget with no sizes", () => {
    expect(checkDefinition("docker", { widgets: [{ ...widget, sizes: [], defaultSize: undefined }] })).toEqual([
      "widgets[0].sizes: must list at least one of S, M and L",
      "widgets[0].defaultSize: undefined isn't one of its sizes",
    ]);
  });

  it("fails a defaultSize the Widget doesn't offer", () => {
    expect(checkDefinition("docker", { widgets: [{ ...widget, sizes: ["S", "M"], defaultSize: "L" }] })).toEqual([
      'widgets[0].defaultSize: "L" isn\'t one of its sizes',
    ]);
  });

  it("fails a Widget without useStatus", () => {
    const { useStatus: _, ...noStatus } = widget;
    expect(checkDefinition("docker", { widgets: [noStatus] })).toEqual(["widgets[0].useStatus: must be a hook returning the Widget's Status"]);
  });

  it("fails Widgets sharing an id", () => {
    expect(checkDefinition("docker", { widgets: [widget, { ...widget }] })).toEqual(['widgets[1].id: "running" is used twice']);
  });
});
