// What the Hosts Widget shows of the Docker hosts: plain functions over
// docker-api's and compose's answers.
import { ago } from "@ddash/display";
import type { Status } from "@ddash/plugin";
import { ImageState, type ListProjectsResponse } from "@ddash/proto/compose/compose_pb";
import { State, type Container } from "@ddash/proto/ddash/container/container_pb";
import type { HostContainers } from "@ddash/proto/docker-api/docker_api_pb";
import type { SummaryPart, SummaryRow } from "@ddash/ui";

/** Exited, or dead: stopped by itself or by someone, and not coming back on its own. */
export const exited = (c: Container) => c.state === State.EXITED || c.state === State.DEAD;

/** "5 of 6 containers running". */
export function containersRunning(hosts: readonly HostContainers[]): string {
  const all = hosts.flatMap((h) => h.containers);
  const running = all.filter((c) => c.state === State.RUNNING).length;
  return `${running} of ${all.length} ${all.length === 1 ? "container" : "containers"} running`;
}

/**
 * The image updates as one part of the line: how many wait, or that all
 * are up to date, with the age of the last check. Undefined without a
 * Docker Agent to check through.
 */
export function updateState(projects: ListProjectsResponse | undefined, now: number): string | undefined {
  if (!projects || projects.hosts.length === 0) return undefined;
  const last = projects.checks?.last;
  if (!last) return "images not checked yet";
  const stale = projects.hosts
    .flatMap((h) => [...h.projects.flatMap((p) => p.services.flatMap((s) => s.containers)), ...h.containers])
    .filter((c) => c.imageStatus?.state === ImageState.STALE).length;
  const what = stale ? `${stale} ${stale === 1 ? "update" : "updates"} waiting` : "all up to date";
  return `${what}, checked ${ago(last, now)}`;
}

/** The line under the headline: the exited containers in the down tone, the hosts that can't be read, and the update state. */
export function containersLine(hosts: readonly HostContainers[], update: string | undefined): SummaryPart[] {
  const parts: SummaryPart[] = [];
  const out = hosts.flatMap((h) => h.containers).filter(exited).length;
  if (out) parts.push({ text: `${out} exited`, tone: "down" });
  for (const h of hosts.filter((h) => h.error)) parts.push({ text: `${h.host} unreadable`, tone: "warn" });
  if (update) parts.push({ text: update });
  return parts;
}

/** The first n exited containers, each named with its host. */
export function exitedRows(hosts: readonly HostContainers[], n = 2): SummaryRow[] {
  return hosts
    .flatMap((h) => h.containers.filter(exited).map((c) => ({ tone: "down" as const, label: c.name, value: `exited on ${h.host}` })))
    .slice(0, n);
}

/**
 * Down while a container has exited, naming them; warn while a host can't
 * be read; up with the running count otherwise.
 */
export function hostsStatus(hosts: readonly HostContainers[]): Status {
  const out = hosts.flatMap((h) => h.containers).filter(exited);
  if (out.length) return { tone: "down", summary: `${out.length} exited: ${out.map((c) => c.name).join(", ")}` };
  const unreadable = hosts.filter((h) => h.error).map((h) => `${h.host} unreadable`);
  if (unreadable.length) return { tone: "warn", summary: unreadable.join(" · ") };
  return { tone: "up", summary: containersRunning(hosts) };
}
