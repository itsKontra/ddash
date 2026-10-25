import { createElement, useSyncExternalStore } from "react";
import type { IconName } from "./icon-names";

export type { IconName };

/** A lucide icon's elements: [tag, attributes] pairs inside a 24×24 viewBox. */
type IconNodes = [string, Record<string, string>][];

const px = { sm: 14, md: 16, lg: 20 } as const;

export interface IconProps {
  /** A lucide icon; one lucide doesn't have draws the puzzle. */
  name: IconName;
  /** 14, 16 or 20 px. Default md. */
  size?: keyof typeof px;
  /** What the icon means, when nothing beside it says so. Without one the icon is decoration. */
  label?: string;
}

/**
 * A lucide icon, served by the core and drawn as an inline svg in
 * currentColor. While it loads, it reserves its box.
 */
export function Icon({ name, size = "md", label }: IconProps) {
  const nodes = useSyncExternalStore(
    (onChange) => icons.subscribe(name, onChange),
    () => icons.get(name),
  );
  const s = px[size];
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={s}
      height={s}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      className="shrink-0"
      {...(label ? { role: "img", "aria-label": label } : { "aria-hidden": true })}
    >
      {nodes?.map(([tag, attrs], i) => createElement(tag, { key: i, ...attrs }))}
    </svg>
  );
}

/**
 * The page's icons: each fetched at most once, the names asked for in one
 * render batched into one GET /api/icons.
 */
class IconCache {
  private nodes = new Map<string, IconNodes>();
  private requested = new Set<string>();
  private queued = new Set<string>();
  private listeners = new Map<string, Set<() => void>>();

  get(name: string): IconNodes | undefined {
    return this.nodes.get(name);
  }

  subscribe(name: string, onChange: () => void): () => void {
    let set = this.listeners.get(name);
    if (!set) this.listeners.set(name, (set = new Set()));
    set.add(onChange);
    this.request(name);
    return () => set.delete(onChange);
  }

  private request(name: string) {
    if (this.requested.has(name)) return;
    this.requested.add(name);
    if (this.queued.size === 0) queueMicrotask(() => void this.flush());
    this.queued.add(name);
  }

  private async flush() {
    const names = [...this.queued];
    this.queued.clear();
    try {
      const res = await globalThis.fetch(`/api/icons?names=${names.map(encodeURIComponent).join(",")}&release=${encodeURIComponent(release())}`);
      if (!res.ok) throw new Error(`GET /api/icons: ${res.status}`);
      const got = (await res.json()) as Record<string, IconNodes>;
      for (const n of names) if (got[n]) this.nodes.set(n, got[n]);
    } catch {
      // Left empty, its box reserved; the next Icon of that name asks again.
    }
    for (const n of names) {
      if (!this.nodes.has(n)) this.requested.delete(n);
      for (const l of this.listeners.get(n) ?? []) l();
    }
  }
}

/** The running Release, which index.html names, so icon responses cache immutably. */
const release = () => document.querySelector<HTMLMetaElement>('meta[name="ddash-release"]')?.content ?? "";

const icons = new IconCache();
