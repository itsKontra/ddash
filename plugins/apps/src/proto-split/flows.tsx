// PROTOTYPE (wayfinder #139): the Dialog flows every variant opens the same
// way: Add reverse proxy (Not-proxied Apps, and the "Host Override without
// a Proxy Host" fix) and Delete.
import { Button, Dialog, FormActions, Notice } from "@ddash/ui";
import { useState } from "react";
import { coveringCert, hostOf, reverseProxy, store, type PApp } from "./data";
import { blankProxy, planAddProxy, planDelete, ProxyFields, Steps, useProxyBlocked, useRunner } from "./shared";

/** Runs a plan in a Dialog: the plan first, then its progress, then Done or where it stopped. */
function RunDialog({
  title,
  subtitle,
  plan,
  go,
  onClose,
  onDone,
  children,
  danger,
  blocked,
}: {
  title: string;
  subtitle?: string;
  plan: ReturnType<typeof planDelete>;
  go: string;
  onClose: () => void;
  onDone: (failedAt?: number) => void;
  children?: React.ReactNode;
  danger?: boolean;
  blocked?: string;
}) {
  const r = useRunner();
  const [result, setResult] = useState<"ok" | number>();
  const finished = result !== undefined;
  return (
    <Dialog
      title={title}
      subtitle={subtitle}
      onClose={onClose}
      footer={
        <FormActions status={r.running ? "Running one step at a time…" : undefined}>
          {finished ? (
            <Button onClick={onClose}>Close</Button>
          ) : (
            <>
              <Button variant="ghost" onClick={onClose} disabled={r.running}>
                Cancel
              </Button>
              <Button
                variant={danger ? "danger" : "default"}
                disabled={r.running || Boolean(blocked)}
                onClick={async () => {
                  const failed = await r.run(plan);
                  setResult(failed ?? "ok");
                  onDone(failed);
                }}
              >
                {go}
              </Button>
            </>
          )}
        </FormActions>
      }
    >
      <div className="flex flex-col gap-4">
        {!r.states && children}
        {blocked && <Notice tone="warn">{blocked}</Notice>}
        <div>
          <p className="mb-2 text-xs font-semibold">{r.states ? "Progress" : "ddash will"}</p>
          <Steps steps={plan} states={r.states} />
        </div>
        {typeof result === "number" && (
          <Notice tone="down" title={`Stopped at: ${plan[result].label}`}>
            Nothing was rolled back. Whatever is left half-done shows as Warnings on the App.
          </Notice>
        )}
      </div>
    </Dialog>
  );
}

export function AddProxyDialog({ app, onClose }: { app: PApp; onClose: () => void }) {
  const host = hostOf(app.url);
  const [ph, setPh] = useState(() => ({ ...blankProxy(coveringCert(host)), host: app.overrideIp ?? "" }));
  const blocked = useProxyBlocked();
  const plan = planAddProxy(app, ph);
  return (
    <RunDialog
      title={`Add reverse proxy to ${app.name}`}
      subtitle={`A Proxy Host serves ${host}, and its Host Override becomes an alias of ${reverseProxy.host}.`}
      plan={app.reach === "proxied" ? plan.filter((s) => !s.label.startsWith("Replace") && !s.label.startsWith("Apply")) : plan}
      go="Add reverse proxy"
      blocked={blocked}
      onClose={onClose}
      onDone={(failed) => {
        if (failed !== undefined) return;
        store.update(app.id, (a) => ({
          ...a,
          reach: "proxied",
          proxy: { ...ph, id: 30 + Math.floor(Math.random() * 40) },
          overrideIp: undefined,
          dns: a.dns.map((d) => ({ ...d, how: "alias", ip: reverseProxy.ip, parent: reverseProxy.host })),
          warnings: a.warnings.filter((w) => w.kind !== "no-proxy-host" && w.kind !== "dns-bypass"),
        }));
      }}
    >
      <ProxyFields value={ph} onChange={setPh} host={host} />
    </RunDialog>
  );
}

export function DeleteDialog({ app, onClose, onDeleted }: { app: PApp; onClose: () => void; onDeleted: () => void }) {
  return (
    <RunDialog
      title={`Delete ${app.name}?`}
      subtitle={app.reach === "link-only" ? "Only ddash holds this App." : "This deletes it in pfSense and Nginx Proxy Manager too, not just in ddash."}
      plan={planDelete(app)}
      go="Delete"
      danger
      onClose={onClose}
      onDone={(failed) => {
        if (failed === undefined) {
          store.remove(app.id);
          onDeleted();
        }
      }}
    />
  );
}

export { RunDialog };
