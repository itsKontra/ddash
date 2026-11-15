import { useAction } from "@ddash/plugin";
import { AdguardApiService, type GetStatusResponse } from "@ddash/proto/adguard-api/adguard_api_pb";
import { Button, Notice, Switch } from "@ddash/ui";

/** The pauses offered, in minutes: the first is the switch's. */
export const pauses = [10, 60] as const;

/** A pause's length, e.g. "10 min" or "1 h". */
export const pauseLabel = (minutes: number) => (minutes % 60 ? `${minutes} min` : `${minutes / 60} h`);

/**
 * AdGuard's protection switch: on, flipping it pauses protection for ten
 * minutes, and with durations the pauses sit beside it; paused or off,
 * flipping it or Resume turns protection back on. Both apply at once, no
 * confirm, audited. AdGuard ends a pause by itself; the time left is in
 * the status (protection's label). Nothing while AdGuard isn't running.
 */
export function ProtectionSwitch({ status, durations = true }: { status: GetStatusResponse; durations?: boolean }) {
  const pause = useAction(AdguardApiService.method.pauseProtection);
  const resume = useAction(AdguardApiService.method.resumeProtection);
  if (!status.running) return null;
  const on = status.protectionEnabled;
  const busy = pause.pending || resume.pending;
  const pauseFor = (minutes: number) => void pause.run({ duration: { seconds: BigInt(minutes * 60) } });
  const error = pause.error ?? resume.error;
  return (
    <div className="flex flex-col items-end gap-2">
      <span className="inline-flex items-center gap-1.5">
        {durations &&
          on &&
          pauses.map((m, i) => (
            <Button key={m} variant="ghost" size="sm" icon={i === 0 ? "pause" : undefined} aria-label={`Pause protection for ${pauseLabel(m)}`} disabled={busy} onClick={() => pauseFor(m)}>
              {pauseLabel(m)}
            </Button>
          ))}
        {!on && (
          <Button variant="outline" size="sm" disabled={busy} onClick={() => void resume.run({})}>
            {status.protectionPausedFor ? "Resume" : "Turn on"}
          </Button>
        )}
        <Switch label="AdGuard protection" checked={on} disabled={busy} onChange={(enabled) => (enabled ? void resume.run({}) : pauseFor(pauses[0]))} />
      </span>
      {error && <Notice tone="down">{error.rawMessage}</Notice>}
    </div>
  );
}
