// PROTOTYPE variant A: classic labelled sidebar; startpage is a Hero over a 4-column grid
// where every Widget declares the sizes it supports (S = 1×1, M = 2×1, L = 2×2).
import { useState } from "react";
import { GripVertical, LayoutDashboard, Pencil, Plus, Puzzle, Settings, Palette, X } from "lucide-react";
import { Badge, Button, Hero, Segmented, WidgetCard, cx, type WidgetSize } from "../ddash-ui";
import { navEntries, widgetById } from "../registry";
import { AddWidgetSheet, problemsOf } from "../core";
import type { ShellProps } from "../App";

const span: Record<WidgetSize, string> = { S: "col-span-1 row-span-1", M: "col-span-2 row-span-1", L: "col-span-2 row-span-2" };

export default function VariantA({ page, setPage, renderCorePage }: ShellProps) {
  const [layout, setLayout] = useState<{ id: string; size: WidgetSize }[]>([
    { id: "checks.summary", size: "M" },
    { id: "network.vpn", size: "S" },
    { id: "network.new-devices", size: "S" },
    { id: "apps.launcher", size: "L" },
    { id: "checks.uptime", size: "M" },
    { id: "proxmox.guests", size: "M" },
    { id: "dns.stats", size: "S" },
    { id: "docker.hosts", size: "S" },
  ]);
  const [edit, setEdit] = useState(false);
  const [adding, setAdding] = useState(false);
  const [drag, setDrag] = useState<string | null>(null);

  const move = (from: string, to: string) => {
    if (from === to) return;
    const l = [...layout];
    const a = l.findIndex((x) => x.id === from);
    const [item] = l.splice(a, 1);
    l.splice(l.findIndex((x) => x.id === to), 0, item);
    setLayout(l);
  };

  const NavLink = ({ id, label, icon: Icon, badge }: { id: string; label: string; icon: any; badge?: React.ReactNode }) => (
    <button
      onClick={() => setPage(id)}
      className={cx(
        "flex w-full items-center gap-3 rounded-ui px-3 py-2 text-sm cursor-pointer",
        page === id ? "bg-primary text-primary-foreground" : "hover:bg-muted",
      )}
    >
      <Icon className="h-4 w-4" />
      {label}
      <span className="ml-auto">{badge}</span>
    </button>
  );

  return (
    <div className="flex min-h-screen">
      <aside className="sticky top-0 flex h-screen w-60 shrink-0 flex-col border-r bg-card p-3">
        <div className="mb-4 px-3 py-2 text-lg font-bold tracking-tight">ddash</div>
        <NavLink id="home" label="Overview" icon={LayoutDashboard} />
        <div className="mt-4 mb-1 px-3 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">Plugins</div>
        <nav className="space-y-0.5">
          {navEntries.map((n) => (
            <NavLink key={n.id} id={n.id} label={n.label} icon={n.icon} badge={n.badge && <Badge tone={n.badge.tone}>{n.badge.count}</Badge>} />
          ))}
        </nav>
        <div className="mt-auto space-y-0.5 border-t pt-3">
          <NavLink id="catalogue" label="Plugins" icon={Puzzle} />
          <NavLink id="settings" label="Settings" icon={Settings} />
          <NavLink id="uikit" label="UI kit (proto)" icon={Palette} />
        </div>
      </aside>

      <main className="min-w-0 flex-1 p-8">
        {page !== "home" ? (
          renderCorePage(page)
        ) : (
          <div className="space-y-6">
            <div className="flex items-center">
              <h1 className="text-2xl font-semibold tracking-tight">Overview</h1>
              <div className="ml-auto flex gap-2">
                {edit && (
                  <Button variant="outline" onClick={() => setAdding(true)}>
                    <Plus className="h-4 w-4" /> Add Widget
                  </Button>
                )}
                <Button variant={edit ? "default" : "outline"} onClick={() => setEdit(!edit)}>
                  <Pencil className="h-4 w-4" /> {edit ? "Done" : "Edit"}
                </Button>
              </div>
            </div>
            <Hero total={layout.length} problems={problemsOf(layout.map((l) => l.id))} />
            <div className="grid auto-rows-[minmax(150px,auto)] grid-cols-4 gap-4 [grid-auto-flow:dense]">
              {layout.map(({ id, size }) => {
                const w = widgetById(id);
                return (
                  <div
                    key={id}
                    className={cx(span[size], drag === id && "opacity-40")}
                    draggable={edit}
                    onDragStart={() => setDrag(id)}
                    onDragEnd={() => setDrag(null)}
                    onDragOver={(e) => {
                      e.preventDefault();
                      if (drag) move(drag, id);
                    }}
                  >
                    <WidgetCard
                      className={cx("h-full", edit && "ring-2 ring-primary/30 cursor-grab")}
                      title={w.title}
                      health={w.health}
                      actions={
                        edit && (
                          <>
                            {w.sizes.length > 1 && (
                              <Segmented
                                value={size}
                                options={w.sizes}
                                onChange={(s) => setLayout(layout.map((x) => (x.id === id ? { ...x, size: s } : x)))}
                              />
                            )}
                            <GripVertical className="h-4 w-4 text-muted-foreground" />
                            <Button size="icon" variant="ghost" onClick={() => setLayout(layout.filter((x) => x.id !== id))}>
                              <X className="h-4 w-4" />
                            </Button>
                          </>
                        )
                      }
                    >
                      <w.Body size={size} />
                    </WidgetCard>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </main>
      <AddWidgetSheet
        open={adding}
        onClose={() => setAdding(false)}
        placed={layout.map((l) => l.id)}
        onAdd={(id) => setLayout([...layout, { id, size: widgetById(id).sizes[0] }])}
      />
    </div>
  );
}
