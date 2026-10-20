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

