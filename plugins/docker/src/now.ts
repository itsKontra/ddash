import { useEffect, useState } from "react";

/** The time now in ms, ticking every 30 s, so "3 min ago" stays right without reading the clock while rendering. */
export function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);
  return now;
}
