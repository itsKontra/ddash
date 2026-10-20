import type { Badge, Status, Widget } from "@ddash/plugin";
import { StatusDot } from "@ddash/ui";
import { firstLine, ReadTime, useSnapshot } from "./snapshot";

/** Down while the read fails, with nginx's first error line; warn while nginx-config is unavailable. */
function useStatus(): Status | undefined {
  const { snapshot, error } = useSnapshot();
  if (!snapshot) return error ? { tone: "warn", summary: `nginx-config unavailable: ${error.rawMessage}` } : undefined;
  if (snapshot.error) return { tone: "down", summary: firstLine(snapshot.error) };
  return { tone: "up", summary: "config loads" };
}

/** Whether the config loads, and when it was last read. */
function Body() {
  const { snapshot } = useSnapshot();
  if (!snapshot) return null;
  return (
    <div className="flex flex-col gap-1 text-sm">
      <p className="flex items-center gap-2 font-medium">
        <StatusDot tone={snapshot.error ? "down" : "up"} />
        {snapshot.error ? "Config doesn't load" : "Config loads"}
      </p>
      {snapshot.readAt && (
        <p className="text-xs text-muted-foreground">
          {snapshot.error ? "Last good read " : "Read "}
          <ReadTime at={snapshot.readAt} />
        </p>
      )}
    </div>
  );
}

/** The Overview Widget: nginx's config health. */
export const nginx: Widget = {
  id: "nginx",
  title: "nginx",
  sizes: ["S"],
  defaultSize: "S",
  Body,
  useStatus,
};

/** "!" on the Nav Entry while the read fails. */
export function useBadge(): Badge | null {
  const { snapshot } = useSnapshot();
  return snapshot?.error ? { text: "!", tone: "down" } : null;
}
