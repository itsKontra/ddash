import { useEffect, useState } from "react";

/** How long a value must stop changing before useSettled hands it on, in ms. */
export const settleDelay = 300;

/**
 * value, once it stopped changing for settleDelay: what a server-side
 * search or a Plan is asked with, so typing a word makes one request and
 * not one per character. current is false while a newer value waits.
 * Values are compared as JSON (bigints as strings), so an object built
 * anew each render settles once.
 */
export function useSettled<T>(value: T): { settled: T; current: boolean } {
  const key = JSON.stringify(value, (_, v: unknown) => (typeof v === "bigint" ? v.toString() : v));
  const [settled, setSettled] = useState({ key, value });
  useEffect(() => {
    if (key === settled.key) return;
    const t = setTimeout(() => setSettled({ key, value }), settleDelay);
    return () => clearTimeout(t);
  }, [key, settled.key, value]);
  return { settled: settled.value, current: key === settled.key };
}
