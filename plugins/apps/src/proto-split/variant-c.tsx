// PROTOTYPE (wayfinder #139), variant C "Split view": a compact list on the
// left (a pinned "Needs attention" group, then the groups, Hidden folded at
// the bottom) and the selected App on the right (?app=), each section
// editable in place with its own Save; no Edit Sheet. Warnings live on the
// App they're about, not in a section of their own. Add App takes over the
// right pane, with a three-way Reach picker and a live "ddash will" plan
// beside the fields.
import {
  AppIcon,
  AppIconPicker,
  Button,
  Checkbox,
  Facts,
  Field,
  FormActions,
  matches,
  Notice,
  Panel,
  PageTitle,
  Pill,
  SearchField,
  Segmented,
  StatusDot,
  TextInput,
  UptimeBars,
  useQueryState,
  cx,
} from "@ddash/ui";
import { useId, useState, type ReactNode } from "react";
import { coveringCert, hostOf, reverseProxy, store, useStore, type PApp } from "./data";
import { AddProxyDialog, DeleteDialog, RunDialog } from "./flows";
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

export function VariantC() {
  const { apps, orphans } = useStore();
  const [sel, setSel] = useQueryState("app", "");
  const [q, setQ] = useState("");
  const [showIgnored, setShowIgnored] = useState(false);
  const [foldHidden, setFoldHidden] = useState(true);
  const shown = apps.filter((a) => matches(q, a.name, a.url, ...a.aliases, a.group));
  const attention = shown.filter((a) => !a.hidden && a.warnings.some((w) => showIgnored || !w.ignored));
  const groups = [...new Set(shown.filter((a) => !a.hidden).map((a) => a.group))].sort((a, b) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)));
  const hidden = shown.filter((a) => a.hidden);
  const ignored = apps.filter((a) => !a.hidden).flatMap((a) => a.warnings).filter((w) => w.ignored).length;
  const app = apps.find((a) => a.id === sel);

  return (
    <div>
      <PageTitle
        kicker="Services"
        title="Apps"
        actions={
          <Button icon="plus" onClick={() => setSel("new")}>
            Add App
          </Button>
        }
      />
      <div className="mb-4">
        <ScenarioNotices />
      </div>
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[20rem_minmax(0,1fr)]">
        <Panel flush>
          <div className="flex flex-col gap-2 border-b border-border p-3">
            <SearchField label="Search Apps" value={q} onChange={setQ} placeholder="Search" />
            {ignored > 0 && <Checkbox label={`Show ignored Warnings (${ignored})`} checked={showIgnored} onChange={(e) => setShowIgnored(e.target.checked)} />}
          </div>
          <nav aria-label="Apps" className="max-h-[70vh] overflow-y-auto py-2">
            {(attention.length > 0 || orphans.length > 0) && (
              <Group title="Needs attention" tone="warn">
                {attention.map((a) => (
                  <Item key={a.id} app={a} selected={a.id === sel} onSelect={() => setSel(a.id)} note={warningTitle[a.warnings.find((w) => showIgnored || !w.ignored)!.kind]} />
                ))}
                {orphans.map((o) => (
                  <button key={o.id} type="button" onClick={() => setSel(`orphan:${o.id}`)} className={cx("flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm hover:bg-muted", sel === `orphan:${o.id}` && "bg-primary-tint")}>
                    <StatusDot tone="warn" />
                    <span className="flex min-w-0 flex-col">
                      <span className="truncate font-medium">{o.name}</span>
                      <span className="truncate text-xs text-muted-foreground">Orphaned App settings</span>
                    </span>
                  </button>
                ))}
              </Group>
            )}
            {groups.map((g) => (
              <Group key={g} title={g || "Ungrouped"}>
                {shown
                  .filter((a) => !a.hidden && a.group === g)
                  .map((a) => (
                    <Item key={a.id} app={a} selected={a.id === sel} onSelect={() => setSel(a.id)} />
                  ))}
              </Group>
            ))}
            {hidden.length > 0 && (
              <Group
                title={`Hidden (${hidden.length})`}
                action={
                  <button type="button" className="text-xs text-muted-foreground hover:text-foreground" onClick={() => setFoldHidden(!foldHidden)}>
                    {foldHidden ? "show" : "fold"}
                  </button>
                }
              >
                {!foldHidden && hidden.map((a) => <Item key={a.id} app={a} selected={a.id === sel} onSelect={() => setSel(a.id)} />)}
              </Group>
            )}
          </nav>
        </Panel>
        <div className="min-w-0">
          {sel === "new" ? (
            <AddPane onDone={(id) => setSel(id ?? "")} />
          ) : sel.startsWith("orphan:") ? (
            <OrphanPane id={sel.slice(7)} apps={apps} />
          ) : app ? (
            <AppPane key={app.id} app={app} onGone={() => setSel("")} />
          ) : (
            <Panel>
              <p className="py-16 text-center text-sm text-muted-foreground">Pick an App on the left, or Add App.</p>
            </Panel>
          )}
        </div>
      </div>
    </div>
  );
}

