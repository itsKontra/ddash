import { checkStatus, workloadName } from "@ddash/display";
import { useAction } from "@ddash/plugin";
import { AppsService, FixKind, Reach, type App, type Fix, type ListAppsResponse, type Warning, type Workload } from "@ddash/proto/apps/apps_pb";
import type { Check } from "@ddash/proto/checks/checks_pb";
import { AppIcon, Button, Dialog, Facts, Field, FormActions, Notice, Panel, Pill, Presence, Select } from "@ddash/ui";
import { useId, useState, type ReactNode } from "react";
import { groupsOf, hostOf } from "./apps";
import { reachLook } from "./list";
import { AddressPanel, DisplayPanel, NetworkPanel, ProxyPanel, WorkloadPanel, type Editing, type PanelName } from "./panels";
import { PlanDialog, type DialogSlot } from "./plan";
import { fixLabel, WarningBoxes, type FixHandlers } from "./warnings";

/** The fixes that remove something, confirmed with a danger button. */
const removing: ReadonlySet<FixKind> = new Set([FixKind.REMOVE_NAME, FixKind.DELETE_PROXY_HOST, FixKind.DELETE_ENTRY]);

/**
 * The selected App on the right: its header with Delete…, its Warning
 * boxes, then the Panels Display, Address and DNS, Reverse proxy,
 * Workload and Check, and Network. One Panel edits at a time; external
 * writes, Delete and the external fixes open their Plan in a Dialog; a
 * Link-only App's Delete is a plain confirm.
 * Orphaned App settings get their own pane.
 */
export function AppPane({
  app,
  list,
  check,
  showIgnored,
  onGone,
}: {
  app: App;
  list: ListAppsResponse;
  check?: Check;
  showIgnored: boolean;
  /** The App is gone, e.g. deleted: the pane empties. */
  onGone: () => void;
}) {
  const [now, setNow] = useState<PanelName>();
  const [dialog, setDialog] = useState<ReactNode>();
  const title = useId();
  const slot: DialogSlot = { open: setDialog, close: () => setDialog(undefined) };
  const editing: Editing = { now, start: setNow, done: () => setNow(undefined) };
  const deleteRecord = useAction(AppsService.method.deleteRecord);
  // A Link-only App is ddash's alone: deleted after a plain confirm, without a Plan.
  const remove = async () => {
    if (app.reach === Reach.LINK_ONLY) {
      if (await deleteRecord.run({ id: app.id }, { confirm: { text: `Delete ${app.name} and its Check?`, action: "Delete" } })) onGone();
      return;
    }
    slot.open(
      <PlanDialog
        title={`Delete ${app.name}`}
        action={{ action: { case: "delete", value: { id: app.id } } }}
        confirm="Delete"
        danger
        onClose={slot.close}
        onDone={() => {
          slot.close();
          onGone();
        }}
      />,
    );
  };
  const fix = (_: Warning, f: Fix) => {
    const label = fixLabel(f, list.apps);
    slot.open(
      <PlanDialog
        title={label}
        action={{ action: { case: "fix", value: { id: app.id, fix: f } } }}
        confirm={label}
        danger={removing.has(f.kind)}
        onClose={slot.close}
        onDone={slot.close}
      />,
    );
  };
  const handlers: FixHandlers = {
    editWorkload: () => setNow("workload"),
    editProxy: () => setNow("proxy"),
    addProxy: () => setNow("proxy"),
    reattach: () => slot.open(<Reattach orphan={app} apps={list.apps} onClose={slot.close} />),
    remove: () => void remove(),
    external: fix,
  };
  return (
    <article aria-labelledby={title} className="flex flex-col gap-4">
      <Header id={title} app={app} check={check} onDelete={() => void remove()} />
      {deleteRecord.error && <Notice tone="down">{deleteRecord.error.rawMessage}</Notice>}
      <WarningBoxes app={app} apps={list.apps} showIgnored={showIgnored} handlers={handlers} onGone={onGone} />
      {app.orphaned ? (
        <Kept app={app} />
      ) : (
        <>
          <DisplayPanel app={app} groups={groupsOf(list.apps)} editing={editing} />
          <AddressPanel app={app} list={list} editing={editing} slot={slot} />
          <ProxyPanel app={app} list={list} editing={editing} slot={slot} />
          <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
            <WorkloadPanel app={app} check={check} editing={editing} />
            <NetworkPanel app={app} list={list} />
          </div>
        </>
      )}
      <Presence>{dialog}</Presence>
    </article>
  );
}

