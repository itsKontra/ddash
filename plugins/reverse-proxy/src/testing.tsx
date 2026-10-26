import { renderScoped as render, type Answers } from "@ddash/plugin/testing";
import plugin from "./index";

/** The reverse-proxy Plugin's scope in tests. */
export const reverseProxy = { name: "reverse-proxy", requires: ["nginx-proxy-manager-api", "apps"] };

/** Now, in every test: Date is faked to it. */
export const now = new Date("2026-10-26T08:00:00Z");

/** n days in ms. */
export const days = (n: number) => n * 86_400_000;

/** The ISO time ms after now. */
export const fromNow = (ms: number) => new Date(now.getTime() + ms).toISOString();

/** GetStatus's answers (Connect JSON). */
export const up = { status: "STATUS_UP", readAt: "2026-10-26T07:59:00Z", lastGoodReadAt: "2026-10-26T07:59:00Z", version: "2.10.4", address: "198.18.1.210" };
export const warn = {
  status: "STATUS_WARN",
  readAt: "2026-10-26T07:59:00Z",
  lastGoodReadAt: "2026-10-26T07:15:00Z",
  error: "GET /api/nginx/proxy-hosts: context deadline exceeded",
  version: "2.10.4",
};
export const down = { status: "STATUS_DOWN", readAt: "2026-10-26T07:59:00Z", error: "sign-in refused: Invalid email or password" };

/** Nginx Proxy Manager's answer while it has no config: the core's failed_precondition. */
export const notConfigured = () => Response.json({ code: "failed_precondition", message: "not configured" }, { status: 400 });

/** A certificate as ListCertificates answers it, expiring ms after now. */
export const certificate = (id: number, provider: string, niceName: string, ms: number) => ({
  id: String(id),
  provider,
  niceName,
  domainNames: [`${niceName}.home.example`],
  expiresOn: fromNow(ms),
});

/**
 * ListCertificates' answer: the wildcard is OK, the printer's uploaded one
 * expires soon, legacy's Let's Encrypt one isn't renewing and old's has
 * expired.
 */
export const certificates = {
  certificates: [
    { ...certificate(1, "letsencrypt", "*.home.example", days(60)), domainNames: ["*.home.example", "home.example"] },
    certificate(2, "other", "printer", days(12)),
    certificate(3, "letsencrypt", "legacy", days(5)),
    certificate(4, "other", "old", -days(2)),
  ],
};

/**
 * ListApps' answer. Grafana, Jellyfin and the Hidden Old use the wildcard,
 * Printer the printer's certificate. Two Offline Proxy Hosts count:
 * Jellyfin's and NAS's; Printer's is Ignored and Old's is on a Hidden App.
 */
export const apps = {
  apps: [
    { id: "proxy:grafana.home.example", name: "Grafana", url: "https://grafana.home.example", reach: "REACH_PROXIED", proxyHost: { id: "3", certificateId: "1" } },
    {
      id: "proxy:jellyfin.home.example",
      name: "Jellyfin",
      url: "https://jellyfin.home.example",
      reach: "REACH_PROXIED",
      proxyHost: { id: "7", certificateId: "1", offline: true },
      warnings: [{ kind: "WARNING_KIND_PROXY_HOST_OFFLINE", detail: "nginx: [emerg] bad" }],
    },
    {
      id: "proxy:printer.home.example",
      name: "Printer",
      url: "https://printer.home.example",
      reach: "REACH_PROXIED",
      proxyHost: { id: "12", certificateId: "2", offline: true },
      warnings: [{ kind: "WARNING_KIND_PROXY_HOST_OFFLINE", ignored: true }],
    },
    {
      id: "proxy:old.home.example",
      name: "Old",
      url: "https://old.home.example",
      hidden: true,
      reach: "REACH_PROXIED",
      proxyHost: { id: "9", certificateId: "1", offline: true },
      warnings: [{ kind: "WARNING_KIND_PROXY_HOST_OFFLINE" }],
    },
    {
      id: "proxy:nas.home.example",
      name: "NAS",
      url: "http://nas.home.example",
      reach: "REACH_PROXIED",
      proxyHost: { id: "14", offline: true },
      warnings: [{ kind: "WARNING_KIND_NO_TLS" }, { kind: "WARNING_KIND_PROXY_HOST_OFFLINE" }],
    },
    { id: "app:00aa11bb22cc33dd", name: "pfSense", url: "https://198.18.0.1", reach: "REACH_LINK_ONLY" },
  ],
  proxyHosts: { readAt: "2026-10-26T07:59:00Z" },
};

/**
 * Renders children in the reverse-proxy Plugin's scope against fakes
 * answering by method name (GetStatus up, ListCertificates certificates
 * and ListApps apps unless answers say otherwise; Actions answer {}), with
 * the installed Plugins named, and returns the calls, the router starting
 * at path.
 */
export const renderScoped = (children: React.ReactNode, answers: Answers = {}, installed: string[] = ["reverse-proxy"], path = "/") =>
  render(reverseProxy, children, {
    answers: {
      GetStatus: () => Response.json(up),
      ListCertificates: () => Response.json(certificates),
      ListApps: () => Response.json(apps),
      ...answers,
    },
    installed,
    path,
  });

/** The reverse-proxy Page. */
export const Page = () => {
  const P = plugin.pages![0].Component!;
  return <P />;
};