function Group({ title, tone, action, children }: { title: string; tone?: "warn"; action?: ReactNode; children: ReactNode }) {
  return (
    <div className="mb-2">
      <div className={cx("flex items-center gap-2 px-3 pt-2 pb-1 text-[11px] font-bold tracking-wide uppercase", tone === "warn" ? "text-warn" : "text-muted-foreground")}>
        {title}
        <span className="ml-auto normal-case">{action}</span>
      </div>
      {children}
    </div>
  );
}

const reachGlyph: Record<PApp["reach"], string> = { proxied: "⇄", "not-proxied": "→", "link-only": "↗" };

function Item({ app, selected, onSelect, note }: { app: PApp; selected: boolean; onSelect: () => void; note?: string }) {
  return (
    <button type="button" onClick={onSelect} aria-current={selected || undefined} className={cx("flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm hover:bg-muted", selected && "bg-primary-tint")}>
      <StatusDot tone={app.check?.tone ?? "idle"} />
      <AppIcon name={app.name} icon={app.icon} url={app.url} size={20} />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate font-medium">{app.name}</span>
        {note && <span className="truncate text-xs text-warn">{note}</span>}
      </span>
      <span title={reachLabel[app.reach]} className="text-xs text-muted-foreground">
        {reachGlyph[app.reach]}
      </span>
    </button>
  );
}

// ---- The App pane: sections editable in place ---------------------------------

type Section = "display" | "address" | "proxy" | "workload";

