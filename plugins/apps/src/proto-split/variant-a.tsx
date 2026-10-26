// PROTOTYPE (wayfinder #139), variant A "Cards + detail page": today's
// cards by group, a Warnings Panel on top, a full detail route per App
// (like a Device's), Edit and Add App as Sheets. Add App is the map's shape
// as settled: a reverse proxy Switch, and with it off "Create Host Override"
// or "Link only".
import {
  AppIcon,
  AppIconPicker,
  Button,
  Checkbox,
  EmptyState,
  Facts,
  Field,
  FormActions,
  Menu,
  Notice,
  PageTitle,
  Panel,
  Pill,
  SearchField,
  SectionHeading,
  Segmented,
  Sheet,
  StatusList,
  Switch,
  TextInput,
  UptimeBars,
  matches,
  type StatusItem,
} from "@ddash/ui";
import { useId, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { coveringCert, hostOf, store, useStore, type PApp } from "./data";
import { AddProxyDialog, DeleteDialog } from "./flows";
import {
  appFromDraft,
  certText,
  CheckPill,
  commitEdit,
  dnsText,
  editDraft,
  emptyDraft,
  flags,
  Groups,
  inHome,
  nameError,
  NetLine,
  planAdd,
  planEdit,
  ProxyFields,
  reachLabel,
  ReachPill,
  ScenarioNotices,
  Steps,
  useKeep,
  useProxyBlocked,
  useRunner,
  warningTitle,
  WarningButtons,
  WorkloadSelect,
  workloadText,
  type Draft,
  type EditDraft,
} from "./shared";

export function VariantA() {
  const { apps, orphans } = useStore();
  const [q, setQ] = useState("");
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<PApp>();
  const [proxying, setProxying] = useState<PApp>();
  const [showHidden, setShowHidden] = useState(false);
  const shown = apps.filter((a) => matches(q, a.name, a.url, ...a.aliases, a.group));
  const visible = shown.filter((a) => !a.hidden);
  const hidden = shown.filter((a) => a.hidden);
  const groups = [...new Set(visible.map((a) => a.group))].sort((a, b) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)));
  return (
    <div>
      <PageTitle
        kicker="Services"
        title="Apps"
        subtitle="Every Proxy Host and Host Override, plus the links you add."
        actions={
          <>
            <div className="w-full sm:w-72">
              <SearchField label="Search Apps" value={q} onChange={setQ} placeholder="Search by name, domain or group" />
            </div>
            <Button icon="plus" onClick={() => setAdding(true)}>
              Add App
            </Button>
          </>
        }
      />
      <div className="flex flex-col gap-6">
        <ScenarioNotices />
        <WarningsPanel apps={apps} orphans={orphans} onEdit={setEditing} onAddProxy={setProxying} />
        {groups.map((g) => (
          <section key={g} aria-label={g || "Ungrouped"}>
            <SectionHeading title={g || "Ungrouped"} />
            <Cards apps={visible.filter((a) => a.group === g)} onEdit={setEditing} />
          </section>
        ))}
        {hidden.length > 0 && (
          <section aria-label="Hidden">
            <SectionHeading
              title={`Hidden (${hidden.length})`}
              actions={
                <Button size="sm" variant="ghost" icon={showHidden ? "chevron-up" : "chevron-down"} onClick={() => setShowHidden(!showHidden)}>
                  {showHidden ? "Collapse" : "Show"}
                </Button>
              }
            />
            {showHidden && <Cards apps={hidden} onEdit={setEditing} />}
          </section>
        )}
        {shown.length === 0 && (
          <Panel>
            <EmptyState icon="search-x" title={`No Apps match "${q}"`} />
          </Panel>
        )}
      </div>
      {adding && <AddSheet onClose={() => setAdding(false)} />}
      {editing && <EditSheet app={editing} onClose={() => setEditing(undefined)} />}
      {proxying && <AddProxyDialog app={proxying} onClose={() => setProxying(undefined)} />}
    </div>
  );
}

