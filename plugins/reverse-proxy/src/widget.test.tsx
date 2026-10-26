import { cleanup, screen, within } from "@testing-library/react";
import type { Badge } from "@ddash/plugin";
import { pending, settle, showWidget, unavailable, type Answers } from "@ddash/plugin/testing";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import plugin from "./index";
import { apps, certificate, certificates, days, down, now, notConfigured, renderScoped, reverseProxy, up, warn } from "./testing";

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(now);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const widget = () => plugin.widgets!.find((w) => w.id === "certificates")!;

/** The Widget's status and body over these certificates. */
async function show(certs: object[] | ((body: unknown) => Response | Promise<Response>)) {
  const ListCertificates = typeof certs === "function" ? certs : () => Response.json({ certificates: certs });
  const { reported } = await showWidget(reverseProxy, widget(), { answers: { ListCertificates } });
  return { status: reported.status, body: screen.getByRole("region", { name: "widget" }) };
}

it("is the Certificates Widget, S only", () => {
  const w = widget();
  expect([w.title, w.sizes, w.defaultSize]).toEqual(["Certificates", ["S"], "S"]);
});

it("shows the nearest expiry in up while every certificate is OK", async () => {
  const { status, body } = await show([certificate(1, "letsencrypt", "far", days(80)), certificate(2, "letsencrypt", "near", days(40))]);

  expect(status).toEqual({ tone: "up", summary: "near expires in 40 days" });
  expect(body.textContent).toMatch(/near/);
  expect(within(body).getByText((_, t) => t?.tagName === "TIME").getAttribute("datetime")).toBe("2026-12-05T08:00:00.000Z");
});

it("tones the nearest expiry by the worst state, even when another certificate has it", async () => {
  // near is OK at 25 days; the uploaded far one expires soon at 29.
  const { status } = await show([certificate(1, "letsencrypt", "near", days(25)), certificate(2, "other", "far", days(29))]);
  expect(status).toEqual({ tone: "warn", summary: "near expires in 25 days" });
});

it("is down while a certificate has expired", async () => {
  const { status, body } = await show(certificates.certificates);
  expect(status).toEqual({ tone: "down", summary: "old expired 2 days ago" });
  expect(body.textContent).toMatch(/old/);
});

it("is idle without certificates", async () => {
  expect((await show([])).status).toEqual({ tone: "idle", summary: "no certificates" });
});

it("shows a skeleton until nginx-proxy-manager-api answers", async () => {
  const { status, body } = await show(pending);
  expect(status).toBeUndefined();
  expect(within(body).getByRole("status", { name: "Loading" })).toBeTruthy();
});

it("asks to set up Nginx Proxy Manager until it is configured", async () => {
  expect((await show(notConfigured)).status).toEqual({ tone: "warn", summary: "Set up Nginx Proxy Manager" });
});

it("reports warn when nginx-proxy-manager-api is unavailable", async () => {
  expect((await show(() => unavailable("nginx-proxy-manager-api is down"))).status).toEqual({
    tone: "warn",
    summary: "nginx-proxy-manager-api unavailable: nginx-proxy-manager-api is down",
  });
});

async function badge(answers: Answers) {
  const seen: { badge?: Badge | null } = {};
  function Probe() {
    seen.badge = plugin.nav!.useBadge!();
    return null;
  }
  renderScoped(<Probe />, answers);
  await settle();
  return seen.badge;
}

it("badges the Nav Entry with ! while Nginx Proxy Manager is Down", async () => {
  expect(await badge({ GetStatus: () => Response.json(down) })).toEqual({ text: "!", tone: "down" });
});

it("doesn't badge Warn, Offline Proxy Hosts, certificate states or a missing config", async () => {
  // apps has Offline Proxy Hosts that count, and certificates has an expired one.
  for (const GetStatus of [() => Response.json(warn), () => Response.json(up), notConfigured]) {
    expect(await badge({ GetStatus, ListApps: () => Response.json(apps), ListCertificates: () => Response.json(certificates) })).toBeNull();
    cleanup();
  }
});
