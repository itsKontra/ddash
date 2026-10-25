import type { Badge, Status, Widget } from "@ddash/plugin";
import { Skeleton, StatusList } from "@ddash/ui";
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
  const { snapshot, error } = useSnapshot();
  // While nginx-config can't answer, the Widget's status says why.
  if (!snapshot) return error ? null : <Skeleton lines={2} />;
  return (
    <StatusList
      items={[
        {
          tone: snapshot.error ? "down" : "up",
          title: snapshot.error ? "Config doesn't load" : "Config loads",
          meta: snapshot.readAt && (
            <>
              {snapshot.error ? "Last good read " : "Read "}
              <ReadTime at={snapshot.readAt} />
            </>
          ),
        },
      ]}
    />
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