function AppPane({ app, onGone }: { app: PApp; onGone: () => void }) {
  const [editing, setEditing] = useState<Section>();
  const [proxying, setProxying] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const warnings = app.warnings;
  const edit = (s: Section) => (
    <Button size="sm" variant="ghost" icon="pencil" aria-label={`Edit ${s}`} disabled={Boolean(editing) && editing !== s} onClick={() => setEditing(s)}>
      Edit
    </Button>
  );
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start gap-3">
        <AppIcon name={app.name} icon={app.icon} url={app.url} size={48} />
        <div className="min-w-0 flex-1">
          <h2 className="text-2xl font-extrabold tracking-tight">{app.name}</h2>
          <div className="mt-1 flex flex-wrap items-center gap-2">
            <a href={app.url} target="_blank" rel="noreferrer" className="text-sm underline">
              {app.url}
            </a>
            <ReachPill reach={app.reach} />
            <CheckPill app={app} />
            {app.hidden && <Pill>Hidden</Pill>}
          </div>
        </div>
        <Button variant="danger" size="sm" onClick={() => setDeleting(true)}>
          Delete…
        </Button>
      </div>
      {warnings.map((w) => (
        <div key={w.kind} className={cx("flex flex-col gap-2 rounded-ui border p-3", w.ignored ? "border-border opacity-70" : "border-warn bg-warn-tint")}>
          <div className="flex items-center gap-2 text-sm font-semibold">
            <StatusDot tone={w.ignored ? "idle" : "warn"} />
            {warningTitle[w.kind]}
            {w.ignored && <span className="text-xs font-normal text-muted-foreground">ignored</span>}
          </div>
          <p className="text-xs">{w.detail}</p>
          <div className="flex flex-wrap gap-2">
            <WarningButtons app={app} w={w} onEdit={() => setEditing(w.kind === "no-tls" ? "proxy" : "workload")} onAddProxy={() => setProxying(true)} />
          </div>
        </div>
      ))}
      <Panel title="Display" actions={!editing && edit("display")}>
        {editing === "display" ? (
          <InPlace app={app} fields={["name", "icon", "group", "hidden"]} onDone={() => setEditing(undefined)} />
        ) : (
          <Facts
            items={[
              { label: "Group", value: app.group || "—" },
              { label: "Icon", value: app.icon || "Automatic" },
            ]}
          />
        )}
      </Panel>
      <Panel title="Address and DNS" actions={!editing && edit("address")}>
        {editing === "address" ? (
          <InPlace app={app} fields={["url", "aliases"]} onDone={() => setEditing(undefined)} />
        ) : (
          <div className="flex flex-col gap-2">
            {app.dns.length ? (
              app.dns.map((d, i) => (
                <div key={i} className="flex flex-wrap items-center gap-2 text-sm">
                  <StatusDot tone={d.how === "none" ? "warn" : "up"} />
                  <span className="font-mono text-xs">{d.host}</span>
                  <span className="text-xs text-muted-foreground">{d.how === "none" ? "not registered" : dnsText(d)}</span>
                </div>
              ))
            ) : (
              <p className="text-sm text-muted-foreground">{hostOf(app.url)}: a link only, nothing in pfSense.</p>
            )}
          </div>
        )}
      </Panel>
      <Panel
        title="Reverse proxy"
        actions={!editing && (app.proxy ? edit("proxy") : app.reach === "not-proxied" && <Button size="sm" variant="outline" onClick={() => setProxying(true)}>Add reverse proxy…</Button>)}
      >
        {editing === "proxy" ? (
          <InPlace app={app} fields={["ph"]} onDone={() => setEditing(undefined)} />
        ) : app.proxy ? (
          <Facts
            items={[
              { label: "Upstream", value: `${app.proxy.scheme}://${app.proxy.host}:${app.proxy.port}`, mono: true },
              { label: "Certificate", value: certText(app.proxy) },
              { label: "Options", value: flags(app.proxy).join(" · ") || "none" },
              { label: "Proxy Host", value: `#${app.proxy.id} in Nginx Proxy Manager` },
            ]}
          />
        ) : (
          <p className="text-sm text-muted-foreground">{app.reach === "not-proxied" ? `Not proxied: pfSense resolves it to ${app.overrideIp}.` : app.reach === "link-only" ? "A link only." : "No Proxy Host serves it."}</p>
        )}
      </Panel>
      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <Panel title="Workload and Check" actions={!editing && edit("workload")}>
          {editing === "workload" ? (
            <InPlace app={app} fields={["workload"]} onDone={() => setEditing(undefined)} />
          ) : (
            <div className="flex flex-col gap-3">
              <p className="text-sm">{workloadText(app.workload)}</p>
              {app.check && app.check.bars.length > 0 && <UptimeBars label="Last 30 days" bars={app.check.bars.map((tone) => ({ tone }))} />}
            </div>
          )}
        </Panel>
        <Panel title="Network" description="Changed on Network only.">
          <NetLine net={app.net} />
        </Panel>
      </div>
      {proxying && <AddProxyDialog app={app} onClose={() => setProxying(false)} />}
      {deleting && <DeleteDialog app={app} onClose={() => setDeleting(false)} onDeleted={onGone} />}
    </div>
  );
}

type Fieldname = keyof EditDraft;