/** Every counting Warning on the page, with its fixes; ignored ones behind "Show ignored". Hidden Apps' Warnings stay on their detail. */
function WarningsPanel({
  apps,
  orphans,
  onEdit,
  onAddProxy,
}: {
  apps: PApp[];
  orphans: ReturnType<typeof useStore>["orphans"];
  onEdit: (a: PApp) => void;
  onAddProxy: (a: PApp) => void;
}) {
  const [showIgnored, setShowIgnored] = useState(false);
  const keep = useKeep();
  const all = apps.filter((a) => !a.hidden).flatMap((a) => a.warnings.map((w) => ({ app: a, w })));
  const ignored = all.filter((x) => x.w.ignored).length;
  const list = all.filter((x) => showIgnored || !x.w.ignored);
  if (!list.length && !orphans.length && !ignored) return null;
  const items: StatusItem[] = [
    ...list.map(({ app, w }) => ({
      tone: w.ignored ? ("idle" as const) : ("warn" as const),
      title: (
        <span>
          <Link to={`${app.id}${keep}`} className="hover:underline">
            {app.name}
          </Link>
          : {warningTitle[w.kind]}
          {w.ignored && <span className="ml-2 text-xs font-normal text-muted-foreground">ignored</span>}
        </span>
      ),
      meta: w.detail,
      body: (
        <div className="flex flex-wrap gap-2">
          <WarningButtons app={app} w={w} onEdit={() => onEdit(app)} onAddProxy={() => onAddProxy(app)} />
        </div>
      ),
    })),
    ...orphans.map((o) => ({
      tone: "warn" as const,
      title: `${o.name}: Orphaned App settings`,
      meta: `Name, icon ${o.icon} and group ${o.group} kept for ${o.was}, whose Proxy Host and Host Override are gone.`,
      body: <OrphanButtons orphan={o} apps={apps} />,
    })),
  ];
  return (
    <Panel
      title={`Warnings (${all.length - ignored + orphans.length})`}
      description="They count toward the Overview and the Apps badge, unless ignored."
      actions={ignored > 0 && <Checkbox label={`Show ignored (${ignored})`} checked={showIgnored} onChange={(e) => setShowIgnored(e.target.checked)} />}
    >
      {items.length ? <StatusList items={items} /> : <p className="text-sm text-muted-foreground">Nothing but ignored Warnings.</p>}
    </Panel>
  );
}

export function OrphanButtons({ orphan, apps }: { orphan: ReturnType<typeof useStore>["orphans"][number]; apps: PApp[] }) {
  const [to, setTo] = useState("");
  return (
    <div className="flex flex-wrap items-center gap-2">
      <select aria-label="Re-attach to" className="h-7 rounded-ui-sm border border-border bg-card px-2 text-xs" value={to} onChange={(e) => setTo(e.target.value)}>
        <option value="">Re-attach to…</option>
        {apps.map((a) => (
          <option key={a.id} value={a.id}>
            {a.name}
          </option>
        ))}
      </select>
      <Button
        size="sm"
        variant="outline"
        disabled={!to}
        onClick={() => {
          store.log(`ddash: re-attach ${orphan.name}'s settings to ${apps.find((a) => a.id === to)?.name}`);
          store.update(to, (a) => ({ ...a, name: orphan.name, icon: orphan.icon, group: orphan.group }));
          store.set((s) => ({ ...s, orphans: s.orphans.filter((o) => o !== orphan) }));
        }}
      >
        Re-attach
      </Button>
      <Button
        size="sm"
        variant="danger"
        onClick={() => {
          store.log(`ddash: delete the settings kept for ${orphan.was}`);
          store.set((s) => ({ ...s, orphans: s.orphans.filter((o) => o !== orphan) }));
        }}
      >
        Delete settings
      </Button>
    </div>
  );
}

