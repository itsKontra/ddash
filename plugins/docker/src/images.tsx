import { ImageState, ProjectState, type ImageStatus, type Project, type ProjectContainer } from "@ddash/proto/compose/compose_pb";
import { Pill, type Tone } from "@ddash/ui";

/** A digest as Docker shows image ids: its first 12 hex digits. */
export const shortDigest = (d: string) => d.replace(/^sha256:/, "").slice(0, 12);

const imageLook: Record<ImageState, { tone: Tone; label: string }> = {
  [ImageState.UNSPECIFIED]: { tone: "idle", label: "not checked yet" },
  [ImageState.UNKNOWN]: { tone: "idle", label: "not checked yet" },
  [ImageState.UP_TO_DATE]: { tone: "up", label: "up to date" },
  [ImageState.STALE]: { tone: "warn", label: "update available" },
  [ImageState.NOT_CHECKED]: { tone: "idle", label: "not checked" },
  [ImageState.ERROR]: { tone: "warn", label: "check failed" },
};

/** Why an image reads as it does, for its title: the digests, or the reason. */
function why(s: ImageStatus): string {
  const parts: string[] = [];
  if (s.reason) parts.push(s.reason);
  if (s.currentDigest) parts.push(`running ${shortDigest(s.currentDigest)}`);
  if (s.latestDigest && s.latestDigest !== s.currentDigest) parts.push(`registry ${shortDigest(s.latestDigest)}`);
  return parts.join(" · ");
}

/** A container's image state as a Pill, its digests or reason in its title. Nothing for a stopped container. */
export function ImageBadge({ status }: { status?: ImageStatus }) {
  if (!status || (status.state === ImageState.UNKNOWN && status.reason === "not running")) return null;
  const l = imageLook[status.state];
  return (
    <span title={why(status)}>
      <Pill tone={l.tone}>{l.label}</Pill>
    </span>
  );
}

/** Every container of a Compose Project. */
export const containersOf = (p: Project): ProjectContainer[] => p.services.flatMap((s) => s.containers);

/** The containers whose image is stale. */
export const stale = (cs: readonly ProjectContainer[]) => cs.filter((c) => c.imageStatus?.state === ImageState.STALE);

const projectLook: Record<ProjectState, { tone: Tone; label: string }> = {
  [ProjectState.UNSPECIFIED]: { tone: "idle", label: "unknown" },
  [ProjectState.RUNNING]: { tone: "up", label: "running" },
  [ProjectState.PARTIAL]: { tone: "warn", label: "partly running" },
  [ProjectState.STOPPED]: { tone: "down", label: "stopped" },
  [ProjectState.NOT_STARTED]: { tone: "idle", label: "not started" },
};

/** A Compose Project's state as a Pill. */
export function ProjectStatePill({ project: p }: { project: Project }) {
  const l = projectLook[p.state];
  return (
    <Pill tone={l.tone} dot>
      {l.label}
    </Pill>
  );
}
