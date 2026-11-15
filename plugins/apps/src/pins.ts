import { useAction } from "@ddash/plugin";
import { AppsService, type App } from "@ddash/proto/apps/apps_pb";
import { useApps, visible } from "./apps";

const byPin = (a: App, b: App) => a.pin - b.pin;

/** Every pinned App's id in the Operator's order, Hidden and orphaned ones included: what SetPins keeps. */
export const pinIds = (apps: readonly App[]) =>
  apps
    .filter((a) => a.pin > 0)
    .sort(byPin)
    .map((a) => a.id);

/** The pinned Apps the Apps Widget shows: those neither Hidden nor orphaned, in the Operator's order. */
export const pinned = (apps: readonly App[]) => visible(apps).filter((a) => a.pin > 0).sort(byPin);

/** The pinned Apps in the Operator's order, undefined while apps answers; e.g. for a command palette. */
export function usePinnedApps(): App[] | undefined {
  const { list } = useApps();
  return list && pinned(list.apps);
}

/** The pins with id pinned last, or unpinned. */
export function withPin(apps: readonly App[], id: string, on: boolean): string[] {
  const ids = pinIds(apps).filter((p) => p !== id);
  return on ? [...ids, id] : ids;
}

/**
 * The pins with the shown pinned App id moved one place earlier (-1) or
 * later (1) among the shown ones, past its neighbour there; undefined when
 * it is already first or last.
 */
export function moved(apps: readonly App[], id: string, by: -1 | 1): string[] | undefined {
  const shown = pinned(apps).map((a) => a.id);
  const i = shown.indexOf(id);
  const past = i < 0 ? undefined : shown[i + by];
  if (past === undefined) return undefined;
  const ids = pinIds(apps).filter((p) => p !== id);
  ids.splice(ids.indexOf(past) + (by > 0 ? 1 : 0), 0, id);
  return ids;
}

/** Pins, unpins and moves Apps through SetPins, from the pins ListApps last gave. */
export function usePins() {
  const { list } = useApps();
  const set = useAction(AppsService.method.setPins);
  const apply = async (ids: string[] | undefined) => {
    if (ids) await set.run({ ids });
  };
  return {
    pin: (id: string, on: boolean) => apply(list && withPin(list.apps, id, on)),
    move: (id: string, by: -1 | 1) => apply(list && moved(list.apps, id, by)),
    pending: set.pending,
    error: set.error,
  };
}
