import { cleanup, screen, within } from "@testing-library/react";
import { settle, unavailable } from "@ddash/plugin/testing";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { certificate, days, now, Page, renderScoped } from "./testing";

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(now);
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

const panel = () => screen.getByRole("region", { name: "Certificates" });
const names = () => within(panel()).queryAllByRole("rowheader").map((h) => h.textContent);
const row = (name: string) => within(panel()).getByRole("rowheader", { name }).closest("tr")!;
/** A row's cells, by column header. */
function cells(name: string): Record<string, string> {
  const headers = within(panel()).getAllByRole("columnheader").map((h) => h.textContent ?? "");
  const texts = [...row(name).querySelectorAll("th, td")].map((c) => c.textContent ?? "");
  return Object.fromEntries(headers.map((h, i) => [h, texts[i]]));
}

/** The state cell of a one-certificate list. */
async function stateOf(cert: ReturnType<typeof certificate>) {
  renderScoped(<Page />, { ListCertificates: () => Response.json({ certificates: [cert] }) });
  await settle();
  return cells(cert.niceName).State;
}

it("lists every certificate with its domains, expiry, the Apps using it and its state", async () => {
  renderScoped(<Page />);
  await settle();

  expect(names()).toEqual(["*.home.example", "printer", "legacy", "old"]);
  const wildcard = cells("*.home.example");
  expect(wildcard.Domains).toBe("*.home.example, home.example");
  expect(wildcard.Expires).toMatch(/in 60 days/);
  expect(within(row("*.home.example")).getByText((_, t) => t?.tagName === "TIME").getAttribute("datetime")).toBe("2026-12-25T08:00:00.000Z");
  expect(wildcard.State).toBe("OK");
  expect(cells("old").Expires).toMatch(/2 days ago/);
});

it("joins the Apps using each certificate by its id, Hidden ones too", async () => {
  renderScoped(<Page />);
  await settle();

  expect(cells("*.home.example").Apps).toBe("Grafana, Jellyfin, Old");
  expect(cells("printer").Apps).toBe("Printer");
  expect(cells("legacy").Apps).toBe("None");
});

it("links each App to its pane on the Apps Page when that Plugin is installed", async () => {
  renderScoped(<Page />, {}, ["reverse-proxy", "apps"]);
  await settle();

  const link = within(row("printer")).getByRole("link", { name: "Printer" });
  expect(link.getAttribute("href")).toBe("/apps?app=proxy%3Aprinter.home.example");
});

it("says each state's message", async () => {
  renderScoped(<Page />);
  await settle();

  expect(cells("printer").State).toBe("Expires soonUpload a new certificate in Nginx Proxy Manager.");
  expect(cells("legacy").State).toBe("Not renewingNginx Proxy Manager hasn't renewed this: check its Let's Encrypt log.");
  expect(cells("old").State).toBe("ExpiredUpload a new certificate in Nginx Proxy Manager.");
});

it("shows the state nginx-proxy-manager-api tells, whatever the expiry", async () => {
  expect(await stateOf(certificate(1, "letsencrypt", "a", days(60), "EXPIRED"))).toBe("ExpiredNginx Proxy Manager hasn't renewed this: check its Let's Encrypt log.");
  cleanup();
  expect(await stateOf(certificate(1, "other", "a", -days(2)))).toBe("OK");
});

it("says when Nginx Proxy Manager has no certificates", async () => {
  renderScoped(<Page />, { ListCertificates: () => Response.json({}) });
  await settle();

  expect(panel().textContent).toMatch(/No certificates in Nginx Proxy Manager/);
});

it("says why the certificates couldn't be read", async () => {
  renderScoped(<Page />, { ListCertificates: () => unavailable("nginx-proxy-manager-api is down") });
  await settle();

  expect(within(panel()).getByRole("alert").textContent).toBe("Couldn't read the certificates: nginx-proxy-manager-api is down");
});