function Cards({ apps, onEdit }: { apps: PApp[]; onEdit: (a: PApp) => void }) {
  const keep = useKeep();
  return (
    <ul className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
      {apps.map((a) => {
        const warns = a.warnings.filter((w) => !w.ignored).length;
        return (
          <li key={a.id} aria-label={a.name} className="min-w-0">
            <Panel>
              <div className="flex flex-col gap-3">
                <div className="flex items-start gap-2">
                  <Link to={`${a.id}${keep}`} className="group flex min-w-0 flex-1 items-center gap-3">
                    <AppIcon name={a.name} icon={a.icon} url={a.url} />
                    <span className="flex min-w-0 flex-col">
                      <span className="truncate font-semibold group-hover:underline">{a.name}</span>
                      <span className="truncate text-xs text-muted-foreground">{a.url}</span>
                    </span>
                  </Link>
                  <a href={a.url} target="_blank" rel="noreferrer" aria-label={`Open ${a.name}`} className="rounded-ui-sm p-1.5 text-muted-foreground hover:bg-muted">
                    ↗
                  </a>
                  <Button variant="ghost" size="sm" icon="pencil" aria-label={`Edit ${a.name}`} onClick={() => onEdit(a)} />
                </div>
                <div className="flex flex-wrap gap-1.5">
                  <CheckPill app={a} />
                  <ReachPill reach={a.reach} />
                  {warns > 0 && (
                    <Pill tone="warn" dot>
                      {warns} {warns === 1 ? "Warning" : "Warnings"}
                    </Pill>
                  )}
                </div>
                {(a.workload || a.aliases.length > 0) && (
                  <div className="flex min-w-0 flex-col gap-1 text-xs text-muted-foreground">
                    {a.workload && <span className="truncate">{workloadText(a.workload)}</span>}
                    {a.aliases.length > 0 && <span className="truncate">Also {a.aliases.join(", ")}</span>}
                  </div>
                )}
              </div>
            </Panel>
          </li>
        );
      })}
    </ul>
  );
}

// ---- Detail route -------------------------------------------------------------

