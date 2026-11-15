import { Refresh, useQuery, type Command } from "@ddash/plugin";
import { NetworkService, type Device, type GetRemoteAccessResponse } from "@ddash/proto/network/network_pb";
import { useNavigate } from "react-router";
import { AddDeviceDialog } from "./add-device";
import { detailPath, nameOf, useDevices } from "./devices";

/**
 * The network Commands: each Device, going to its detail, found by its
 * name, address or MAC; and, while there is a Remote Access tunnel, Add a
 * Remote Access device, opening the Page's own Dialog.
 */
export function networkCommands(devices: readonly Device[], remote: GetRemoteAccessResponse | undefined, go: (to: string) => void): Command[] {
  const out: Command[] = devices.map((d) => ({
    id: `device:${d.mac}`,
    title: `Go to ${nameOf(d)}`,
    hint: `${d.adopted ? "Device" : "New Device"} · ${d.ip || d.mac}`,
    icon: d.adopted ? "monitor-smartphone" : "circle-question-mark",
    keywords: ["device", d.ip, d.mac, d.lease?.hostname ?? "", d.adopted ? "" : "new"].filter(Boolean),
    run: () => go(`/network/${detailPath(d)}`),
  }));
  if (remote && !remote.error) {
    out.push({
      id: "remote-access:add",
      title: "Add a Remote Access device",
      hint: remote.tunnel ? `WireGuard on ${remote.tunnel}` : "WireGuard",
      icon: "qr-code",
      keywords: ["vpn", "wireguard", "peer", "phone", "laptop"],
      sheet: (close) => <AddDeviceDialog unavailable={remote.addUnavailable} onClose={close} />,
    });
  }
  return out;
}

/**
 * The network Plugin's Commands for the command palette; undefined until
 * the Devices are listed (polled on every Page for the badge already).
 * Remote Access is read once, not polled: its Widget and Page poll it.
 */
export function useCommands(): Command[] | undefined {
  const { data } = useDevices();
  const remote = useQuery(NetworkService.method.getRemoteAccess, {}, { refresh: Refresh.NONE });
  const navigate = useNavigate();
  return data && networkCommands(data.devices, remote.data, (to) => void navigate(to));
}
