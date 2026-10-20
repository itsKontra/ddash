import { useAction } from "@ddash/plugin";
import { NginxConfigService } from "@ddash/proto/nginx-config/nginx_config_pb";
import { Button, PageHeader, StatusDot } from "@ddash/ui";
import { ReadTime, useSnapshot } from "./snapshot";

/** The reverse-proxy Page: whether nginx's config loads, with a Refresh. */
export function ReverseProxy() {
  const refresh = useAction(NginxConfigService.method.refresh);
  return (
    <div>
      <PageHeader
        title="Reverse proxy"
        subtitle="nginx's config, as ddash last read it."
        actions={
          <Button variant="outline" size="sm" disabled={refresh.pending} onClick={() => void refresh.run({})}>
            Refresh
          </Button>
        }
      />
      {refresh.error && <p className="mb-4 text-sm text-down">Refresh failed: {refresh.error.rawMessage}</p>}
      <ConfigHealth />
    </div>
  );
}

function ConfigHealth() {
  const { snapshot, error } = useSnapshot();
  return (
    <section aria-labelledby="config-health" className="rounded-ui border border-border bg-card p-4">
      <h2 id="config-health" className="mb-3 font-semibold">
        Config health
      </h2>
      {!snapshot ? (
        error && <p className="text-sm text-muted-foreground">nginx-config unavailable: {error.rawMessage}</p>
      ) : snapshot.error ? (
        <div className="flex flex-col gap-3 text-sm">
          <p className="flex items-center gap-2">
            <StatusDot tone="down" />
            <span className="font-medium">nginx's config doesn't load</span>
          </p>
          <pre role="alert" className="overflow-x-auto rounded-ui border border-border bg-muted p-3 text-xs whitespace-pre-wrap text-down">
            {snapshot.error}
          </pre>
          <p className="text-muted-foreground">
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
        <p className="flex items-center gap-2 text-sm">
          <StatusDot tone="up" />
          <span className="font-medium">Config loads</span>
          {snapshot.readAt && (
            <span className="text-muted-foreground">
              · read <ReadTime at={snapshot.readAt} />
            </span>
          )}
        </p>
      )}
    </section>
  );
}
