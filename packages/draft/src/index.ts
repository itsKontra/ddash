// A draft of a new Compose Project and its App, passed in the URL's
// fragment as #draft=<base64url(JSON)>. The AI App helper writes it; the
// apps Plugin's Add App (/apps?add=compose#draft=…) and the Docker
// Plugin's Add Compose Project (/docker/projects/new#draft=…) read it.
// Only the fragment carries it, so it never reaches a server's logs.
//
// This is the one contract: no Plugin declares the type or decodes the
// fragment by itself.

/** The draft's JSON. */
export interface Draft {
  /** The Docker host's name (a Docker Agent's). */
  host?: string;
  /** The Compose Project's name. */
  name: string;
  /** Its directory; unset for <first root>/<name>. */
  directory?: string;
  /** The Compose file's text. */
  compose: string;
  /** The .env's text; unset for none. */
  env?: string;
  /** The Add App fields. */
  app?: {
    url?: string;
    name?: string;
    icon?: string;
    group?: string;
    /** The Compose service the App opens, and its published port. */
    service?: string;
    port?: number;
  };
}

const isString = (v: unknown): v is string => typeof v === "string";
const optString = (v: unknown) => (isString(v) ? v : undefined);

/** base64url text as UTF-8 text; undefined when it isn't base64url. */
function fromBase64url(s: string): string | undefined {
  try {
    const b64 = s.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(s.length / 4) * 4, "=");
    const bin = atob(b64);
    const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0));
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return undefined;
  }
}

/** text as UTF-8, base64url without padding. */
function toBase64url(text: string): string {
  const bytes = new TextEncoder().encode(text);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/**
 * The draft a URL fragment ("#draft=…", the "#" optional) carries;
 * undefined without one, or when it isn't a draft: not base64url JSON,
 * or without a name and Compose file. Fields of the wrong type are
 * dropped.
 */
export function parseDraft(fragment: string): Draft | undefined {
  const params = new URLSearchParams(fragment.replace(/^#/, ""));
  const raw = params.get("draft");
  if (!raw) return undefined;
  const text = fromBase64url(raw);
  if (text === undefined) return undefined;
  let v: unknown;
  try {
    v = JSON.parse(text);
  } catch {
    return undefined;
  }
  if (typeof v !== "object" || v === null) return undefined;
  const o = v as Record<string, unknown>;
  if (!isString(o.name) || !isString(o.compose)) return undefined;
  const draft: Draft = { name: o.name, compose: o.compose, env: optString(o.env), host: optString(o.host), directory: optString(o.directory) };
  if (typeof o.app === "object" && o.app !== null) {
    const a = o.app as Record<string, unknown>;
    const port = typeof a.port === "number" && Number.isInteger(a.port) && a.port > 0 && a.port <= 65535 ? a.port : undefined;
    draft.app = { url: optString(a.url), name: optString(a.name), icon: optString(a.icon), group: optString(a.group), service: optString(a.service), port };
  }
  return draft;
}

/** The fragment carrying draft, "#draft=…". */
export const draftFragment = (draft: Draft) => `#draft=${toBase64url(JSON.stringify(draft))}`;
