import type { Host } from "@ddash/proto/compose/compose_pb";

// What the AI App helper sends, and the draft it hands on. The preview
// shows prompt(): the very text ai-helper sends as the one user message
// (its Go Prompt builds the same), under ai-helper's fixed system prompt.

/** The context sent beside the description: never a Compose or .env file's content. */
export interface DraftContext {
  host: string;
  projects: string[];
  used_ports: number[];
  networks: string[];
  home_domain: string;
}

/** The context of the Docker host picked (none: an empty one), with the networks and home domain typed. */
export function contextOf(host: Host | undefined, networks: string, homeDomain: string): DraftContext {
  const ports = new Set<number>();
  const add = (cs: readonly { ports: readonly { publicPort: number }[] }[]) => {
    for (const c of cs) for (const p of c.ports) if (p.publicPort) ports.add(p.publicPort);
  };
  if (host) {
    add(host.containers);
    for (const p of host.projects) for (const s of p.services) add(s.containers);
  }
  return {
    host: host?.name ?? "",
    projects: host ? [...new Set(host.projects.map((p) => p.name))].sort() : [],
    used_ports: [...ports].sort((a, b) => a - b),
    networks: networks
      .split(/[\s,]+/)
      .map((n) => n.trim())
      .filter(Boolean),
    home_domain: homeDomain.trim().replace(/^\.+|\.+$/g, ""),
  };
}

/** The user message sent: the description and the context. */
export const prompt = (description: string, context: DraftContext) => `Description:\n${description.trim()}\n\nContext:\n${JSON.stringify(context, null, 2)}`;

/** A draft as the Docker Plugin's Add Compose Project and the apps Plugin's Add App read it from "#draft=…". */
export interface DraftJSON {
  host?: string;
  name: string;
  compose: string;
  app?: { url?: string; name?: string; icon?: string; group?: string; service?: string; port?: number };
}

/** The fragment carrying draft: "#draft=" and its JSON, UTF-8, base64url without padding. */
export function draftFragment(draft: DraftJSON): string {
  let bin = "";
  for (const b of new TextEncoder().encode(JSON.stringify(draft))) bin += String.fromCharCode(b);
  return "#draft=" + btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
