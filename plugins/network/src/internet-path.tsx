import { useAction } from "@ddash/plugin";
import { InternetPath, NetworkService, type Device, type ListDevicesResponse } from "@ddash/proto/network/network_pb";
import { Notice, Pill, Segmented } from "@ddash/ui";

type Choice = "Tunnelled" | "Direct" | "No Internet";

const pathOf: Record<Choice, InternetPath> = {
  Tunnelled: InternetPath.TUNNELLED,
  Direct: InternetPath.DIRECT,
  "No Internet": InternetPath.NO_INTERNET,
};

/** A Device's internet path by name; Tunnelled until network says otherwise. */
export function choiceOf(d: Device): Choice {
  if (d.internetPath === InternetPath.NO_INTERNET) return "No Internet";
  return d.internetPath === InternetPath.DIRECT ? "Direct" : "Tunnelled";
}

/** Where a Device's traffic goes, in a line: its path and the alias holding it. */
export function whereItGoes(d: Device, aliases: Pick<ListDevicesResponse, "bypassAlias" | "blockAlias">): string {
  switch (choiceOf(d)) {
    case "Direct":
      return `Direct, in ${aliases.bypassAlias}`;
    case "No Internet":
      return `No Internet, in ${aliases.blockAlias}`;
  }
  return "Tunnelled via the VPN Tunnel";
}

/** A Device's internet path, as a pill. */
export function PathPill({ device: d }: { device: Device }) {
  const choice = choiceOf(d);
  const tone = choice === "Tunnelled" ? "up" : choice === "Direct" ? "idle" : "warn";
  return <Pill tone={tone}>{choice}</Pill>;
}

/** The internet path picker's Action: it moves the Device at once, with no confirm. */
export const useSetInternetPath = () => useAction(NetworkService.method.setInternetPath);

/**
 * The internet path picker: Tunnelled, Direct, and No Internet once
 * network has a block alias. A pick moves the Device's address between the
 * aliases at once, with no confirm; on a New Device network Adopts it
 * first, so the path sticks to a stable address. It is busy while that
 * applies.
 */
export function InternetPathPicker({ device: d, blockAlias, action }: { device: Device; blockAlias: string; action: ReturnType<typeof useSetInternetPath> }) {
  const options: Choice[] = blockAlias ? ["Tunnelled", "Direct", "No Internet"] : ["Tunnelled", "Direct"];
  return (
    <Segmented
      label="Internet path"
      value={choiceOf(d)}
      options={options}
      busy={action.pending}
      onChange={(choice) => void action.run({ mac: d.mac, path: pathOf[choice] })}
    />
  );
}

/** The detail's internet path: the picker, where the Device is, and the refusal, if any. */
export function InternetPathField({ device: d, aliases }: { device: Device; aliases: Pick<ListDevicesResponse, "bypassAlias" | "blockAlias"> }) {
  const set = useSetInternetPath();
  return (
    <div className="flex flex-col gap-1">
      <InternetPathPicker device={d} blockAlias={aliases.blockAlias} action={set} />
      <span className="text-sm text-muted-foreground">{whereItGoes(d, aliases)}</span>
      {!d.adopted && <span className="text-xs text-muted-foreground">Picking Adopts it first, at the lowest free reserved address.</span>}
      {set.error && <Notice tone="down">{set.error.rawMessage}</Notice>}
    </div>
  );
}
