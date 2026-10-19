// PROTOTYPE variant C: sidebar grouped by a category each Nav Entry declares; startpage is one
// column of ExpandableCards. Every Widget must have a one-line summary; problems float to the top.
import { useState } from "react";
import { ArrowDown, ArrowUp, LayoutDashboard, Palette, Pencil, Plus, Puzzle, Settings, X } from "lucide-react";
import { Button, ExpandableCard, StatusDot, Switch, cx } from "../ddash-ui";
import { navEntries, notInstalledNav, widgetById, type NavGroup } from "../registry";
import { AddWidgetSheet } from "../core";
import type { ShellProps } from "../App";

const rank = { down: 0, warn: 1, maint: 2, up: 3, idle: 4 };

export default function VariantC({ page, setPage, renderCorePage }: ShellProps) {
  const [order, setOrder] = useState([
    "apps.launcher",
    "checks.summary",
    "network.vpn",
    "network.new-devices",
    "dns.stats",
    "proxmox.guests",
    "docker.hosts",
    "reverse-proxy.health",
    "checks.uptime",
  ]);
  const [open, setOpen] = useState<Set<string>>(new Set(["apps.launcher"]));
  const [floatProblems, setFloatProblems] = useState(true);
  const [edit, setEdit] = useState(false);
  const [adding, setAdding] = useState(false);

  const shown = floatProblems && !edit ? [...order].sort((a, b) => rank[widgetById(a).health.tone] - rank[widgetById(b).health.tone]) : order;
  const isOpen = (id: string) => open.has(id) || (floatProblems && !edit && widgetById(id).health.tone === "down");
  const toggle = (id: string) => {
    const s = new Set(open);
    s.has(id) ? s.delete(id) : s.add(id);
    setOpen(s);
  };
  const nudge = (id: string, d: number) => {
    const o = [...order];
    const i = o.indexOf(id);
    const j = i + d;
    if (j < 0 || j >= o.length) return;
    [o[i], o[j]] = [o[j], o[i]];
    setOrder(o);
  };

  const groups: NavGroup[] = ["Monitor", "Network", "Compute"];
  const healthFor: Record<string, string> = {
    apps: "1 down",
    checks: "22/23",
    network: "2 new",
    dns: "18 % blocked",
    "reverse-proxy": "24 hosts",
    docker: "41/42",
    proxmox: "9/10",
  };

  const Item = ({ id, label, icon: Icon, right }: { id: string; label: string; icon: any; right?: React.ReactNode }) => (
    <button
      onClick={() => setPage(id)}
      className={cx("flex w-full items-center gap-2.5 rounded-ui px-2.5 py-1.5 text-sm cursor-pointer", page === id ? "bg-muted font-semibold" : "hover:bg-muted/60")}
    >
      <Icon className="h-4 w-4 shrink-0 text-muted-foreground" />
      <span className="truncate">{label}</span>
      <span className="ml-auto whitespace-nowrap text-xs text-muted-foreground">{right}</span>
    </button>
  );

  return (
    <div className="min-h-screen">
      <header className="sticky top-0 z-20 flex h-14 items-center gap-4 border-b bg-background/80 px-5 backdrop-blur">
        <div className="font-bold tracking-tight">ddash</div>
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <StatusDot tone="down" pulse /> 1 down · 2 need attention
        </div>
        <div className="ml-auto flex items-center gap-1">
          <Button variant="ghost" size="icon" title="Plugins" onClick={() => setPage("catalogue")}>
            <Puzzle className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" title="UI kit (proto)" onClick={() => setPage("uikit")}>
            <Palette className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon" title="Settings" onClick={() => setPage("settings")}>
            <Settings className="h-4 w-4" />
          </Button>
        </div>
      </header>
      <div className="flex">
        <aside className="sticky top-14 h-[calc(100vh-3.5rem)] w-56 shrink-0 overflow-y-auto border-r p-3">
          <Item id="home" label="Overview" icon={LayoutDashboard} />
          {groups.map((g) => (
            <div key={g} className="mt-4">
              <div className="mb-1 px-2.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{g}</div>
              {navEntries
                .filter((n) => n.group === g)
                .map((n) => (
                  <Item
                    key={n.id}
                    id={n.id}
                    label={n.label}
                    icon={n.icon}
                    right={<span className={cx(n.badge?.tone === "down" && "text-down", n.badge?.tone === "warn" && "text-warn")}>{healthFor[n.plugin]}</span>}
                  />
                ))}
            </div>
          ))}
          <div className="mt-4 px-2.5 text-xs text-muted-foreground">
            <span className="opacity-60">+ {notInstalledNav.label} (not installed)</span>
          </div>
        </aside>

        <main className="mx-auto w-full max-w-4xl min-w-0 p-8">
          {page !== "home" ? (
            renderCorePage(page)
          ) : (
            <div>
              <div className="mb-4 flex items-center gap-3">
                <h1 className="text-2xl font-semibold tracking-tight">Overview</h1>
                <label className="ml-auto flex items-center gap-2 text-sm text-muted-foreground">
                  <Switch checked={floatProblems} onChange={setFloatProblems} /> Problems first
                </label>
                {edit && (
                  <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
                    <Plus className="h-3.5 w-3.5" /> Add
                  </Button>
                )}
                <Button size="sm" variant={edit ? "default" : "outline"} onClick={() => setEdit(!edit)}>
                  <Pencil className="h-3.5 w-3.5" /> {edit ? "Done" : "Edit"}
                </Button>
              </div>
              <div className="space-y-2">
                {shown.map((id) => {
                  const w = widgetById(id);
                  return (
                    <ExpandableCard
                      key={id}
                      title={w.title}
                      health={w.health}
                      open={isOpen(id)}
                      onToggle={() => toggle(id)}
                      aside={
                        edit && (
                          <div className="flex">
                            <Button size="icon" variant="ghost" onClick={() => nudge(id, -1)}>
                              <ArrowUp className="h-4 w-4" />
                            </Button>
                            <Button size="icon" variant="ghost" onClick={() => nudge(id, 1)}>
                              <ArrowDown className="h-4 w-4" />
                            </Button>
                            <Button size="icon" variant="ghost" onClick={() => setOrder(order.filter((x) => x !== id))}>
                              <X className="h-4 w-4" />
                            </Button>
                          </div>
                        )
                      }
                    >
                      <w.Body size="L" />
                    </ExpandableCard>
                  );
                })}
              </div>
              {floatProblems && !edit && <p className="mt-3 text-xs text-muted-foreground">Down Widgets float up and open themselves. Your order applies within each status.</p>}
            </div>
          )}
        </main>
      </div>
      <AddWidgetSheet open={adding} onClose={() => setAdding(false)} placed={order} onAdd={(id) => setOrder([...order, id])} />
    </div>
  );
}
