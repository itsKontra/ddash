import type { JsonObject, JsonValue } from "@bufbuild/protobuf";
import { FormField_Kind, type FormField } from "@ddash/proto/ddash/core/core_pb";

/**
 * The rules a config form checks before saving: required, format and
 * min/max. The host checks the same ones on save (core/config/validate.go);
 * core/config/testdata/formats.json holds the format cases both run.
 */

const formats: Record<string, { message: string; ok: (value: string) => boolean }> = {
  url: { message: "must be a URL like https://host.example", ok: isURL },
  hostname: { message: "must be a hostname like pve.home.example", ok: (s) => s.length <= 253 && hostname.test(s) },
  ip: { message: "must be an IP address", ok: (s) => parseIP(s) !== null },
  cidr: { message: "must be an address with a prefix length, like 198.18.1.0/24", ok: isCIDR },
  ip_range: { message: "must be a range of addresses, low to high, like 198.18.1.100-198.18.1.199", ok: isRange },
};

/** What is wrong with value in the format named format ("url", "ip", ...); "" when nothing is. */
export function checkFormat(format: string, value: string): string {
  const f = formats[format];
  if (!f) return `has an unknown format ${format}`;
  return f.ok(value) ? "" : f.message;
}

/**
 * The errors in a form's values, keyed by the host's paths ("peers[1].port").
 * values hold only what is set; secretsSet are the secrets stored and not
 * cleared.
 */
export function validate(fields: FormField[], values: JsonObject, secretsSet: string[]): Record<string, string> {
  const errors: Record<string, string> = {};
  check(fields, values, "", secretsSet, errors);
  return errors;
}

function check(fields: FormField[], values: JsonObject, prefix: string, secretsSet: string[], errors: Record<string, string>) {
  for (const f of fields) {
    const path = prefix + f.name;
    const value = values[f.name];
    if (f.repeated) {
      const list = Array.isArray(value) ? value : [];
      if (f.required && list.length === 0) errors[path] = "is required";
      list.forEach((item, i) => {
        if (f.kind === FormField_Kind.TABLE) {
          check(f.columns, isObject(item) ? item : {}, `${path}[${i}].`, [], errors);
        } else {
          const msg = checkScalar(f, item);
          if (msg) errors[`${path}[${i}]`] = msg;
        }
      });
      continue;
    }
    if (isEmpty(value)) {
      const set = f.default !== "" || (f.secret && secretsSet.includes(f.name));
      if (f.required && !set) errors[path] = "is required";
      continue;
    }
    const msg = checkScalar(f, value);
    if (msg) errors[path] = msg;
  }
}

function checkScalar(f: FormField, value: JsonValue): string {
  if (f.kind === FormField_Kind.STRING && f.format) return checkFormat(f.format, String(value));
  if (f.kind === FormField_Kind.INT) {
    if (!/^-?\d+$/.test(String(value).trim())) return "must be a whole number";
    const n = BigInt(String(value).trim());
    if (f.min !== undefined && n < f.min) return `must be at least ${f.min}`;
    if (f.max !== undefined && n > f.max) return `must be at most ${f.max}`;
  }
  return "";
}

const isEmpty = (v: JsonValue | undefined) => v === undefined || v === null || v === "";
const isObject = (v: JsonValue): v is JsonObject => typeof v === "object" && v !== null && !Array.isArray(v);

const hostname = /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*\.?$/;

function isURL(s: string): boolean {
  if (/\s/.test(s)) return false;
  try {
    const u = new URL(s);
    return u.protocol.length > 1 && u.host !== "";
  } catch {
    return false;
  }
}

/** An address as bytes: 4 for IPv4, 16 for IPv6. */
type Addr = number[];

const octet = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$/;

function parseIPv4(s: string): Addr | null {
  const parts = s.split(".");
  if (parts.length !== 4 || !parts.every((p) => octet.test(p))) return null;
  return parts.map(Number);
}

function parseIPv6(s: string): Addr | null {
  const halves = s.split("::");
  if (halves.length > 2) return null;
  const groups = (h: string) => (h === "" ? [] : h.split(":"));
  const head = groups(halves[0]);
  const tail = halves.length === 2 ? groups(halves[1]) : [];
  const bytes: number[] = [];
  const words = (parts: string[], last: boolean): number[] | null => {
    const out: number[] = [];
    for (const [i, p] of parts.entries()) {
      if (last && i === parts.length - 1 && p.includes(".")) {
        const v4 = parseIPv4(p);
        if (!v4) return null;
        out.push(...v4);
      } else if (/^[0-9A-Fa-f]{1,4}$/.test(p)) {
        const n = parseInt(p, 16);
        out.push(n >> 8, n & 0xff);
      } else {
        return null;
      }
    }
    return out;
  };
  const front = words(head, halves.length === 1);
  const back = words(tail, true);
  if (!front || !back) return null;
  if (halves.length === 1) return front.length === 16 ? front : null;
  if (front.length + back.length > 14) return null; // "::" stands for at least one group
  bytes.push(...front, ...Array.from({ length: 16 - front.length - back.length }, () => 0), ...back);
  return bytes;
}

function parseIP(s: string): Addr | null {
  return s.includes(":") ? parseIPv6(s) : parseIPv4(s);
}

function isCIDR(s: string): boolean {
  const parts = s.split("/");
  if (parts.length !== 2 || !/^(0|[1-9]\d{0,2})$/.test(parts[1])) return false;
  const addr = parseIP(parts[0]);
  return addr !== null && Number(parts[1]) <= addr.length * 8;
}

function isRange(s: string): boolean {
  const at = s.indexOf("-");
  if (at < 0) return false;
  const lo = parseIP(s.slice(0, at));
  const hi = parseIP(s.slice(at + 1));
  if (!lo || !hi || lo.length !== hi.length) return false;
  for (let i = 0; i < lo.length; i++) {
    if (lo[i] !== hi[i]) return lo[i] < hi[i];
  }
  return true;
}
