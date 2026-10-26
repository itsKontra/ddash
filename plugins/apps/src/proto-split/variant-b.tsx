// PROTOTYPE (wayfinder #139), variant B "Table + detail Sheet": one dense
// table with Tabs by reach (and a Warnings tab), an App's detail as a wide
// Sheet with its own Tabs that flips into edit mode in place, and Add App as
// a stepped Dialog whose second step is three tiles (Reverse proxy / Host
// Override / Link only) instead of a Switch plus a choice.
import {
  ActionStrip,
  AppIcon,
  AppIconPicker,
  Button,
  Checkbox,
  DataTable,
  Dialog,
  Facts,
  Field,
  FormActions,
  matches,
  Notice,
  PageTitle,
  Panel,
  Pill,
  SearchField,
  Select,
  Sheet,
  StatusDot,
  StatusList,
  Tabs,
  TextInput,
  UptimeBars,
  type Column,
} from "@ddash/ui";
import { useId, useState } from "react";
import { coveringCert, hostOf, reverseProxy, store, useStore, type PApp, type Warning } from "./data";
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
  groupsOf,
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
  useProxyBlocked,
  useRunner,
  warningTitle,
  WarningButtons,
  WorkloadSelect,
  workloadText,
  type Draft,
  type EditDraft,
} from "./shared";
import { OrphanButtons } from "./variant-a";

type View = "all" | "proxied" | "not-proxied" | "link-only" | "warnings" | "hidden";

export function VariantB() {
  const { apps, orphans } = useStore();
  const [view, setView] = useState<View>("all");
  const [q, setQ] = useState("");
  const [group, setGroup] = useState("");
  const [open, setOpen] = useState<{ id: string; tab?: DetailTab; edit?: boolean }>();
  const [adding, setAdding] = useState(false);
  const [proxying, setProxying] = useState<PApp>();
  const [showIgnored, setShowIgnored] = useState(false);
  const shown = apps.filter((a) => matches(q, a.name, a.url, ...a.aliases, a.group) && (!group || a.group === group));
  const visible = shown.filter((a) => !a.hidden);
  const warnRows = apps
    .filter((a) => !a.hidden)
    .flatMap((a) => a.warnings.map((w) => ({ app: a, w })))
    .filter((x) => showIgnored || !x.w.ignored);
  const counting = apps.filter((a) => !a.hidden).flatMap((a) => a.warnings).filter((w) => !w.ignored).length + orphans.length;
  const ignored = apps.filter((a) => !a.hidden).flatMap((a) => a.warnings).filter((w) => w.ignored).length;
  const rows = view === "hidden" ? shown.filter((a) => a.hidden) : view === "all" ? visible : visible.filter((a) => a.reach === view);
  const detail = open && apps.find((a) => a.id === open.id);
  const count = (r: PApp["reach"]) => visible.filter((a) => a.reach === r).length;

  return (
    <div>
      <PageTitle
        kicker="Services"
        title="Apps"
        subtitle="Every Proxy Host and Host Override, plus the links you add."
        actions={
          <Button icon="plus" onClick={() => setAdding(true)}>
            Add App
          </Button>
        }
      />
      <div className="flex flex-col gap-6">
        <ScenarioNotices />
        <Panel
          flush
          toolbar={
            <>
              <Tabs
                label="View"
                value={view}
                onChange={setView}
                options={[
                  { id: "all", label: "All", count: visible.length },
                  { id: "proxied", label: "Proxied", count: count("proxied") },
                  { id: "not-proxied", label: "Not proxied", count: count("not-proxied") },
                  { id: "link-only", label: "Link only", count: count("link-only") },
                  { id: "warnings", label: "Warnings", count: counting },
                  { id: "hidden", label: "Hidden", count: apps.filter((a) => a.hidden).length },
                ]}
              />
              <div className="flex flex-1 flex-wrap items-center justify-end gap-2">
                {view === "warnings" ? (
                  ignored > 0 && <Checkbox label={`Show ignored (${ignored})`} checked={showIgnored} onChange={(e) => setShowIgnored(e.target.checked)} />
                ) : (
                  <>
                    <div className="w-44">
                      <Select label="Group" value={group} onChange={(e) => setGroup(e.target.value)}>
                        <option value="">All groups</option>
                        {groupsOf(apps).map((g) => (
                          <option key={g}>{g}</option>
                        ))}
                      </Select>
                    </div>
                    <div className="w-60">
                      <SearchField label="Search Apps" value={q} onChange={setQ} placeholder="Name, domain or group" />
                    </div>
                  </>
                )}
              </div>
            </>
          }
        >
          {view === "warnings" ? (
            <>
              <DataTable
                rows={warnRows}
                rowKey={(x) => `${x.app.id} ${x.w.kind}`}
                columns={warningColumns}
                empty="No Warnings."
                rowLabel={(x) => `${x.app.name}: ${warningTitle[x.w.kind]}`}
                strip={(x) => (
                  <ActionStrip summary={x.w.detail}>
                    <WarningButtons app={x.app} w={x.w} onEdit={() => setOpen({ id: x.app.id, edit: true })} onAddProxy={() => setProxying(x.app)} />
                  </ActionStrip>
                )}
              />
              {orphans.map((o) => (
                <div key={o.id} className="flex flex-wrap items-center gap-3 border-t border-border px-5 py-3 text-sm">
                  <Pill tone="warn" dot>
                    Orphaned App settings
                  </Pill>
                  <span>
                    <span className="font-semibold">{o.name}</span> <span className="text-muted-foreground">kept for {o.was}</span>
                  </span>
                  <span className="ml-auto">
                    <OrphanButtons orphan={o} apps={apps} />
                  </span>
                </div>
              ))}
            </>
          ) : (
            <DataTable rows={rows} rowKey={(a) => a.id} columns={appColumns} onRowClick={(a) => setOpen({ id: a.id })} defaultSort="group" empty="No Apps here." />
          )}
        </Panel>
      </div>
      {detail && <DetailSheet key={detail.id} app={detail} tab={open?.tab} edit={open?.edit} onClose={() => setOpen(undefined)} onAddProxy={() => setProxying(detail)} />}
      {adding && <AddDialog onClose={() => setAdding(false)} />}
      {proxying && <AddProxyDialog app={proxying} onClose={() => setProxying(undefined)} />}
    </div>
  );
}

