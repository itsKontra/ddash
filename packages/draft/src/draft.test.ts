import { expect, it } from "vitest";
import { draftFragment, parseDraft, type Draft } from "./index";

const paperless: Draft = {
  host: "media",
  name: "paperless",
  compose: "services:\n  paperless:\n    image: ghcr.io/paperless-ngx/paperless-ngx:2.14.7\n    environment:\n      PAPERLESS_TITLE: Büro ✓\n",
  app: { url: "https://paperless.home.example", name: "Paperless-ngx", icon: "paperless-ngx", group: "Documents", service: "paperless", port: 8010 },
};

/** JSON as the fragment carries it: base64url, no padding. */
const encoded = (json: string) => {
  let bin = "";
  for (const b of new TextEncoder().encode(json)) bin += String.fromCharCode(b);
  return "#draft=" + btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};

it("reads back a draft it wrote, UTF-8 and all, with or without the #", () => {
  const fragment = draftFragment(paperless);
  expect(fragment).toMatch(/^#draft=[A-Za-z0-9_-]+$/);
  expect(parseDraft(fragment)).toEqual(paperless);
  expect(parseDraft(fragment.slice(1))).toEqual(paperless);
});

it("reads the JSON another Plugin wrote: only name and compose are needed", () => {
  expect(parseDraft(encoded(`{"name":"wiki","compose":"services: {}\\n"}`))).toEqual({ name: "wiki", compose: "services: {}\n" });
  expect(parseDraft(encoded(`{"name":"wiki","compose":"x","directory":"/opt/stacks/wiki","app":{"port":3000}}`))).toEqual({
    name: "wiki",
    compose: "x",
    directory: "/opt/stacks/wiki",
    app: { port: 3000 },
  });
});

it("drops fields of the wrong type, and a port that isn't one", () => {
  const d = parseDraft(encoded(`{"name":"wiki","compose":"x","host":7,"app":{"url":["no"],"name":"Wiki","port":70000}}`));
  expect(d).toEqual({ name: "wiki", compose: "x", app: { name: "Wiki" } });
  expect(parseDraft(encoded(`{"name":"wiki","compose":"x","app":{"port":80.5}}`))?.app?.port).toBeUndefined();
});

it("is no draft without a name and Compose file, or when it isn't base64url JSON", () => {
  for (const fragment of ["", "#", "#other=1", "#draft=", "#draft=%%%", encoded("not json"), encoded("[1,2]"), encoded(`{"name":"x"}`), encoded(`{"compose":"x"}`), encoded("null")]) {
    expect(parseDraft(fragment), fragment).toBeUndefined();
  }
});

it("refuses bytes that aren't UTF-8", () => {
  // 0xFF is no UTF-8: "/w" is base64url for it.
  expect(parseDraft("#draft=_w")).toBeUndefined();
});