/** One section's form in place: its fields only, Save runs its plan (a URL change confirms in a Dialog first). */
function InPlace({ app, fields, onDone }: { app: PApp; fields: Fieldname[]; onDone: () => void }) {
  const { apps } = useStore();
  const [d, setD] = useState<EditDraft>(() => editDraft(app));
  const [moving, setMoving] = useState(false);
  const r = useRunner();
  const blocked = useProxyBlocked();
  const listId = useId();
  const set = (p: Partial<EditDraft>) => setD({ ...d, ...p });
  const plan = planEdit(app, d);
  const urlChanged = hostOf(d.url) !== hostOf(app.url);
  const taken = urlChanged ? nameError(d.url, app.id) : undefined;
  const has = (f: Fieldname) => fields.includes(f);
  const quiet = plan.every((s) => s.system === "ddash");
  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={async (e) => {
        e.preventDefault();
        if (!quiet) return setMoving(true);
        await r.run(plan);
        commitEdit(app, d, plan);
        onDone();
      }}
    >
      {has("name") && (
        <Field label="Name">
          <TextInput value={d.name} onChange={(e) => set({ name: e.target.value })} />
        </Field>
      )}
      {has("icon") && (
        <Field label="Icon">
          <AppIconPicker label="Icon" value={d.icon} onChange={(icon) => set({ icon })} name={d.name} url={d.url} />
        </Field>
      )}
      {has("group") && (
        <>
          <Field label="Group">
            <TextInput list={listId} value={d.group} placeholder="None" onChange={(e) => set({ group: e.target.value })} />
          </Field>
          <Groups id={listId} apps={apps} />
        </>
      )}
      {has("hidden") && <Checkbox label="Hidden" checked={d.hidden} onChange={(e) => set({ hidden: e.target.checked })} />}
      {has("url") && (
        <Field label="URL" error={taken}>
          <TextInput value={d.url} onChange={(e) => set({ url: e.target.value })} />
        </Field>
      )}
      {has("aliases") && (
        <Field label="Aliases" hint="Comma-separated.">
          <TextInput value={d.aliases} placeholder="none" onChange={(e) => set({ aliases: e.target.value })} />
        </Field>
      )}
      {has("ph") && d.ph && <ProxyFields value={d.ph} onChange={(ph) => set({ ph })} host={hostOf(d.url)} />}
      {has("workload") && <WorkloadSelect app={app} value={d.workload} onChange={(workload) => set({ workload })} />}
      <FormActions status={!quiet && plan.length ? `Changes pfSense or Nginx Proxy Manager: ${plan.length} steps` : undefined}>
        <Button variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={!plan.length || Boolean(taken) || r.running}>
          {quiet ? "Save" : "Save…"}
        </Button>
      </FormActions>
      {moving && (
        <RunDialog
          title={urlChanged ? `Move ${app.name} to ${hostOf(d.url)}` : `Save ${app.name}`}
          subtitle="One step at a time. The first failure stops the rest, and nothing is rolled back."
          plan={plan}
          go="Run"
          blocked={urlChanged && app.reach === "proxied" ? blocked : undefined}
          onClose={() => {
            setMoving(false);
            onDone();
          }}
          onDone={(failed) => commitEdit(app, d, plan, failed)}
        />
      )}
    </form>
  );
}

function OrphanPane({ id, apps }: { id: string; apps: PApp[] }) {
  const o = useStore().orphans.find((x) => x.id === id);
  if (!o) return <Panel>Gone.</Panel>;
  return (
    <Panel title={`${o.name}: Orphaned App settings`} description={`Kept for ${o.was}. Its Proxy Host and Host Override are gone; its Check history stays until these are deleted.`}>
      <div className="flex flex-col gap-4">
        <Facts
          items={[
            { label: "Name", value: o.name },
            { label: "Icon", value: o.icon },
            { label: "Group", value: o.group },
            { label: "App id", value: o.id, mono: true },
          ]}
        />
        <OrphanButtons orphan={o} apps={apps} />
      </div>
    </Panel>
  );
}