/** The pane's header: icon, name, URL, Reach, Check and Hidden, and Delete…. */
function Header({ id, app, check, onDelete }: { id: string; app: App; check?: Check; onDelete: () => void }) {
  const status = checkStatus(check);
  return (
    <div className="flex items-start gap-3">
      <AppIcon name={app.name} icon={app.icon} url={app.url} size={48} />
      <div className="min-w-0 flex-1">
        <h2 id={id} className="text-2xl font-extrabold tracking-tight break-words">{app.name}</h2>
        <div className="mt-1 flex flex-wrap items-center gap-2">
          <a href={app.url} target="_blank" rel="noreferrer" className="text-sm break-all hover:underline">
            {app.url}
          </a>
          {app.orphaned ? <Pill tone="warn">Orphaned App settings</Pill> : <Pill tone={app.reach === Reach.PROXIED ? "info" : "idle"}>{reachLook[app.reach].label}</Pill>}
          {!app.orphaned && (
            <Pill tone={status.tone} dot>
              {status.label}
            </Pill>
          )}
          {app.hidden && <Pill>Hidden</Pill>}
        </div>
      </div>
      {!app.orphaned && (
        <Button size="sm" variant="danger" onClick={onDelete}>
          Delete…
        </Button>
      )}
    </div>
  );
}

/** What orphaned App settings keep. */
function Kept({ app }: { app: App }) {
  const s = app.settings;
  return (
    <Panel
      title="Kept"
      description="Its Proxy Host and Host Override are gone. Its settings and Ignores stay, and its Check history with them, until they are reattached to an App or deleted."
    >
      <Facts
        items={[
          { label: "Kept for", value: hostOf(app), mono: true },
          { label: "Name", value: s?.name || "From the URL" },
          { label: "Icon", value: s?.icon || "Automatic" },
          { label: "Group", value: s?.group || "None" },
          { label: "Hidden", value: s?.hidden ? "Yes" : "No" },
          { label: "Workload", value: s?.workload ? workloadName({ ref: s.workload } as Workload) : "Matched" },
          { label: "App id", value: app.id, mono: true },
        ]}
      />
    </Panel>
  );
}

/** Reattaches orphaned App settings to an App without settings of its own. */
function Reattach({ orphan, apps, onClose }: { orphan: App; apps: readonly App[]; onClose: () => void }) {
  const attach = useAction(AppsService.method.reattach);
  const [to, setTo] = useState("");
  const form = useId();
  const targets = apps.filter((a) => (a.reach === Reach.PROXIED || a.reach === Reach.NOT_PROXIED) && !a.touched && !a.orphaned);
  return (
    <Dialog
      title={`Reattach ${orphan.name}`}
      subtitle={`The settings kept for ${hostOf(orphan)} move onto the App, which keeps their id and Check history.`}
      onClose={onClose}
      footer={
        <FormActions>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" form={form} disabled={!to || attach.pending}>
            Reattach
          </Button>
        </FormActions>
      }
    >
      <form
        id={form}
        className="flex flex-col gap-4"
        onSubmit={async (e) => {
          e.preventDefault();
          if (await attach.run({ id: orphan.id, to })) onClose();
        }}
      >
        <Field label="Reattach to" hint="Only Apps without settings of their own.">
          <Select value={to} onChange={(e) => setTo(e.target.value)}>
            <option value="">Choose an App…</option>
            {targets.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({hostOf(a)})
              </option>
            ))}
          </Select>
        </Field>
        {attach.error && <Notice tone="down">{attach.error.rawMessage}</Notice>}
      </form>
    </Dialog>
  );
}
