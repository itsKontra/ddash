import { useQuery } from "@ddash/plugin";
import { AdguardApiService, type Query } from "@ddash/proto/adguard-api/adguard_api_pb";
import { Checkbox, DataTable, Notice, Panel, Pill, type Column } from "@ddash/ui";
import { useState } from "react";

/** How many of a Device's queries its detail shows. */
export const QueryLimit = 100;

const time = (q: Query) => {
  if (!q.time) return "—";
  const d = new Date(Number(q.time.seconds) * 1000);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
};

const columns: Column<Query>[] = [
  { id: "domain", header: "Domain", rowHeader: true, cell: (q) => q.domain },
  { id: "time", header: "Time", mono: true, cell: time },
  { id: "type", header: "Type", cell: (q) => <span className="text-muted-foreground">{q.type}</span> },
  {
    id: "answer",
    header: "Answer",
    cell: (q) =>
      q.blocked ? (
        <span className="inline-flex items-center gap-2">
          <Pill tone="down">Blocked</Pill>
          {q.rule && <span className="text-xs break-all text-muted-foreground">{q.rule}</span>}
        </span>
      ) : (
        <span className="text-muted-foreground">
          {q.status}
          {q.cached && ", cached"}
        </span>
      ),
  },
];

/** The Queries Panel: a Device's last 100 AdGuard queries by its address, newest first, with a "Blocked only" filter. */
export function DeviceQueries({ ip }: { ip: string }) {
  const [blockedOnly, setBlockedOnly] = useState(false);
  const { data, error } = useQuery(AdguardApiService.method.listQueries, { client: ip, limit: QueryLimit, blockedOnly });
  const failed = error && !data;
  return (
    <Panel
      title="Queries"
      description={`The last ${QueryLimit} queries AdGuard logged from ${ip}, newest first.`}
      toolbar={<Checkbox label="Blocked only" checked={blockedOnly} onChange={(e) => setBlockedOnly(e.target.checked)} />}
      flush={!failed}
    >
      {failed ? (
        <Notice tone="down">{error.rawMessage}</Notice>
      ) : (
        <DataTable
          rows={data?.queries ?? []}
          // AdGuard's log has no id; a query is its place in it.
          rowKey={(q) => `${data?.queries.indexOf(q)}-${q.domain}`}
          columns={columns}
          loading={!data}
          density="compact"
          empty={blockedOnly ? `No blocked queries from ${ip} in AdGuard's log.` : `No queries from ${ip} in AdGuard's log.`}
        />
      )}
    </Panel>
  );
}
