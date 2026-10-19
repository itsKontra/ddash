// PROTOTYPE: mock installed Plugins. The shapes here (NavEntry, WidgetDef) are the
// questions the Plugin contract has to answer, so they are deliberately explicit.
import type { LucideIcon } from "lucide-react";
import {
  AppWindow,
  Activity,
  Network,
  Waypoints,
  ShieldBan,
  Container,
  Server,
  BookOpen,
} from "lucide-react";
import { AppIcon, Badge, Button, Stat, StatusDot, UptimeBars, type Health, type Tone, type WidgetSize } from "./ddash-ui";

export type NavGroup = "Monitor" | "Network" | "Compute";

export type NavEntry = {
  id: string;
  plugin: string;
  label: string;
  icon: LucideIcon;
  group: NavGroup; // used by variant C only
  badge?: { count: number; tone: Tone }; // live count from the Plugin, e.g. "2 down"
};

export type WidgetDef = {
  id: string;
  plugin: string;
  title: string;
  sizes: WidgetSize[]; // used by variant A only
  health: Health; // what the Widget reports to the Hero
  Body: React.FC<{ size: WidgetSize }>;
};

export type Plugin = { id: string; name: string; description: string; installed: boolean };

export const plugins: Plugin[] = [
  { id: "apps", name: "Apps", description: "Apps discovered from Proxy Hosts, plus manual ones", installed: true },
  { id: "checks", name: "Checks", description: "Uptime, history and ntfy notifications", installed: true },
  { id: "network", name: "Network", description: "Devices, Adopt, Direct and the VPN Tunnel", installed: true },
  { id: "reverse-proxy", name: "Reverse proxy", description: "Read-only view of nginx Proxy Hosts", installed: true },
  { id: "dns", name: "DNS", description: "AdGuard Home stats and per-Device blocking", installed: true },
  { id: "docker", name: "Docker", description: "Containers on every Docker host", installed: true },
  { id: "proxmox", name: "Proxmox", description: "Guests and power actions", installed: true },
  { id: "docs", name: "Docs", description: "Notes about the home network", installed: false },
];

const apps = [
  { slug: "jellyfin", name: "Jellyfin", tone: "up" as Tone },
  { slug: "grafana", name: "Grafana", tone: "up" as Tone },
  { slug: "home-assistant", name: "Home Assistant", tone: "up" as Tone },
  { slug: "paperless-ngx", name: "Paperless", tone: "down" as Tone },
  { slug: "immich", name: "Immich", tone: "up" as Tone },
  { slug: "vaultwarden", name: "Vaultwarden", tone: "up" as Tone },
  { slug: "nextcloud", name: "Nextcloud", tone: "maint" as Tone },
  { slug: "sonarr", name: "Sonarr", tone: "up" as Tone },
  { slug: "radarr", name: "Radarr", tone: "up" as Tone },
  { slug: "keycloak", name: "Keycloak", tone: "up" as Tone },
  { slug: "proxmox", name: "Proxmox", tone: "up" as Tone },
  { slug: "adguard-home", name: "AdGuard", tone: "up" as Tone },
];

const bars = (n: number, bad: number[] = [], maint: number[] = []): Tone[] =>
  Array.from({ length: n }, (_, i) => (bad.includes(i) ? "down" : maint.includes(i) ? "maint" : "up"));

export const navEntries: NavEntry[] = [
  { id: "apps", plugin: "apps", label: "Apps", icon: AppWindow, group: "Monitor" },
  { id: "checks", plugin: "checks", label: "Checks", icon: Activity, group: "Monitor", badge: { count: 1, tone: "down" } },
  { id: "network", plugin: "network", label: "Network", icon: Network, group: "Network", badge: { count: 2, tone: "warn" } },
  { id: "dns", plugin: "dns", label: "DNS", icon: ShieldBan, group: "Network" },
  { id: "reverse-proxy", plugin: "reverse-proxy", label: "Reverse proxy", icon: Waypoints, group: "Network" },
  { id: "docker", plugin: "docker", label: "Docker", icon: Container, group: "Compute" },
  { id: "proxmox", plugin: "proxmox", label: "Proxmox", icon: Server, group: "Compute" },
];

