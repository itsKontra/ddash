import { useQuery } from "@ddash/plugin";
import { AdguardApiService, type Query } from "@ddash/proto/adguard-api/adguard_api_pb";
import { Badge } from "@ddash/ui";
import { useId, useState } from "react";

/** How many of a Device's queries its detail shows. */
export const QueryLimit = 100;

const time = (q: Query) => {
  if (!q.time) return "—";
  const d = new Date(Number(q.time.seconds) * 1000);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
};

const cell = "px-2 py-1.5";

/** A Device's last 100 AdGuard queries by its address, newest first, with a "Blocked only" filter. */
export function DeviceQueries({ ip }: { ip: string }) {
  const [blockedOnly, setBlockedOnly] = useState(false);
  const id = useId();
  const { data, error } = useQuery(AdguardApiService.method.listQueries, { client: ip, limit: QueryLimit, blockedOnly });
  return (
    <div className="flex flex-col gap-2">
      <label htmlFor={id} className="inline-flex items-center gap-2 text-sm">
        <input id={id} type="checkbox" checked={blockedOnly} onChange={(e) => setBlockedOnly(e.target.checked)} />
        Blocked only
      </label>
      {error && !data && <p className="text-down-text">{error.rawMessage}</p>}
      {data && data.queries.length === 0 && (
        <p className="text-muted-foreground">{blockedOnly ? `No blocked queries from ${ip} in AdGuard's log.` : `No queries from ${ip} in AdGuard's log.`}</p>
      )}
      {data && data.queries.length > 0 && (
        <table aria-label="Queries" className="w-full text-sm">
          <thead>
            <tr className="border-b border-border text-left text-xs text-muted-foreground">
              <th scope="col" className={`${cell} pl-0 font-medium`}>
                Time
              </th>
              <th scope="col" className={`${cell} font-medium`}>
                Domain
              </th>
              <th scope="col" className={`${cell} font-medium`}>
                Type
              </th>
              <th scope="col" className={`${cell} pr-0 font-medium`}>
                Answer
              </th>
            </tr>
          </thead>
          <tbody>
            {data.queries.map((q, i) => (
              <tr key={`${i}-${q.domain}`} className="border-b border-border last:border-b-0">
                <td className={`${cell} pl-0 text-muted-foreground tabular-nums`}>{time(q)}</td>
                <th scope="row" className={`${cell} text-left font-medium break-all`}>
                  {q.domain}
                </th>
                <td className={`${cell} text-muted-foreground`}>{q.type}</td>
                <td className={`${cell} pr-0`}>
                  {q.blocked ? (
                    <span className="inline-flex flex-wrap items-center gap-2">
                      <Badge tone="down">Blocked</Badge>
                      {q.rule && <span className="text-xs text-muted-foreground break-all">{q.rule}</span>}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">
                      {q.status}
                      {q.cached && ", cached"}
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <p className="text-xs text-muted-foreground">The last {QueryLimit} queries AdGuard logged from {ip}, newest first.</p>
    </div>
  );
}
