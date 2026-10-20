const units = ["B", "KiB", "MiB", "GiB", "TiB"];

/** A byte count in binary units, as Proxmox shows them: "512 B", "256 MiB", "1.0 GiB". */
export function bytes(n: bigint | number): string {
  let v = Number(n);
  let u = 0;
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024;
    u++;
  }
  return u === 0 || v >= 100 ? `${Math.round(v)} ${units[u]}` : `${v.toFixed(1)} ${units[u]}`;
}
