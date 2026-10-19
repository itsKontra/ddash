// PROTOTYPE variant B: narrow icon rail with live badges; startpage is a status column
// pinned left plus three free lanes. Widgets have no sizes: lane width is fixed, height follows content.
import { useState } from "react";
import { LayoutDashboard, Palette, Pencil, Plus, Puzzle, Settings, X } from "lucide-react";
import { Button, StatusDot, WidgetCard, cx, toneBg } from "../ddash-ui";
import { navEntries, widgetById } from "../registry";
import { AddWidgetSheet, problemsOf } from "../core";
import type { ShellProps } from "../App";

export default function VariantB({ page, setPage, renderCorePage }: ShellProps) {
  const [lanes, setLanes] = useState<string[][]>([
    ["apps.launcher", "dns.stats"],
    ["checks.uptime", "network.new-devices"],
    ["proxmox.guests", "docker.hosts", "reverse-proxy.health"],
  ]);
  const [edit, setEdit] = useState(false);
  const [addTo, setAddTo] = useState<number | null>(null);
  const [drag, setDrag] = useState<string | null>(null);
  const placed = lanes.flat();
  const problems = problemsOf([...placed, "checks.summary", "network.vpn"]);

  const dropInto = (lane: number, before?: string) => {
    if (!drag || drag === before) return;
    const ls = lanes.map((l) => l.filter((x) => x !== drag));
    const at = before ? ls[lane].indexOf(before) : ls[lane].length;
    ls[lane].splice(at < 0 ? ls[lane].length : at, 0, drag);
    setLanes(ls);
  };

  const RailBtn = ({ id, label, icon: Icon, badge }: { id: string; label: string; icon: any; badge?: { count: number; tone: any } }) => (
    <button
      onClick={() => setPage(id)}
      title={label}
      className={cx(
        "group relative flex h-11 w-11 items-center justify-center rounded-ui cursor-pointer",
        page === id ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground",
      )}
    >
      <Icon className="h-5 w-5" />
      {badge && (
        <span className={cx("absolute -top-0.5 -right-0.5 flex h-4 min-w-4 items-center justify-center rounded-full px-1 text-[10px] font-bold text-white", toneBg[badge.tone as keyof typeof toneBg])}>
          {badge.count}
        </span>
      )}
      <span className="pointer-events-none absolute left-14 z-30 hidden whitespace-nowrap rounded-ui bg-foreground px-2 py-1 text-xs text-background group-hover:block">
        {label}
      </span>
    </button>
  );

  return (
    <div className="flex min-h-screen">
      <aside className="sticky top-0 flex h-screen w-16 shrink-0 flex-col items-center gap-1 border-r bg-card py-3">
        <div className="mb-3 flex h-9 w-9 items-center justify-center rounded-ui bg-primary text-sm font-black text-primary-foreground">d</div>
        <RailBtn id="home" label="Overview" icon={LayoutDashboard} />
        <div className="my-2 h-px w-8 bg-border" />
        {navEntries.map((n) => (
          <RailBtn key={n.id} id={n.id} label={n.label} icon={n.icon} badge={n.badge} />
        ))}
        <div className="mt-auto flex flex-col gap-1">
          <RailBtn id="catalogue" label="Plugins" icon={Puzzle} />
          <RailBtn id="uikit" label="UI kit (proto)" icon={Palette} />
          <RailBtn id="settings" label="Settings" icon={Settings} />
        </div>
      </aside>

      <main className="min-w-0 flex-1">
        {page !== "home" ? (
          <div className="p-8">{renderCorePage(page)}</div>
        ) : (
          <div className="flex min-h-screen">
            {/* Status column: always first, not a Widget, not removable */}
            <section className="sticky top-0 h-screen w-80 shrink-0 overflow-y-auto border-r bg-card p-6">
              <div className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Status</div>
              <div className={cx("text-3xl font-bold leading-tight", problems.some((p) => p.health.tone === "down") ? "text-down" : "text-up")}>
                {problems.length ? `${problems.length} things need you` : "All up"}
              </div>
              <div className="mt-6 space-y-4">
                {problems.map((p) => (
                  <div key={p.title} className="flex gap-3">
                    <div className={cx("w-1 shrink-0 rounded-full", toneBg[p.health.tone])} />
                    <div>
                      <div className="text-sm font-semibold">{p.title}</div>
                      <div className="text-sm text-muted-foreground">{p.health.summary}</div>
                    </div>
                  </div>
                ))}
              </div>
              <div className="mt-8 border-t pt-4 text-xs text-muted-foreground">
                <div className="mb-2 font-semibold uppercase tracking-wide">Fine</div>
                <div className="space-y-1.5">
                  {[...placed, "network.vpn"]
                    .map(widgetById)
                    .filter((w) => w.health.tone === "up")
                    .map((w) => (
                      <div key={w.id} className="flex items-center gap-2">
                        <StatusDot tone="up" /> {w.title}
                      </div>
                    ))}
                </div>
              </div>
            </section>

            <section className="min-w-0 flex-1 p-6">
              <div className="mb-4 flex justify-end">
                <Button size="sm" variant={edit ? "default" : "ghost"} onClick={() => setEdit(!edit)}>
                  <Pencil className="h-3.5 w-3.5" /> {edit ? "Done" : "Arrange"}
                </Button>
              </div>
              <div className="grid grid-cols-3 items-start gap-4">
                {lanes.map((lane, li) => (
                  <div
                    key={li}
                    className={cx("min-h-40 space-y-4 rounded-ui", edit && "border-2 border-dashed p-2")}
                    onDragOver={(e) => e.preventDefault()}
                    onDrop={() => dropInto(li)}
                  >
                    {lane.map((id) => {
                      const w = widgetById(id);
                      return (
                        <div
                          key={id}
                          draggable={edit}
                          onDragStart={() => setDrag(id)}
                          onDragEnd={() => setDrag(null)}
                          onDragOver={(e) => e.preventDefault()}
                          onDrop={(e) => {
                            e.stopPropagation();
                            dropInto(li, id);
                          }}
                          className={cx(edit && "cursor-grab", drag === id && "opacity-40")}
                        >
                          <WidgetCard
                            title={w.title}
                            health={w.health}
                            actions={
                              edit && (
                                <Button size="icon" variant="ghost" onClick={() => setLanes(lanes.map((l) => l.filter((x) => x !== id)))}>
                                  <X className="h-4 w-4" />
                                </Button>
                              )
                            }
                          >
                            <w.Body size="M" />
                          </WidgetCard>
                        </div>
                      );
                    })}
                    {edit && (
                      <Button variant="ghost" className="w-full" onClick={() => setAddTo(li)}>
                        <Plus className="h-4 w-4" /> Add to lane
                      </Button>
                    )}
                  </div>
                ))}
              </div>
            </section>
          </div>
        )}
      </main>
      <AddWidgetSheet
        open={addTo !== null}
        onClose={() => setAddTo(null)}
        placed={placed}
        onAdd={(id) => setLanes(lanes.map((l, i) => (i === addTo ? [...l, id] : l)))}
      />
    </div>
  );
}
