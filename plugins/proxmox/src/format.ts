import { Kind } from "@ddash/proto/proxmox-api/proxmox_api_pb";

const units = ["B", "KiB", "MiB", "GiB", "TiB"];

/** Bytes in binary units: "256 MiB", "1.0 GiB". */
export function bytes(n: bigint | number): string {
  let v = Number(n);
  let u = 0;
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024;
    u++;
  }
  return u === 0 || v >= 100 ? `${Math.round(v)} ${units[u]}` : `${v.toFixed(1)} ${units[u]}`;
}

/** An uptime to its two largest units: "1 d 1 h", "12 min". */
export function uptime(seconds: bigint | number): string {
  const s = Number(seconds);
  const parts = [
    [Math.floor(s / 86400), "d"],
    [Math.floor((s % 86400) / 3600), "h"],
    [Math.floor((s % 3600) / 60), "min"],
  ] as const;
  const first = parts.findIndex(([n]) => n > 0);
  if (first < 0) return "< 1 min";
  return parts
    .slice(first, first + 2)
    .filter(([n]) => n > 0)
    .map(([n, unit]) => `${n} ${unit}`)
    .join(" ");
}

/** CPU use as a fraction of cpus: "1.3 % of 2 CPUs". */
export function cpu(fraction: number, cpus: number): string {
  return `${(fraction * 100).toFixed(1)} % of ${cpus} ${cpus === 1 ? "CPU" : "CPUs"}`;
}

export const kindLabel = (k: Kind) => (k === Kind.LXC ? "LXC" : k === Kind.VM ? "VM" : "?");