export function DetailA() {
  const { id = "" } = useParams();
  const { apps } = useStore();
  const keep = useKeep();
  const nav = useNavigate();
  const [editing, setEditing] = useState(false);
  const [proxying, setProxying] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const a = apps.find((x) => x.id === id);
  const back = { href: `..${keep}`, label: "Apps" };
  if (!a)
    return (
      <div>
        <PageTitle kicker="App" title={id} back={back} />
        <Panel>
          <EmptyState icon="search" title="No such App">
            It was deleted, or merged into another.
          </EmptyState>
        </Panel>
      </div>
    );
  return (
    <div>
      <PageTitle
        kicker={`App · ${reachLabel[a.reach]}`}
        title={
          <span className="flex items-center gap-3">
            <AppIcon name={a.name} icon={a.icon} url={a.url} size={40} />
            {a.name}
          </span>
        }
        subtitle={
          <a href={a.url} target="_blank" rel="noreferrer" className="underline">
            {a.url}
          </a>
        }
        back={back}
        actions={
          <>
            <Button variant="outline" icon="pencil" onClick={() => setEditing(true)}>
              Edit
            </Button>
            <Menu
              label={`More for ${a.name}`}
              items={[
                ...(a.reach === "not-proxied" && !a.proxy ? [{ label: "Add reverse proxy…", onSelect: () => setProxying(true) }] : []),
                { label: a.hidden ? "Unhide" : "Hide", onSelect: () => store.update(a.id, (x) => ({ ...x, hidden: !x.hidden })) },
                { label: "Delete…", danger: true, onSelect: () => setDeleting(true) },
              ]}
            />
          </>
        }
      />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {a.warnings.length > 0 && (
          <div className="lg:col-span-2">
            <Panel title="Warnings">
              <StatusList
                items={a.warnings.map((w) => ({
                  tone: w.ignored ? "idle" : "warn",
                  title: `${warningTitle[w.kind]}${w.ignored ? " (ignored)" : ""}`,
                  meta: w.detail,
                  body: (
                    <div className="flex flex-wrap gap-2">
                      <WarningButtons app={a} w={w} onEdit={() => setEditing(true)} onAddProxy={() => setProxying(true)} />
                    </div>
                  ),
                }))}
              />
            </Panel>
          </div>
        )}
        <Panel title="Status">
          <div className="flex flex-col gap-4">
            <Facts
              items={[
                { label: "Check", value: <CheckPill app={a} /> },
                { label: "Workload", value: a.workload ? workloadText(a.workload) : a.reach === "proxied" ? "none matched" : "none" },
                { label: "Group", value: a.group || "—" },
                { label: "Hidden", value: a.hidden ? "yes" : "no" },
              ]}
            />
            {a.check && a.check.bars.length > 0 && <UptimeBars label="Last 30 days" bars={a.check.bars.map((tone) => ({ tone }))} />}
          </div>
        </Panel>
        <Panel title="Reverse proxy" actions={a.reach === "not-proxied" && !a.proxy && <Button size="sm" variant="outline" onClick={() => setProxying(true)}>Add reverse proxy</Button>}>
          {a.proxy ? (
            <Facts
              items={[
                { label: "Proxy Host", value: `#${a.proxy.id} in Nginx Proxy Manager` },
                { label: "Upstream", value: `${a.proxy.scheme}://${a.proxy.host}:${a.proxy.port}`, mono: true },
                { label: "Certificate", value: certText(a.proxy) },
                {
                  label: "Options",
                  value: (
                    <span className="flex flex-wrap gap-1">
                      {flags(a.proxy).map((f) => (
                        <Pill key={f}>{f}</Pill>
                      ))}
                    </span>
                  ),
                },
              ]}
            />
          ) : (
            <p className="text-sm text-muted-foreground">
              {a.reach === "link-only" ? "A link only: ddash holds nothing in Nginx Proxy Manager or pfSense for it." : a.reach === "not-proxied" ? `Not proxied: reached at its own address, ${a.overrideIp}.` : "No Proxy Host serves it."}
            </p>
          )}
        </Panel>
        <Panel title="DNS" description="Registered when a hostname is a Host Override or one of its aliases.">
          {a.dns.length ? (
            <Facts
              items={a.dns.map((d, i) => ({
                label: d.host + (a.dns.findIndex((x) => x.host === d.host) !== i ? " (again)" : ""),
                value: (
                  <span className="flex items-center gap-2">
                    <Pill tone={d.how === "none" ? "warn" : "up"} dot>
                      {d.how === "none" ? "not registered" : "registered"}
                    </Pill>
                    <span className="text-xs text-muted-foreground">{d.how === "none" ? "" : dnsText(d)}</span>
                  </span>
                ),
              }))}
            />
          ) : (
            <p className="text-sm text-muted-foreground">{hostOf(a.url)} isn't in pfSense; it's a link only.</p>
          )}
        </Panel>
        <Panel title="Network" description="Of the Workload's address. Changed on Network only.">
          <NetLine net={a.net} />
        </Panel>
      </div>
      {editing && <EditSheet app={a} onClose={() => setEditing(false)} />}
      {proxying && <AddProxyDialog app={a} onClose={() => setProxying(false)} />}
      {deleting && <DeleteDialog app={a} onClose={() => setDeleting(false)} onDeleted={() => nav(`..${keep}`)} />}
    </div>
  );
}

// ---- Edit Sheet -------------------------------------------------------------

function Heading({ children }: { children: string }) {
  return <h3 className="mt-2 border-b border-border pb-1 text-xs font-bold tracking-wide text-muted-foreground uppercase">{children}</h3>;
}

