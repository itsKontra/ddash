import { Button, EmptyState, Notice, PageTitle, Panel, Skeleton, useQueryState } from "@ddash/ui";
import { useState } from "react";
import { useLocation, useSearchParams } from "react-router";
import { useApps } from "./apps";
import { parseDraft } from "@ddash/draft";
import { useAppChecks } from "./checks";
import { AddApp } from "./add-app";
import { AppList } from "./list";
import { Notices } from "./notices";
import { AppPane } from "./pane";

/** The value of ?app= while adding an App. */
const adding = "new";

/**
 * The Apps Page, the split view: the list on the left and the App ?app=
 * names on the right, with the Notices over both. ?add=compose opens Add
 * App from a new Compose Project, filled in from the URL fragment's draft
 * (#draft=…) when it has one.
 */
export function AppsPage() {
  const { list, error, answeredAt } = useApps();
  const { byApp, error: checksError } = useAppChecks();
  const [q, setQ] = useQueryState("q", "");
  const [ignored, setIgnored] = useQueryState("ignored", "");
  const [params, setParams] = useSearchParams();
  const { hash } = useLocation();
  const selected = params.get("app") ?? "";
  const fromCompose = params.get("add") === "compose";
  // The draft as the page opened with it: the fragment may go.
  const [draft] = useState(() => parseDraft(hash));
  /** Shows the App id (none: nothing), leaving Add App and its draft. */
  const select = (id: string) =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("add");
        if (id) next.set("app", id);
        else next.delete("app");
        return next;
      },
      { replace: true, preventScrollReset: true },
    );
  const showIgnored = ignored === "1";
  const add = (
    <Button icon="plus" onClick={() => select(adding)}>
      Add App
    </Button>
  );
  const app = list?.apps.find((a) => a.id === selected);

  return (
    <div>
      <PageTitle kicker="Services" title="Apps" subtitle="Every Proxy Host in Nginx Proxy Manager and Host Override in pfSense, plus the Apps you add." actions={add} />
      <div className="flex flex-col gap-4">
        {!list && !error && <Skeleton height="lg" />}
        {!list && error && <Notice tone="down">apps unavailable: {error.rawMessage}</Notice>}
        {list && <Notices list={list} />}
        {list && list.apps.length > 0 && checksError && <Notice tone="warn">Check status unavailable: {checksError.rawMessage}</Notice>}
        {list && (
          <div className="flex flex-col gap-6 lg:flex-row lg:items-start">
            <div className="lg:w-80 lg:shrink-0">
              <Panel flush>
                {list.apps.length === 0 ? (
                  <EmptyState icon="layout-grid" title="No Apps yet">
                    Proxy Hosts and Host Overrides show here once ddash reads them, or add an App.
                  </EmptyState>
                ) : (
                  <AppList
                    list={list}
                    checks={byApp}
                    q={q}
                    onSearch={setQ}
                    showIgnored={showIgnored}
                    onShowIgnored={(on) => setIgnored(on ? "1" : "")}
                    selected={selected}
                    onSelect={select}
                    now={answeredAt}
                  />
                )}
              </Panel>
            </div>
            <div className="min-w-0 flex-1">
              {fromCompose ? (
                <AddApp list={list} fromCompose draft={draft} onDone={(id) => select(id ?? "")} />
              ) : selected === adding ? (
                <AddApp list={list} onDone={(id) => select(id ?? "")} />
              ) : app ? (
                <AppPane key={app.id} app={app} list={list} check={byApp?.get(app.id)} showIgnored={showIgnored} onGone={() => select("")} />
              ) : (
                <Panel>
                  <p className="py-16 text-center text-sm text-muted-foreground">
                    {selected ? `No App has the id ${selected}. Its sources may be gone.` : "Pick an App on the left, or add one."}
                  </p>
                </Panel>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

