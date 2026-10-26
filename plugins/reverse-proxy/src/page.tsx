import { useAction, usePluginLink } from "@ddash/plugin";
import { AppsService } from "@ddash/proto/apps/apps_pb";
import { NginxProxyManagerApiService, Status as NpmStatus, type GetStatusResponse } from "@ddash/proto/nginx-proxy-manager-api/nginx_proxy_manager_api_pb";
import { Button, CodeBlock, EmptyState, Notice, PageTitle, Panel, Pill, Skeleton, StatusList, type Tone } from "@ddash/ui";
import { Link } from "react-router";
import { offlineCount, useApps } from "./apps";
import { Certificates } from "./certificates";
import { notConfigured, Time, useStatus } from "./npm";

/** The reverse-proxy Page: whether Nginx Proxy Manager answers and its certificates, with a Refresh. */
export function ReverseProxy() {
  const refresh = useRefresh();
  const { error } = useStatus();
  return (
    <>
      <PageTitle
        kicker="Infrastructure"
        title="Reverse proxy"
        subtitle="Nginx Proxy Manager, as ddash last read it."
        actions={
          <Button variant="outline" size="sm" disabled={refresh.pending} onClick={() => void refresh.run()}>
            Refresh
          </Button>
        }
      />
      <div className="flex flex-col gap-4">
        {refresh.error && <Notice tone="down">Refresh failed: {refresh.error.rawMessage}</Notice>}
        {notConfigured(error) ? (
          <SetUp />
        ) : (
          <>
            <StatusPanel />
            <Certificates />
          </>
        )}
      </div>
    </>
  );
}

/** Refresh: nginx-proxy-manager-api drops its caches, then apps re-reads it. */
function useRefresh() {
  const npm = useAction(NginxProxyManagerApiService.method.refresh);
  const apps = useAction(AppsService.method.refresh);
  return {
    run: async () => {
      if (await npm.run({})) await apps.run({});
    },
    pending: npm.pending || apps.pending,
    error: npm.error ?? apps.error,
  };
}

/** "Set up Nginx Proxy Manager", while nginx-proxy-manager-api has no config. */
function SetUp() {
  return (
    <Panel>
      <EmptyState
        icon="waypoints"
        title="Set up Nginx Proxy Manager"
        action={
          <Link to="/plugins" className="text-sm font-semibold text-primary hover:underline">
            Configure it in Plugins
          </Link>
        }
      >
        Give nginx-proxy-manager-api Nginx Proxy Manager's address and ddash's user there to read the Reverse Proxy and its certificates.
      </EmptyState>
    </Panel>
  );
}

const states: Record<NpmStatus, { tone: Tone; label: string; title: string }> = {
  [NpmStatus.UNSPECIFIED]: { tone: "idle", label: "Unknown", title: "Not read yet" },
  [NpmStatus.UP]: { tone: "up", label: "Up", title: "Nginx Proxy Manager answers" },
  [NpmStatus.WARN]: { tone: "warn", label: "Warn", title: "The last read failed" },
  [NpmStatus.DOWN]: { tone: "down", label: "Down", title: "ddash can't read Nginx Proxy Manager" },
};

/** Status: Up, Warn with the last good read, or Down, with the error; and the Offline Proxy Hosts. */
function StatusPanel() {
  const { status, error } = useStatus();
  return (
    <Panel title="Status">
      <div className="flex flex-col gap-3">
        {!status ? error ? <Notice tone="down">nginx-proxy-manager-api unavailable: {error.rawMessage}</Notice> : <Skeleton /> : <StatusItem status={status} />}
        <Offline />
      </div>
    </Panel>
  );
}

function StatusItem({ status }: { status: GetStatusResponse }) {
  const s = states[status.status];
  const warn = status.status === NpmStatus.WARN;
  return (
    <StatusList
      items={[
        {
          tone: s.tone,
          title: s.title,
          aside: <Pill tone={s.tone}>{s.label}</Pill>,
          meta: (
            <>
              {status.readAt && (
                <>
                  Read <Time at={status.readAt} />
                </>
              )}
              {warn && status.lastGoodReadAt && (
                <>
                  {" · last good read "}
                  <Time at={status.lastGoodReadAt} />
                </>
              )}
              {status.version && ` · Nginx Proxy Manager ${status.version}`}
            </>
          ),
          body: status.error ? <CodeBlock label="Error">{status.error}</CodeBlock> : undefined,
        },
      ]}
    />
  );
}

/**
 * "2 Proxy Hosts offline", linking to the Apps that need attention. It
 * doesn't change the Reverse Proxy's own status or badge: each is a
 * Warning on its App.
 */
function Offline() {
  const { list } = useApps();
  // Needs attention lists them on the Apps Page.
  const page = usePluginLink("apps");
  const n = offlineCount(list?.apps ?? []);
  if (n === 0) return null;
  const text = n === 1 ? "1 Proxy Host offline" : `${n} Proxy Hosts offline`;
  return (
    <p className="text-sm text-warn-text">
      {page ? (
        <Link to={page} className="font-semibold hover:underline">
          {text}
        </Link>
      ) : (
        text
      )}
    </p>
  );
}
