import { PlanSteps, usePluginLink, useQuery } from "@ddash/plugin";
import { AppsService, Reach, System, type ListAppsResponse, type Plan } from "@ddash/proto/apps/apps_pb";
import { Button, Field, FormActions, Notice, Panel, Segmented, TextInput } from "@ddash/ui";
import { useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { groupsOf } from "./apps";
import { ComposeFields, composeSource, newCompose, type ComposeDraft } from "./compose-fields";
import type { Draft } from "./draft";
import { GroupField, IconField, Text } from "./fields";
import { blocking, NameNotes, steps, useRunAction, type ActionInit } from "./plan";
import { newProxy, ProxyFields, resolve, useCertificates, type ProxyDraft } from "./proxy-fields";

/** How long typing must pause before "ddash will" asks for the Plan again. */
export const planDelay = 300;

const reaches = ["Reverse proxy", "Host Override", "Link only"] as const;
type Way = (typeof reaches)[number];

const reachOf: Record<Way, Reach> = {
  "Reverse proxy": Reach.PROXIED,
  "Host Override": Reach.NOT_PROXIED,
  "Link only": Reach.LINK_ONLY,
};

/** Where the App runs: an upstream that is already there, or a Compose Project ddash starts. */
const sources = ["Existing upstream", "New Compose Project"] as const;
type Source = (typeof sources)[number];

const cantCheck = (source: string) => `Can't check ${source} right now: its last read failed.`;

/** A URL's hostname, canonical; "" for none. */
const hostOf = (url: string) => (URL.parse(url.trim())?.hostname ?? "").toLowerCase().replace(/\.$/, "");

/** The home domain: the Reverse Proxy's Host Override's, while there is exactly one. */
function homeDomain(list: ListAppsResponse): string | undefined {
  const rp = list.reverseProxy?.hostOverrides ?? [];
  if (rp.length !== 1) return undefined;
  const dot = rp[0].indexOf(".");
  return dot > 0 ? rp[0].slice(dot + 1) : undefined;
}

/** The Reaches that write to a source whose last read failed, and why. */
function unreadable(list: ListAppsResponse): { disabled: Way[]; why: string[] } {
  const npm = !!list.proxyHosts?.error || !list.proxyHosts?.readAt;
  const pf = !!list.hostOverrides?.error || !list.hostOverrides?.readAt;
  const disabled: Way[] = [];
  const why: string[] = [];
  if (npm || pf) disabled.push("Reverse proxy");
  if (pf) disabled.push("Host Override");
  if (npm) why.push(cantCheck("Nginx Proxy Manager"));
  if (pf) why.push(cantCheck("pfSense"));
  return { disabled, why };
}

/** value, once it stopped changing for planDelay; bigints compare as strings. */
function useSettled<T>(value: T): { settled: T; current: boolean } {
  const key = JSON.stringify(value, (_, v: unknown) => (typeof v === "bigint" ? v.toString() : v));
  const [settled, setSettled] = useState({ key, value });
  useEffect(() => {
    if (key === settled.key) return;
    const t = setTimeout(() => setSettled({ key, value }), planDelay);
    return () => clearTimeout(t);
  }, [key, settled.key, value]);
  return { settled: settled.value, current: key === settled.key };
}

/** The upstream a Plan's Proxy Host step writes, e.g. "http://198.18.2.10:8010"; "" for none. */
const planUpstream = (plan?: Plan) =>
  plan?.steps.find((s) => s.system === System.NGINX_PROXY_MANAGER)?.values.find((v) => v.label === "Upstream")?.after ?? "";

/** A draft's Compose Project as the form's. */
const composeOf = (d: Draft): ComposeDraft => ({
  ...newCompose(),
  host: d.host ?? "",
  name: d.name,
  directory: d.directory ?? "",
  content: d.compose,
  env: d.env ?? "",
  service: d.app?.service ?? "",
  port: d.app?.port ? String(d.app.port) : "",
});

/**
 * Add App in the pane: the URL, Name, Icon and Group, the source (an
 * existing upstream, or a new Compose Project ddash writes and starts on
 * a Docker host first), the Reach (the Proxy Host's fields for Reverse
 * proxy, the Address for Host Override), and beside them "ddash will":
 * the server's Plan for what is typed, asked for once typing pauses. Add
 * runs the Plan on screen and shows each step's progress; onDone gets the
 * new App's id, or none on Cancel. A draft (from the AI App helper) fills
 * in a new Compose Project and the fields; nothing runs until Add.
 */
export function AddApp({
  list,
  onDone,
  draft,
  fromCompose: startFromCompose = !!draft,
}: {
  list: ListAppsResponse;
  onDone: (id?: string) => void;
  draft?: Draft;
  /** Start with the New Compose Project source. */
  fromCompose?: boolean;
}) {
  const certificates = useCertificates();
  const { disabled, why } = unreadable(list);
  const [url, setUrl] = useState(draft?.app?.url ?? "");
  const [name, setName] = useState(draft?.app?.name ?? "");
  const [icon, setIcon] = useState(draft?.app?.icon ?? "");
  const [group, setGroup] = useState(draft?.app?.group ?? "");
  const [source, setSource] = useState<Source>(startFromCompose ? "New Compose Project" : "Existing upstream");
  const [compose, setCompose] = useState<ComposeDraft>(() => (draft ? composeOf(draft) : newCompose()));
  const [chosen, setChosen] = useState<Way>(() => {
    const h = draft?.app?.url ? hostOf(draft.app.url) : "";
    const home = homeDomain(list);
    return h && home && h !== home && !h.endsWith(`.${home}`) ? "Link only" : "Reverse proxy";
  });
  const [proxy, setProxy] = useState<ProxyDraft>(newProxy);
  const [address, setAddress] = useState("");
  const drafting = usePluginLink("ai-helper");
  const firstOpen = reaches.find((r) => !disabled.includes(r))!;
  const way = disabled.includes(chosen) ? firstOpen : chosen;
  const host = hostOf(url);
  const fromCompose = source === "New Compose Project";

  const proxyHost = resolve(proxy, certificates, host);
  const action: ActionInit = {
    action: {
      case: "add",
      value: {
        url: url.trim(),
        name,
        icon,
        group,
        reach: reachOf[way],
        // A new Compose Project's upstream is filled in by ddash.
        proxyHost: way === "Reverse proxy" ? (fromCompose ? { ...proxyHost, forwardHost: "", forwardPort: 0 } : proxyHost) : undefined,
        address: way === "Host Override" ? address.trim() : "",
        compose: fromCompose ? composeSource(compose) : undefined,
      },
    },
  };
  const { settled, current } = useSettled(action);
  const planQuery = useQuery(AppsService.method.planAction, { action: settled });
  const run = useRunAction();
  /** set, forgetting the last run: the Action changed. */
  const edit =
    <T,>(set: (v: T) => void) =>
    (v: T) => {
      run.reset();
      set(v);
    };

  const plan = run.plan ?? planQuery.data?.plan;
  const typed = url.trim() !== "";
  const blocked = plan ? blocking(plan) : [];
  const outcome = run.outcome;
  const canAdd = typed && current && !!plan && blocked.length === 0 && !run.pending && outcome?.kind !== "done";

  const typeUrl = (v: string) => {
    run.reset();
    setUrl(v);
    const h = hostOf(v);
    const home = homeDomain(list);
    if (h && home && h !== home && !h.endsWith(`.${home}`)) setChosen("Link only");
  };

  const add = async (e: FormEvent) => {
    e.preventDefault();
    if (!canAdd) return;
    const done = await run.run(settled, plan);
    if (done && !done.reread) onDone(done.appId);
  };

  const rp = list.reverseProxy?.hostOverrides.length === 1 ? list.reverseProxy.hostOverrides[0] : "the Reverse Proxy's Host Override";
  const reachHint = [
    way === "Reverse proxy"
      ? `A Proxy Host, and ${host || "the hostname"} as an alias of ${rp}.`
      : way === "Host Override"
        ? "pfSense resolves the hostname straight to an address."
        : "ddash keeps the link and its Check only.",
    ...why,
  ].join(" ");
  const sourceHint = fromCompose
    ? "ddash writes the Compose file and its .env on the Docker host and starts it, then adds the Proxy Host and DNS. The new container is the App's Workload."
    : "The App runs already: ddash adds its Proxy Host and DNS.";

  return (
    <div className="flex flex-col gap-4 xl:flex-row xl:items-start">
      <div className="min-w-0 flex-1">
        <Panel title="Add App" actions={drafting ? <Link to={drafting}>Draft with AI</Link> : undefined}>
          <form onSubmit={(e) => void add(e)} className="flex flex-col gap-4">
            {draft && <Notice tone="info">A draft from the AI App helper: check the Compose file and the fields. Nothing runs until you add it.</Notice>}
            <Field label="URL" hint={typed && plan && plan.names.length > 0 ? <NameNotes names={plan.names} /> : undefined}>
              <TextInput autoFocus value={url} placeholder="https://jellyfin.home.example" onChange={(e) => typeUrl(e.target.value)} />
            </Field>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
              <Text label="Name" value={name} onChange={edit(setName)} placeholder={host || "Jellyfin"} />
              <IconField value={icon} onChange={edit(setIcon)} name={name || host} url={url} />
              <GroupField value={group} onChange={edit(setGroup)} groups={groupsOf(list.apps)} />
            </div>
            <Field label="Source" hint={sourceHint}>
              <Segmented label="Source" value={source} options={sources} onChange={edit(setSource)} />
            </Field>
            {fromCompose && <ComposeFields value={compose} onChange={edit(setCompose)} services={plan?.composeServices ?? []} />}
            <Field label="Reach" hint={reachHint}>
              <Segmented label="Reach" value={way} options={reaches} disabled={disabled} onChange={edit(setChosen)} />
            </Field>
            {way === "Reverse proxy" && (
              <ProxyFields value={proxy} onChange={edit(setProxy)} host={host} certificates={certificates} upstream={fromCompose ? planUpstream(plan) : undefined} />
            )}
            {way === "Host Override" && (
              <Text label="Address" value={address} onChange={edit(setAddress)} placeholder={fromCompose ? "The Docker host's address" : "198.18.1.60"} />
            )}
            {typed && plan && plan.blocked.length > 0 && (
              <Notice tone="warn">
                {plan.blocked.map((b) => (
                  <p key={b}>{b}</p>
                ))}
              </Notice>
            )}
            {outcome?.kind === "changed" && <Notice tone="warn">Changed since you looked: ddash will now do what&apos;s beside. Check it, then add again.</Notice>}
            {(outcome?.kind === "stopped" || outcome?.kind === "failed") && <Notice tone="down">{outcome.message}</Notice>}
            {outcome?.kind === "done" && (
              <Notice tone="warn">
                Added. {outcome.result.reread}: the list catches up at the next sync.
              </Notice>
            )}
            <FormActions>
              <Button variant="ghost" onClick={() => onDone()} disabled={run.pending}>
                {outcome?.kind === "done" ? "Close" : "Cancel"}
              </Button>
              {outcome?.kind === "done" ? (
                <Button onClick={() => onDone(outcome.result.appId)}>Open the App</Button>
              ) : (
                <Button type="submit" disabled={!canAdd}>
                  Add App
                </Button>
              )}
            </FormActions>
          </form>
        </Panel>
      </div>
      <div className="xl:w-80 xl:shrink-0">
        <Panel title="ddash will" description={run.plan ? undefined : "Updates as you type."}>
          {typed && plan ? <PlanSteps steps={steps(plan)} state={run.state} /> : <p className="text-sm text-muted-foreground">Type the App&apos;s URL to see what ddash will do.</p>}
        </Panel>
      </div>
    </div>
  );
}