export const notInstalledNav: NavEntry = { id: "docs", plugin: "docs", label: "Docs", icon: BookOpen, group: "Monitor" };

export const widgets: WidgetDef[] = [
  {
    id: "apps.launcher",
    plugin: "apps",
    title: "Apps",
    sizes: ["M", "L"],
    health: { tone: "down", summary: "Paperless down · Nextcloud in Maintenance" },
    Body: ({ size }) => (
      <div className={size === "L" ? "grid grid-cols-4 gap-3" : "grid grid-cols-6 gap-2"}>
        {(size === "L" ? apps : apps.slice(0, 6)).map((a) => (
          <a key={a.slug} href="#" className="group flex flex-col items-center gap-1 rounded-ui p-1.5 hover:bg-muted">
            <div className="relative">
              <AppIcon slug={a.slug} name={a.name} size={size === "L" ? 36 : 28} />
              <span className="absolute -right-1 -bottom-1">
                <StatusDot tone={a.tone} />
              </span>
            </div>
            <span className="w-full truncate text-center text-[11px]">{a.name}</span>
          </a>
        ))}
      </div>
    ),
  },
  {
    id: "checks.summary",
    plugin: "checks",
    title: "Checks",
    sizes: ["S", "M"],
    health: { tone: "down", summary: "22 of 23 up · paperless.<domain> down 14 min" },
    Body: ({ size }) => (
      <div className="space-y-3">
        <div className="flex gap-6">
          <Stat label="up" value="22" tone="up" />
          <Stat label="down" value="1" tone="down" />
          {size !== "S" && <Stat label="Maintenance" value="1" tone="maint" />}
        </div>
        {size !== "S" && (
          <div className="space-y-1 text-sm">
            <div className="flex items-center gap-2">
              <StatusDot tone="down" />
              paperless.&lt;domain&gt;<span className="ml-auto text-muted-foreground">502 · 14 min</span>
            </div>
            <div className="flex items-center gap-2">
              <StatusDot tone="maint" />
              nextcloud.&lt;domain&gt;<span className="ml-auto text-muted-foreground">Maintenance · 6 min left</span>
            </div>
          </div>
        )}
      </div>
    ),
  },
  {
    id: "checks.uptime",
    plugin: "checks",
    title: "Uptime, last 24 h",
    sizes: ["M", "L"],
    health: { tone: "up", summary: "99.4 % across all Checks" },
    Body: ({ size }) => (
      <div className="space-y-2.5">
        {(size === "L" ? ["Jellyfin", "Paperless", "Home Assistant", "Vaultwarden", "Router DNS", "nginx"] : ["Jellyfin", "Paperless", "Router DNS"]).map(
          (n, i) => (
            <div key={n}>
              <div className="mb-1 flex text-xs">
                <span>{n}</span>
                <span className="ml-auto tabular-nums text-muted-foreground">{n === "Paperless" ? "96.1 %" : "100 %"}</span>
              </div>
              <UptimeBars buckets={bars(size === "L" ? 48 : 32, n === "Paperless" ? [29, 30, 31] : [], i === 4 ? [10] : [])} />
            </div>
          ),
        )}
      </div>
    ),
  },
  {
    id: "network.vpn",
    plugin: "network",
    title: "VPN Tunnel",
    sizes: ["S", "M"],
    health: { tone: "up", summary: "WireGuard primary up · OpenVPN standby" },
    Body: ({ size }) => (
      <div className="space-y-2 text-sm">
        <div className="flex items-center gap-2">
          <StatusDot tone="up" /> WireGuard <span className="ml-auto text-muted-foreground">primary · 24 ms</span>
        </div>
        <div className="flex items-center gap-2">
          <StatusDot tone="idle" /> OpenVPN <span className="ml-auto text-muted-foreground">standby</span>
        </div>
        {size === "M" && <div className="pt-1 text-xs text-muted-foreground">31 Devices Tunnelled · 3 Direct</div>}
      </div>
    ),
  },
  {
    id: "network.new-devices",
    plugin: "network",
    title: "New Devices",
    sizes: ["S", "M"],
    health: { tone: "warn", summary: "2 New Devices waiting to be Adopted" },
    Body: ({ size }) => (
      <div className="space-y-2 text-sm">
        {[
          { mac: "a4:83:e7:…:1f", vendor: "Apple", ip: "<lan>.143" },
          { mac: "3c:22:fb:…:9a", vendor: "Espressif", ip: "<lan>.151" },
        ].map((d) => (
          <div key={d.mac} className="flex items-center gap-2">
            <StatusDot tone="warn" />
            <span>{d.vendor}</span>
            {size === "M" && <span className="font-mono text-xs text-muted-foreground">{d.mac}</span>}
            <Button size="sm" variant="outline" className="ml-auto">
              Adopt
            </Button>
          </div>
        ))}
      </div>
    ),
  },
  {
    id: "dns.stats",
    plugin: "dns",
    title: "DNS",
    sizes: ["S", "M"],
    health: { tone: "up", summary: "Protection on · 18 % blocked today" },
    Body: ({ size }) => (
      <div className="flex gap-6">
        <Stat label="queries today" value="48.2k" />
        <Stat label="blocked" value="18 %" />
        {size === "M" && <Stat label="avg latency" value="9 ms" />}
      </div>
    ),
  },
  {
    id: "reverse-proxy.health",
    plugin: "reverse-proxy",
    title: "nginx config",
    sizes: ["S"],
    health: { tone: "up", summary: "nginx -T OK · 24 Proxy Hosts · read 12 min ago" },
    Body: () => (
      <div className="space-y-1 text-sm">
        <div className="flex items-center gap-2">
          <StatusDot tone="up" /> Config valid
        </div>
        <div className="text-xs text-muted-foreground">24 Proxy Hosts · 1 warning · read 12 min ago</div>
      </div>
    ),
  },
  {
    id: "docker.hosts",
    plugin: "docker",
    title: "Docker hosts",
    sizes: ["S", "M"],
    health: { tone: "up", summary: "3 hosts · 41 of 42 containers running" },
    Body: ({ size }) => (
      <div className="space-y-1.5 text-sm">
        {[
          ["ddash", "1/1"],
          ["docker-env", "17/18"],
          ["media-stack", "23/23"],
        ].map(([h, c]) => (
          <div key={h} className="flex items-center gap-2">
            <StatusDot tone={c === "17/18" ? "warn" : "up"} />
            {h}
            <span className="ml-auto tabular-nums text-muted-foreground">{c}</span>
            {size === "M" && <Badge>{h === "media-stack" ? "socket-proxy" : "socket-proxy"}</Badge>}
          </div>
        ))}
      </div>
    ),
  },
  {
    id: "proxmox.guests",
    plugin: "proxmox",
    title: "Proxmox guests",
    sizes: ["M", "L"],
    health: { tone: "up", summary: "9 of 10 guests running" },
    Body: ({ size }) => (
      <table className="w-full text-sm">
        <tbody>
          {[
            ["101", "nginx", "LXC", true, "2 %"],
            ["102", "docker-env", "LXC", true, "11 %"],
            ["103", "media-stack", "LXC", true, "34 %"],
            ["104", "ddash", "LXC", true, "1 %"],
            ["110", "home-assistant", "VM", true, "6 %"],
            ["120", "win-test", "VM", false, "–"],
          ]
            .slice(0, size === "L" ? 6 : 4)
            .map(([id, name, kind, on, cpu]) => (
              <tr key={id as string} className="border-t first:border-t-0">
                <td className="py-1 pr-2">
                  <StatusDot tone={on ? "up" : "idle"} />
                </td>
                <td className="py-1 tabular-nums text-muted-foreground">{id}</td>
                <td className="py-1">{name}</td>
                <td className="py-1 text-muted-foreground">{kind}</td>
                <td className="py-1 text-right tabular-nums text-muted-foreground">{cpu}</td>
              </tr>
            ))}
        </tbody>
      </table>
    ),
  },
];

export const widgetById = (id: string) => widgets.find((w) => w.id === id)!;
export const pluginName = (id: string) => plugins.find((p) => p.id === id)?.name ?? id;