function EditSheet({ app, onClose }: { app: PApp; onClose: () => void }) {
  const { apps } = useStore();
  const [d, setD] = useState<EditDraft>(() => editDraft(app));
  const r = useRunner();
  const [result, setResult] = useState<"ok" | number>();
  const [deleting, setDeleting] = useState(false);
  const blocked = useProxyBlocked();
  const listId = useId();
  const formId = useId();
  const set = (p: Partial<EditDraft>) => setD({ ...d, ...p });
  const plan = planEdit(app, d);
  const urlChanged = hostOf(d.url) !== hostOf(app.url);
  const taken = urlChanged ? nameError(d.url, app.id) : undefined;
  const blockedHere = urlChanged && app.reach === "proxied" ? blocked : undefined;
  return (
    <Sheet
      wide
      title={`Edit ${app.name}`}
      kicker={reachLabel[app.reach]}
      onClose={onClose}
      footer={
        <FormActions
          status={
            !r.states && (
              <Button variant="danger" onClick={() => setDeleting(true)}>
                Delete…
              </Button>
            )
          }
        >
          {result !== undefined ? (
            <Button onClick={onClose}>Close</Button>
          ) : (
            <>
              <Button variant="ghost" onClick={onClose} disabled={r.running}>
                Cancel
              </Button>
              <Button type="submit" form={formId} disabled={r.running || !plan.length || Boolean(taken) || Boolean(blockedHere)}>
                Save{plan.length > 1 ? ` (${plan.length} steps)` : ""}
              </Button>
            </>
          )}
        </FormActions>
      }
    >
      {r.states ? (
        <div className="flex flex-col gap-4">
          <Steps steps={r.steps} states={r.states} />
          {typeof result === "number" && (
            <Notice tone="down" title={`Stopped at: ${r.steps[result].label}`}>
              Nothing was rolled back. What's half-done shows as Warnings on {app.name}.
            </Notice>
          )}
          {result === "ok" && <Notice tone="info">Saved.</Notice>}
        </div>
      ) : (
        <form
          id={formId}
          className="flex flex-col gap-4"
          onSubmit={async (e) => {
            e.preventDefault();
            const failed = await r.run(plan);
            setResult(failed ?? "ok");
            commitEdit(app, d, plan, failed);
          }}
        >
          <Heading>Display</Heading>
          <Field label="Name">
            <TextInput value={d.name} onChange={(e) => set({ name: e.target.value })} />
          </Field>
          <Field label="Icon">
            <AppIconPicker label="Icon" value={d.icon} onChange={(icon) => set({ icon })} name={d.name} url={d.url} />
          </Field>
          <Field label="Group">
            <TextInput list={listId} value={d.group} placeholder="None" onChange={(e) => set({ group: e.target.value })} />
          </Field>
          <Groups id={listId} apps={apps} />
          <Checkbox label="Hidden" checked={d.hidden} onChange={(e) => set({ hidden: e.target.checked })} />
          <Heading>Address</Heading>
          <Field label="URL" error={taken}>
            <TextInput value={d.url} onChange={(e) => set({ url: e.target.value })} />
          </Field>
          <Field label="Aliases" hint={app.reach === "proxied" ? "The Proxy Host's other domain names, each an alias of the Reverse Proxy's Host Override." : app.reach === "not-proxied" ? `Aliases of the Host Override ${hostOf(app.url)}.` : "Other names ddash matches this App by."}>
            <TextInput value={d.aliases} placeholder="none" onChange={(e) => set({ aliases: e.target.value })} />
          </Field>
          {blockedHere && <Notice tone="warn">Can't move a proxied App's URL now: {blockedHere}</Notice>}
          {app.proxy && d.ph && (
            <>
              <Heading>Reverse proxy</Heading>
              <ProxyFields value={d.ph} onChange={(ph) => set({ ph })} host={hostOf(d.url)} />
            </>
          )}
          <Heading>Workload</Heading>
          <WorkloadSelect app={app} value={d.workload} onChange={(workload) => set({ workload })} />
          {plan.length > 0 && (
            <div className="rounded-ui-sm border border-border p-3">
              <p className="mb-2 text-xs font-semibold">Saving will</p>
              <Steps steps={plan} />
            </div>
          )}
        </form>
      )}
      {deleting && <DeleteDialog app={app} onClose={() => setDeleting(false)} onDeleted={onClose} />}
    </Sheet>
  );
}


// ---- Add App Sheet --------------------------------------------------------------

