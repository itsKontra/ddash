import { checkStatus, workloadName } from "@ddash/display";
import { useAction } from "@ddash/plugin";
import { AppsService, Reach, type App, type ListAppsResponse, type Workload } from "@ddash/proto/apps/apps_pb";
import type { Check } from "@ddash/proto/checks/checks_pb";
import { AppIcon, Button, Dialog, Facts, Field, FormActions, Notice, Panel, Pill, Presence, Select } from "@ddash/ui";
import { useId, useState, type ReactNode } from "react";
import { groupsOf, hostOf } from "./apps";
import { reachLook } from "./list";
import { AddressPanel, DisplayPanel, NetworkPanel, ProxyPanel, WorkloadPanel, type Editing, type PanelName } from "./panels";
import { WarningBoxes, type FixHandlers } from "./warnings";

/**
 * The pane's Dialog slot: one Dialog at a time over the pane, such as
 * Reattach, and the Plan Dialogs of external writes. open puts one in it,
 * close empties it.
 */
export interface DialogSlot {
  open: (dialog: ReactNode) => void;
  close: () => void;
}

/**
 * The selected App on the right: its header with Delete…, its Warning
 * boxes, then the Panels Display, Address and DNS, Reverse proxy,
 * Workload and Check, and Network. One Panel edits at a time; orphaned App
 * settings get their own pane.
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
  const handlers: FixHandlers = {
    editWorkload: () => setNow("workload"),
    reattach: () => slot.open(<Reattach orphan={app} apps={list.apps} onClose={slot.close} />),
  };
  return (
    <article aria-labelledby={title} className="flex flex-col gap-4">
      <Header id={title} app={app} check={check} onGone={onGone} />
      <WarningBoxes app={app} apps={list.apps} showIgnored={showIgnored} handlers={handlers} onGone={onGone} />
      {app.orphaned ? (
        <Kept app={app} />
      ) : (
        <>
          <DisplayPanel app={app} groups={groupsOf(list.apps)} editing={editing} />
          <AddressPanel app={app} list={list} editing={editing} />
          <ProxyPanel app={app} list={list} />
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
function Header({ id, app, check, onGone }: { id: string; app: App; check?: Check; onGone: () => void }) {
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
      {!app.orphaned && <Delete app={app} onGone={onGone} />}
    </div>
  );
}

/**
 * Delete…: a Link-only App goes at once, after a confirm. Deleting an App
 * with a Proxy Host or a Host Override writes to pfSense and Nginx Proxy
 * Manager through a Plan, which the write tickets add here.
 */
function Delete({ app, onGone }: { app: App; onGone: () => void }) {
  const remove = useAction(AppsService.method.deleteApp);
  const linkOnly = app.reach === Reach.LINK_ONLY;
  return (
    <div className="flex flex-col items-end gap-2">
      <Button
        size="sm"
        variant="danger"
        disabled={!linkOnly || remove.pending}
        title={linkOnly ? undefined : "Writes to pfSense or Nginx Proxy Manager aren't available yet"}
        onClick={async () => {
          if (await remove.run({ id: app.id }, { confirm: { text: `Delete the App ${app.name}?`, action: "Delete" } })) onGone();
        }}
      >
        Delete…
      </Button>
      {remove.error && <Notice tone="down">{remove.error.rawMessage}</Notice>}
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
