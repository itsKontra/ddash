import { useAction } from "@ddash/plugin";
import { NginxConfigService } from "@ddash/proto/nginx-config/nginx_config_pb";
import { Button, CodeBlock, Notice, PageTitle, Panel, Skeleton, StatusList } from "@ddash/ui";
import { ReadTime, useSnapshot } from "./snapshot";

/** The reverse-proxy Page: whether nginx's config loads, with a Refresh. */
export function ReverseProxy() {
  const refresh = useAction(NginxConfigService.method.refresh);
  return (
    <>
      <PageTitle
        kicker="Infrastructure"
        title="Reverse proxy"
        subtitle="nginx's config, as ddash last read it."
        actions={
          <Button variant="outline" size="sm" disabled={refresh.pending} onClick={() => void refresh.run({})}>
            Refresh
          </Button>
        }
      />
      <div className="flex flex-col gap-4">
        {refresh.error && <Notice tone="down">Refresh failed: {refresh.error.rawMessage}</Notice>}
        <ConfigHealth />
      </div>
    </>
  );
}

function ConfigHealth() {
  const { snapshot, error } = useSnapshot();
  return (
    <Panel title="Config health">
      {!snapshot ? (
        error ? (
          <Notice tone="down">nginx-config unavailable: {error.rawMessage}</Notice>
        ) : (
          <Skeleton />
        )
      ) : snapshot.error ? (
        <div className="flex flex-col gap-3">
          <Notice tone="down" title="nginx's config doesn't load">
            <CodeBlock label="nginx's error">{snapshot.error}</CodeBlock>
          </Notice>
          <p className="text-sm text-muted-foreground">
            {snapshot.readAt ? (
              <>
                Last good read <ReadTime at={snapshot.readAt} />
              </>
            ) : (
              "No good read yet."
            )}
          </p>
        </div>
      ) : (
        <StatusList
          items={[{ tone: "up", title: "Config loads", meta: snapshot.readAt && <>Read <ReadTime at={snapshot.readAt} /></> }]}
        />
      )}
    </Panel>
  );
}