function AddSheet({ onClose }: { onClose: () => void }) {
  const { apps } = useStore();
  const [d, setD] = useState<Draft>(emptyDraft);
  const r = useRunner();
  const [result, setResult] = useState<"ok" | number>();
  const blocked = useProxyBlocked();
  const listId = useId();
  const formId = useId();
  const host = hostOf(d.url);
  const set = (p: Partial<Draft>) => setD({ ...d, ...p });
  const taken = host ? nameError(d.url) : undefined;
  const plan = planAdd(d);
  const ok = d.url && !taken && (!d.proxy || (d.ph.host && !blocked)) && (d.proxy || d.dns === "link" || d.address);
  return (
    <Sheet
      wide
      title="Add App"
      onClose={onClose}
      footer={
        <FormActions status={!r.states && `${plan.length} steps`}>
          {result !== undefined ? (
            <Button onClick={onClose}>Close</Button>
          ) : (
            <>
              <Button variant="ghost" onClick={onClose} disabled={r.running}>
                Cancel
              </Button>
              <Button type="submit" form={formId} disabled={!ok || r.running}>
                Add App
              </Button>
            </>
          )}
        </FormActions>
      }
    >
      {r.states ? (
        <div className="flex flex-col gap-4">
          <Steps steps={r.steps} states={r.states} />
          {typeof result === "number" && (
            <Notice tone="down" title={`Stopped at: ${r.steps[result].label}`}>
              Nothing was rolled back: what was created before it shows as a Warning.
            </Notice>
          )}
        </div>
      ) : (
        <form
          id={formId}
          className="flex flex-col gap-4"
          onSubmit={async (e) => {
            e.preventDefault();
            const failed = await r.run(plan);
            setResult(failed ?? "ok");
            if (failed === undefined) store.add(appFromDraft(d));
          }}
        >
          <Field label="Name">
            <TextInput value={d.name} placeholder={host || "Jellyfin"} onChange={(e) => set({ name: e.target.value })} />
          </Field>
          <Field label="URL" error={taken}>
            <TextInput
              value={d.url}
              placeholder="https://jellyfin.home.example"
              onChange={(e) => {
                const url = e.target.value;
                const h = hostOf(url);
                // The certificate follows the hostname until picked; the DNS choice follows the home domain.
                setD({ ...d, url, dns: inHome(h) ? "override" : "link", ph: { ...d.ph, certId: coveringCert(h), forceSsl: coveringCert(h) > 0, http2: coveringCert(h) > 0 } });
              }}
            />
          </Field>
          <div className="grid grid-cols-2 gap-4">
            <Field label="Icon">
              <AppIconPicker label="Icon" value={d.icon} onChange={(icon) => set({ icon })} name={d.name || host} url={d.url} />
            </Field>
            <Field label="Group">
              <TextInput list={listId} value={d.group} placeholder="None" onChange={(e) => set({ group: e.target.value })} />
            </Field>
          </div>
          <Groups id={listId} apps={apps} />
          <div className="flex items-center gap-3 rounded-ui-sm border border-border p-3">
            <Switch checked={d.proxy} onChange={(proxy) => set({ proxy })} label="Reverse proxy" />
            <div className="flex flex-col">
              <span className="text-sm font-semibold">Reverse proxy</span>
              <span className="text-xs text-muted-foreground">A Proxy Host in Nginx Proxy Manager, and {host || "the hostname"} as an alias of the Reverse Proxy.</span>
            </div>
          </div>
          {d.proxy ? (
            <>
              {blocked && <Notice tone="warn">Proxied Apps can't be created now: {blocked}</Notice>}
              <ProxyFields value={d.ph} onChange={(ph) => set({ ph })} host={host} />
            </>
          ) : (
            <>
              <Field label="DNS" hint={d.dns === "override" ? "pfSense resolves the hostname to the address." : "ddash only keeps the link; nothing changes in pfSense."}>
                <Segmented label="DNS" value={d.dns === "override" ? "Create Host Override" : "Link only"} options={["Create Host Override", "Link only"]} onChange={(v) => set({ dns: v === "Link only" ? "link" : "override" })} />
              </Field>
              {d.dns === "override" && (
                <Field label="Address">
                  <TextInput value={d.address} placeholder="198.18.1.10" onChange={(e) => set({ address: e.target.value })} />
                </Field>
              )}
            </>
          )}
          <div className="rounded-ui-sm border border-border p-3">
            <p className="mb-2 text-xs font-semibold">Adding will</p>
            <Steps steps={plan} />
          </div>
        </form>
      )}
    </Sheet>
  );
}

