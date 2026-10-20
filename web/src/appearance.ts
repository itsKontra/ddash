import { useQuery } from "@ddash/plugin";
import { CoreService, Theme } from "@ddash/proto/ddash/core/core_pb";
import { useEffect, useState } from "react";

/**
 * The look of the UI: the Theme is a core setting (CoreSettings), the same
 * in every browser; light or dark starts from the browser's preference and
 * a switch in Settings overrides it, remembered per browser.
 */

export type Scheme = "light" | "dark";

const storageKey = "ddash.scheme";
const darkQuery = "(prefers-color-scheme: dark)";

function stored(): Scheme | null {
  try {
    const v = window.localStorage.getItem(storageKey);
    return v === "light" || v === "dark" ? v : null;
  } catch {
    return null;
  }
}

const preferred = (): Scheme => (typeof window.matchMedia === "function" && window.matchMedia(darkQuery).matches ? "dark" : "light");

/** The scheme in use: the remembered switch, else the browser's preference. */
export const currentScheme = (): Scheme => stored() ?? preferred();

/** Puts .dark on <html> when the scheme in use is dark. */
export function applyScheme() {
  document.documentElement.classList.toggle("dark", currentScheme() === "dark");
}

/**
 * Applies the core's Theme and the scheme, and follows the browser's
 * preference while no switch is remembered. The shell runs it once.
 */
export function useAppearance() {
  const { data } = useQuery(CoreService.method.getCoreSettings, {});
  const theme = data?.settings?.theme ?? Theme.DDASH;
  useEffect(() => {
    document.documentElement.dataset.theme = themeName(theme);
  }, [theme]);
  useEffect(() => {
    applyScheme();
    if (typeof window.matchMedia !== "function") return;
    const media = window.matchMedia(darkQuery);
    media.addEventListener("change", applyScheme);
    return () => media.removeEventListener("change", applyScheme);
  }, []);
}

/** A Theme's name as <html data-theme> carries it: THEME_DDASH is "ddash". */
export const themeName = (t: Theme): string => (Theme[t] ?? "DDASH").toLowerCase();

/** The scheme in use and the switch that remembers another in this browser. */
export function useScheme(): [Scheme, (s: Scheme) => void] {
  const [scheme, setState] = useState(currentScheme);
  const set = (s: Scheme) => {
    try {
      window.localStorage.setItem(storageKey, s);
    } catch {
      // Not remembered; it still applies until the page reloads.
    }
    document.documentElement.classList.toggle("dark", s === "dark");
    setState(s);
  };
  return [scheme, set];
}
