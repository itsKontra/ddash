import { Activity, Container, LayoutDashboard, LayoutGrid, Network, Puzzle, Server, Settings, Waypoints, type LucideIcon } from "lucide-react";

/**
 * The lucide icons the shell can draw, by name. Plugins are first-party, so
 * this lists exactly the icons their manifests name; add one here with a
 * new Plugin. Anything else draws as a puzzle piece.
 */
const icons: Record<string, LucideIcon> = {
  activity: Activity,
  container: Container,
  "layout-dashboard": LayoutDashboard,
  "layout-grid": LayoutGrid,
  network: Network,
  puzzle: Puzzle,
  server: Server,
  settings: Settings,
  waypoints: Waypoints,
};

export const icon = (name: string): LucideIcon => icons[name] ?? Puzzle;