const appColumns: Column<PApp>[] = [
  {
    id: "name",
    header: "App",
    rowHeader: true,
    sortable: true,
    sortValue: (a) => a.name.toLowerCase(),
    cell: (a) => (
      <span className="flex items-center gap-2">
        <AppIcon name={a.name} icon={a.icon} url={a.url} size={22} />
        <span className="flex flex-col">
          <span className="flex items-center gap-1.5 font-semibold">
            {a.name}
            {a.warnings.some((w) => !w.ignored) && <StatusDot tone="warn" label={`${a.warnings.filter((w) => !w.ignored).length} Warnings`} />}
          </span>
          <span className="text-xs text-muted-foreground">{hostOf(a.url)}</span>
        </span>
      </span>
    ),
  },
  { id: "reach", header: "Reach", cell: (a) => <ReachPill reach={a.reach} />, sortable: true, sortValue: (a) => a.reach },
  { id: "check", header: "Check", cell: (a) => <CheckPill app={a} /> },
  { id: "workload", header: "Workload", hideBelow: "sm", cell: (a) => <span className="text-xs text-muted-foreground">{a.workload ? `${a.workload.kind} ${a.workload.name}` : "—"}</span> },
  {
    id: "cert",
    header: "TLS",
    hideBelow: "sm",
    cell: (a) => <span className="text-xs text-muted-foreground">{a.proxy ? (a.proxy.certId ? certText(a.proxy).split(" · ")[1] : "none") : a.url.startsWith("https") ? "upstream's own" : "none"}</span>,
  },
  {
    id: "dns",
    header: "DNS",
    hideBelow: "sm",
    cell: (a) =>
      a.reach === "link-only" ? (
        <span className="text-xs text-muted-foreground">—</span>
      ) : a.dns.every((d) => d.how !== "none") ? (
        <Pill tone="up">registered</Pill>
      ) : (
        <Pill tone="warn">not registered</Pill>
      ),
  },
  { id: "group", header: "Group", sortable: true, sortValue: (a) => a.group || "~", cell: (a) => a.group || <span className="text-muted-foreground">—</span> },
];

const warningColumns: Column<{ app: PApp; w: Warning }>[] = [
  {
    id: "app",
    header: "App",
    rowHeader: true,
    cell: ({ app }) => (
      <span className="flex items-center gap-2">
        <AppIcon name={app.name} icon={app.icon} url={app.url} size={22} />
        {app.name}
      </span>
    ),
  },
  {
    id: "kind",
    header: "Warning",
    cell: ({ w }) => (
      <span className="flex items-center gap-2">
        <Pill tone={w.ignored ? "idle" : "warn"} dot>
          {warningTitle[w.kind]}
        </Pill>
        {w.ignored && <span className="text-xs text-muted-foreground">ignored</span>}
      </span>
    ),
  },
  { id: "detail", header: "Detail", hideBelow: "sm", cell: ({ w }) => <span className="text-xs text-muted-foreground">{w.detail}</span> },
];

