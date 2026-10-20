import type { Fetch } from "@ddash/plugin";
import { createContext, useContext, useSyncExternalStore } from "react";

/** Whether ddash answers. */
export interface Reach {
  unreachable: boolean;
  /** When ddash last answered; undefined before it ever did. */
  lastAnswer?: Date;
}

/**
 * Watches every call the shell and the Plugins make to the host. A network
 * error, or a 5xx that isn't a Connect error answer (a Component's
 * Unavailable is a 503 from a host that answered), makes ddash unreachable
 * until the next answer. A 401 means the session is gone: it reloads the
 * page, once, into sign-in.
 */
export class HostWatch {
  private state: Reach = { unreachable: false };
  private lastAnswer?: Date;
  private reloading = false;
  private readonly listeners = new Set<() => void>();

  constructor(
    private readonly inner: Fetch,
    private readonly reload: () => void,
  ) {}

  /** The fetch every transport uses. */
  readonly fetch: Fetch = async (input, init) => {
    let res: Response;
    try {
      res = await this.inner(input, init);
    } catch (err) {
      if (!(err instanceof DOMException && err.name === "AbortError")) this.update(true);
      throw err;
    }
    if (res.status === 401) {
      if (!this.reloading) {
        this.reloading = true;
        this.reload();
      }
    } else if (res.status >= 500 && !res.headers.get("Content-Type")?.startsWith("application/json")) {
      this.update(true);
    } else {
      this.lastAnswer = new Date();
      this.update(false);
    }
    return res;
  };

  private update(unreachable: boolean) {
    if (unreachable === this.state.unreachable) return;
    this.state = { unreachable, lastAnswer: this.lastAnswer };
    this.listeners.forEach((l) => l());
  }

  readonly subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };

  readonly get = (): Reach => this.state;
}

export const ReachContext = createContext<HostWatch | null>(null);

/** Whether ddash answers, as the shell's HostWatch sees it. */
export function useReach(): Reach {
  const watch = useContext(ReachContext);
  if (!watch) throw new Error("useReach: not inside the shell");
  return useSyncExternalStore(watch.subscribe, watch.get);
}

/** HH:MM, local time. */
export const clock = (d: Date) => `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;

/** The banner saying ddash is unreachable, and how old what's on screen is. */
export function unreachableText(reach: Reach): string {
  return reach.lastAnswer ? `ddash unreachable, showing data from ${clock(reach.lastAnswer)}` : "ddash unreachable";
}
