import { useState } from "react";

/** What useShownRuns needs to know of a run. */
export interface RunState {
  id: string;
  ended: boolean;
}

/**
 * The runs a Page shows: every run in flight, and those that ended while
 * the Page was open. endedBefore holds the ids that had already ended at
 * the Page's first read; until that read only the runs in flight show.
 */
export function shownRuns<T>(runs: readonly T[], endedBefore: ReadonlySet<string> | undefined, state: (r: T) => RunState): T[] {
  return runs.filter((r) => {
    const s = state(r);
    return !s.ended || (endedBefore !== undefined && !endedBefore.has(s.id));
  });
}

/**
 * Keeps a finished run's result on its Page only while the Operator who
 * had it open stays there: a run in flight shows, and so does one that
 * ended while the Page was open. A run that had already ended when the
 * Page first read its runs (after a reload, or coming back later) is left
 * out: its outcome is in Recently on the Overview and in the Audit Log.
 *
 *     const q = useQuery(ComposeService.method.listRuns, {});
 *     const runs = useShownRuns(q.data?.runs, q.isFetchedAfterMount, (r) => ({ id: r.id, ended: !!r.ended }));
 *
 * fresh says runs were read since the Page opened: a cached answer from an
 * earlier visit isn't the Page's first read.
 */
export function useShownRuns<T>(runs: readonly T[] | undefined, fresh: boolean, state: (r: T) => RunState): T[] {
  const [before, setBefore] = useState<ReadonlySet<string>>();
  if (!runs) return [];
  if (before === undefined && fresh) {
    const first = new Set(runs.flatMap((r) => (state(r).ended ? [state(r).id] : [])));
    setBefore(first);
    return shownRuns(runs, first, state);
  }
  return shownRuns(runs, before, state);
}