// ---- Detail Sheet -------------------------------------------------------------

type DetailTab = "overview" | "proxy" | "dns" | "network" | "warnings";

function DetailSheet({ app, tab: initialTab, edit: initialEdit, onClose, onAddProxy }: { app: PApp; tab?: DetailTab; edit?: boolean; onClose: () => void; onAddProxy: () => void }) {
  const { apps } = useStore();
  const [tab, setTab] = useState<DetailTab>(initialTab ?? (app.warnings.some((w) => !w.ignored) ? "warnings" : "overview"));
  const [editing, setEditing] = useState(Boolean(initialEdit));
  const [d, setD] = useState<EditDraft>(() => editDraft(app));
  const [reviewing, setReviewing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [result, setResult] = useState<"ok" | number>();
  const r = useRunner();
  const blocked = useProxyBlocked();
  const listId = useId();
  const set = (p: Partial<EditDraft>) => setD({ ...d, ...p });
  const plan = planEdit(app, d);
  const urlChanged = hostOf(d.url) !== hostOf(app.url);
  const taken = urlChanged ? nameError(d.url, app.id) : undefined;
  const blockedHere = urlChanged && app.reach === "proxied" ? blocked : undefined;
  const tabs = [
    { id: "overview" as const, label: "Overview" },
    { id: "proxy" as const, label: "Reverse proxy" },
    { id: "dns" as const, label: editing ? "Address" : "DNS" },
    ...(editing ? [] : [{ id: "network" as const, label: "Network" }]),
    ...(editing ? [] : [{ id: "warnings" as const, label: "Warnings", count: app.warnings.filter((w) => !w.ignored).length }]),
  ];
  const leaveEdit = () => {
    setEditing(false);
    setReviewing(false);
    setResult(undefined);
    r.reset();
    setD(editDraft(store.get().apps.find((a) => a.id === app.id) ?? app));
  };

  const footer = reviewing ? (
    <FormActions status={r.running ? "Running one step at a time…" : undefined}>
      {result !== undefined ? (
        <Button onClick={leaveEdit}>Done</Button>
      ) : (
        <>
          <Button variant="ghost" disabled={r.running} onClick={() => setReviewing(false)}>
            Back
          </Button>
          <Button
            disabled={r.running}
            onClick={async () => {
              const failed = await r.run(plan);
              setResult(failed ?? "ok");
              commitEdit(app, d, plan, failed);
            }}
          >
            Apply {plan.length} {plan.length === 1 ? "step" : "steps"}
          </Button>
        </>
      )}
    </FormActions>
  ) : editing ? (
    <FormActions status={plan.length ? `${plan.length} pending ${plan.length === 1 ? "change" : "changes"}` : "No changes"}>
      <Button variant="ghost" onClick={leaveEdit}>
        Cancel
      </Button>
      <Button disabled={!plan.length || Boolean(taken) || Boolean(blockedHere)} onClick={() => setReviewing(true)}>
        Review…
      </Button>
    </FormActions>
  ) : (
    <FormActions
      status={
        <Button variant="danger" size="sm" onClick={() => setDeleting(true)}>
          Delete…
        </Button>
      }
    >
      <a href={app.url} target="_blank" rel="noreferrer" className="inline-flex h-9 items-center rounded-ui border border-border px-3.5 text-sm hover:bg-muted">
        Open ↗
      </a>
      <Button icon="pencil" onClick={() => setEditing(true)}>
        Edit
      </Button>
    </FormActions>
  );

  return (
    <Sheet wide title={app.name} kicker={`${reachLabel[app.reach]}${editing ? " · editing" : ""}`} subtitle={app.url} onClose={onClose} footer={footer}>
      {reviewing ? (
        <div className="flex flex-col gap-4">
          <p className="text-sm text-muted-foreground">One step at a time; the first failure stops the rest. Nothing is rolled back.</p>
          <Steps steps={plan} states={r.states} />
          {typeof result === "number" && (
            <Notice tone="down" title={`Stopped at: ${plan[result].label}`}>
              What's half-done now shows as Warnings.
            </Notice>
          )}
        </div>
      ) : (
        <div className="flex flex-col gap-5">
          <Tabs label="Sections" value={tab} onChange={setTab} options={tabs} />
          {tab === "overview" &&
            (editing ? (
              <div className="flex flex-col gap-4">
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
                <WorkloadSelect app={app} value={d.workload} onChange={(workload) => set({ workload })} />
              </div>
            ) : (
              <div className="flex flex-col gap-4">
                <Facts
                  items={[
                    { label: "Check", value: <CheckPill app={app} /> },
                    { label: "Workload", value: workloadText(app.workload) },
                    { label: "Group", value: app.group || "—" },
                    { label: "Aliases", value: app.aliases.join(", ") || "—" },
                    { label: "Hidden", value: app.hidden ? "yes" : "no" },
                    { label: "App id", value: app.id, mono: true },
                  ]}
                />
                {app.check && app.check.bars.length > 0 && <UptimeBars label="Last 30 days" bars={app.check.bars.map((tone) => ({ tone }))} />}
              </div>
            ))}
          {tab === "proxy" &&
            (app.proxy ? (
              editing && d.ph ? (
                <ProxyFields value={d.ph} onChange={(ph) => set({ ph })} host={hostOf(d.url)} />
              ) : (
                <Facts
                  items={[
                    { label: "Proxy Host", value: `#${app.proxy.id}` },
                    { label: "Upstream", value: `${app.proxy.scheme}://${app.proxy.host}:${app.proxy.port}`, mono: true },
                    { label: "Certificate", value: certText(app.proxy) },
                    { label: "Options", value: flags(app.proxy).join(" · ") || "none" },
                  ]}
                />
              )
            ) : (
              <div className="flex flex-col items-start gap-3 text-sm text-muted-foreground">
                {app.reach === "not-proxied" ? `Reached at its own address, ${app.overrideIp}.` : "No Proxy Host."}
                {app.reach === "not-proxied" && !app.proxy && (
                  <Button variant="outline" onClick={onAddProxy}>
                    Add reverse proxy…
                  </Button>
                )}
              </div>
            ))}
          {tab === "dns" &&
            (editing ? (
              <div className="flex flex-col gap-4">
                <Field label="URL" error={taken} hint="Changing the hostname moves it in pfSense and Nginx Proxy Manager; you'll see the steps before they run.">
                  <TextInput value={d.url} onChange={(e) => set({ url: e.target.value })} />
                </Field>
                <Field label="Aliases">
                  <TextInput value={d.aliases} placeholder="none" onChange={(e) => set({ aliases: e.target.value })} />
                </Field>
                {blockedHere && <Notice tone="warn">{blockedHere}</Notice>}
              </div>
            ) : app.dns.length ? (
              <StatusList
                items={app.dns.map((x) => ({
                  tone: x.how === "none" ? "warn" : "up",
                  title: x.host,
                  meta: x.how === "none" ? "not registered: nothing resolves it" : dnsText(x),
                  aside: x.ip,
                }))}
              />
            ) : (
              <p className="text-sm text-muted-foreground">A link only; pfSense holds nothing for it.</p>
            ))}
          {tab === "network" && (
            <div className="flex flex-col gap-3">
              <NetLine net={app.net} />
              <p className="text-xs text-muted-foreground">Read-only here: the internet path and Ads Allowed change on Network.</p>
            </div>
          )}
          {tab === "warnings" &&
            (app.warnings.length ? (
              <StatusList
                items={app.warnings.map((w) => ({
                  tone: w.ignored ? "idle" : "warn",
                  title: warningTitle[w.kind] + (w.ignored ? " (ignored)" : ""),
                  meta: w.detail,
                  body: (
                    <div className="flex flex-wrap gap-2">
                      <WarningButtons
                        app={app}
                        w={w}
                        onEdit={() => {
                          setEditing(true);
                          setTab(w.kind === "no-tls" ? "proxy" : "overview");
                        }}
                        onAddProxy={onAddProxy}
                      />
                    </div>
                  ),
                }))}
              />
            ) : (
              <p className="text-sm text-muted-foreground">No Warnings.</p>
            ))}
        </div>
      )}
      {deleting && <DeleteDialog app={app} onClose={() => setDeleting(false)} onDeleted={onClose} />}
    </Sheet>
  );
}

// ---- Add App: a stepped Dialog ------------------------------------------------

type AddStep = "app" | "reach" | "details" | "review";
type Way = "proxy" | "override" | "link";

function AddDialog({ onClose }: { onClose: () => void }) {
  const { apps } = useStore();
  const [step, setStep] = useState<AddStep>("app");
  const [d, setD] = useState<Draft>(emptyDraft);
  const [way, setWay] = useState<Way>("proxy");
  const [result, setResult] = useState<"ok" | number>();
  const r = useRunner();
  const blocked = useProxyBlocked();
  const listId = useId();
  const host = hostOf(d.url);
  const taken = host ? nameError(d.url) : undefined;
  const draft: Draft = { ...d, proxy: way === "proxy", dns: way === "link" ? "link" : "override" };
  const plan = planAdd(draft);
  const set = (p: Partial<Draft>) => setD({ ...d, ...p });
  const order: AddStep[] = way === "link" ? ["app", "reach", "review"] : ["app", "reach", "details", "review"];
  const i = order.indexOf(step);
  const canNext =
    step === "app" ? Boolean(d.url && !taken) : step === "reach" ? !(way === "proxy" && blocked) : step === "details" ? (way === "proxy" ? Boolean(d.ph.host) : Boolean(d.address)) : true;
  const tiles: { id: Way; title: string; body: string }[] = [
    { id: "proxy", title: "Behind the Reverse Proxy", body: `A Proxy Host in Nginx Proxy Manager; ${host || "the hostname"} becomes an alias of ${reverseProxy.host}.` },
    { id: "override", title: "Its own Host Override", body: `pfSense resolves ${host || "the hostname"} straight to the App's address. No TLS from ddash.` },
    { id: "link", title: "Link only", body: "ddash keeps the link and its Check. Nothing changes in pfSense or Nginx Proxy Manager." },
  ];
  return (
    <Dialog
      title="Add App"
      kicker={`Step ${i + 1} of ${order.length}`}
      onClose={onClose}
      footer={
        <FormActions status={r.running ? "Running…" : undefined}>
          {result !== undefined ? (
            <Button onClick={onClose}>Close</Button>
          ) : (
            <>
              {i > 0 && (
                <Button variant="ghost" disabled={r.running} onClick={() => setStep(order[i - 1])}>
                  Back
                </Button>
              )}
              {step === "review" ? (
                <Button
                  disabled={r.running}
                  onClick={async () => {
                    const failed = await r.run(plan);
                    setResult(failed ?? "ok");
                    if (failed === undefined) store.add(appFromDraft(draft));
                  }}
                >
                  Add App
                </Button>
              ) : (
                <Button disabled={!canNext} onClick={() => setStep(order[i + 1])}>
                  Next
                </Button>
              )}
            </>
          )}
        </FormActions>
      }
    >
      {step === "app" && (
        <div className="flex flex-col gap-4">
          <Field label="URL" error={taken}>
            <TextInput
              autoFocus
              value={d.url}
              placeholder="https://jellyfin.home.example"
              onChange={(e) => {
                const h = hostOf(e.target.value);
                setWay(inHome(h) ? (way === "link" ? "proxy" : way) : "link");
                setD({ ...d, url: e.target.value, ph: { ...d.ph, certId: coveringCert(h), forceSsl: coveringCert(h) > 0, http2: coveringCert(h) > 0 } });
              }}
            />
          </Field>
          <Field label="Name">
            <TextInput value={d.name} placeholder={host || "Jellyfin"} onChange={(e) => set({ name: e.target.value })} />
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
        </div>
      )}
      {step === "reach" && (
        <div role="radiogroup" aria-label="How it's reached" className="flex flex-col gap-2">
          {tiles.map((t) => (
            <button
              key={t.id}
              type="button"
              role="radio"
              aria-checked={way === t.id}
              onClick={() => setWay(t.id)}
              className={`flex cursor-pointer flex-col items-start gap-1 rounded-ui border p-4 text-left ${way === t.id ? "border-primary bg-primary-tint" : "border-border hover:bg-muted"}`}
            >
              <span className="font-semibold">
                {t.title}
                {t.id === "proxy" && " (default)"}
              </span>
              <span className="text-xs text-muted-foreground">{t.body}</span>
            </button>
          ))}
          {way === "proxy" && blocked && <Notice tone="warn">{blocked}</Notice>}
        </div>
      )}
      {step === "details" &&
        (way === "proxy" ? (
          <ProxyFields value={d.ph} onChange={(ph) => set({ ph })} host={host} />
        ) : (
          <Field label="Address" hint={`The Host Override ${host} → this address.`}>
            <TextInput value={d.address} placeholder="198.18.1.10" onChange={(e) => set({ address: e.target.value })} />
          </Field>
        ))}
      {step === "review" && (
        <div className="flex flex-col gap-4">
          <Facts
            items={[
              { label: "Name", value: d.name || host },
              { label: "URL", value: d.url, mono: true },
              { label: "Reach", value: tiles.find((t) => t.id === way)!.title },
            ]}
          />
          <Steps steps={plan} states={r.states} />
          {typeof result === "number" && (
            <Notice tone="down" title={`Stopped at: ${plan[result].label}`}>
              Nothing rolled back; what was created shows as a Warning.
            </Notice>
          )}
        </div>
      )}
    </Dialog>
  );
}