// ---- Add App in the pane ------------------------------------------------------

const ways = ["Reverse proxy", "Host Override", "Link only"] as const;
type Way = (typeof ways)[number];

function AddPane({ onDone }: { onDone: (id?: string) => void }) {
  const { apps } = useStore();
  const [d, setD] = useState<Draft>(emptyDraft);
  const [way, setWay] = useState<Way>("Reverse proxy");
  const [result, setResult] = useState<"ok" | number>();
  const r = useRunner();
  const blocked = useProxyBlocked();
  const listId = useId();
  const host = hostOf(d.url);
  const taken = host ? nameError(d.url) : undefined;
  const draft: Draft = { ...d, proxy: way === "Reverse proxy", dns: way === "Link only" ? "link" : "override" };
  const plan = planAdd(draft);
  const set = (p: Partial<Draft>) => setD({ ...d, ...p });
  const ok = d.url && !taken && (way === "Reverse proxy" ? d.ph.host && !blocked : way === "Host Override" ? d.address : true);
  return (
    <div className="grid grid-cols-1 gap-4 xl:grid-cols-[minmax(0,1fr)_18rem]">
      <Panel title="Add App">
        <form
          className="flex flex-col gap-4"
          onSubmit={async (e) => {
            e.preventDefault();
            const failed = await r.run(plan);
            setResult(failed ?? "ok");
            if (failed === undefined) {
              const a = appFromDraft(draft);
              store.add(a);
              onDone(a.id);
            }
          }}
        >
          <Field label="URL" error={taken}>
            <TextInput
              autoFocus
              value={d.url}
              placeholder="https://jellyfin.home.example"
              onChange={(e) => {
                const h = hostOf(e.target.value);
                if (!inHome(h)) setWay("Link only");
                setD({ ...d, url: e.target.value, ph: { ...d.ph, certId: coveringCert(h), forceSsl: coveringCert(h) > 0, http2: coveringCert(h) > 0 } });
              }}
            />
          </Field>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <Field label="Name">
              <TextInput value={d.name} placeholder={host || "Jellyfin"} onChange={(e) => set({ name: e.target.value })} />
            </Field>
            <Field label="Icon">
              <AppIconPicker label="Icon" value={d.icon} onChange={(icon) => set({ icon })} name={d.name || host} url={d.url} />
            </Field>
            <Field label="Group">
              <TextInput list={listId} value={d.group} placeholder="None" onChange={(e) => set({ group: e.target.value })} />
            </Field>
          </div>
          <Groups id={listId} apps={apps} />
          <Field
            label="Reach"
            hint={
              way === "Reverse proxy"
                ? `A Proxy Host, and ${host || "the hostname"} as an alias of ${reverseProxy.host}.`
                : way === "Host Override"
                  ? "pfSense resolves the hostname straight to an address."
                  : "ddash keeps the link and its Check only."
            }
          >
            <Segmented label="Reach" value={way} options={ways} onChange={setWay} />
          </Field>
          {way === "Reverse proxy" && (blocked ? <Notice tone="warn">{blocked}</Notice> : <ProxyFields value={d.ph} onChange={(ph) => set({ ph })} host={host} />)}
          {way === "Host Override" && (
            <Field label="Address">
              <TextInput value={d.address} placeholder="198.18.1.10" onChange={(e) => set({ address: e.target.value })} />
            </Field>
          )}
          <FormActions>
            <Button variant="ghost" onClick={() => onDone()} disabled={r.running}>
              Cancel
            </Button>
            <Button type="submit" disabled={!ok || r.running}>
              Add App
            </Button>
          </FormActions>
        </form>
      </Panel>
      <Panel title="ddash will" description="Updates as you type.">
        <div className="flex flex-col gap-3">
          <Steps steps={plan} states={r.states} />
          {typeof result === "number" && <Notice tone="down">Stopped at step {result + 1}; nothing rolled back.</Notice>}
        </div>
      </Panel>
    </div>
  );
}
