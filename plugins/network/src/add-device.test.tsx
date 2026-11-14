import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { settle, showWidget, type Answers } from "@ddash/plugin/testing";
import { afterEach, expect, it, vi } from "vitest";
import { configFile } from "./add-device";
import plugin from "./index";
import { network, show as showNetwork } from "./testing";

afterEach(cleanup);

// What network answers GetRemoteAccess with.
const remoteAccess = {
  tunnel: "tun_wg1",
  interface: "REMOTEACCESS",
  peers: [{ name: "Phone", publicKey: "cGhvbmUtcGVlci1wdWJsaWMta2V5LWZpeHR1cmUtMDE=", addresses: ["198.18.40.2/32"] }],
};

const privateKey = "aGVyZS1pcy10aGUtZGV2aWNlcy1wcml2YXRlLWtleTE=";
const config = `[Interface]
PrivateKey = ${privateKey}
Address = 198.18.40.5/32
DNS = 198.18.1.53

[Peer]
PublicKey = L/711l8eD25p0ET+JXNg85+elMdhg/98adYt60nxxbk=
Endpoint = vpn.example.org:51820
AllowedIPs = 198.18.1.0/24, 198.18.40.0/24
PersistentKeepalive = 25
`;

// What network answers AddRemoteAccessPeer with.
const added = {
  peer: { name: "Anna's phone", publicKey: "YW5uYXMtcGhvbmUtcHVibGljLWtleS1maXh0dXJlLTE=", addresses: ["198.18.40.5/32"] },
  config,
};

const widget = () => plugin.widgets!.find((w) => w.id === "remote-access")!;

/** Shows the Remote Access Widget with network answering by method name, and returns the calls. */
async function show(answers: Answers = {}) {
  const { calls } = await showWidget(network, widget(), {
    answers: { GetRemoteAccess: () => Response.json(remoteAccess), AddRemoteAccessPeer: () => Response.json(added), ...answers },
  });
  return calls;
}

/** Opens the Add a device Dialog from where it shows and returns it. */
async function open(within_: HTMLElement = document.body) {
  fireEvent.click(within(within_).getByRole("button", { name: "Add a device" }));
  await settle();
  return screen.getByRole("dialog", { name: "Add a device" });
}

/** Names the device in the Dialog and adds it. */
async function add(dialog: HTMLElement, name: string) {
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Name" }), { target: { value: name } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Add" }));
  await settle();
}

it("adds a named device and shows its config once, as a QR code and as text", async () => {
  const calls = await show();

  const dialog = await open();
  const addButton = within(dialog).getByRole("button", { name: "Add" }) as HTMLButtonElement;
  expect(addButton.disabled).toBe(true);
  await add(dialog, "Anna's phone");

  expect(calls.filter((c) => c.method === "AddRemoteAccessPeer").map((c) => c.body)).toEqual([{ name: "Anna's phone" }]);
  const shown = screen.getByRole("dialog", { name: "Anna's phone is added" });
  expect(within(shown).getByRole("img", { name: "WireGuard config of Anna's phone as a QR code" })).toBeTruthy();
  expect(within(shown).getByRole("region", { name: "WireGuard config" }).textContent).toBe(config);
  expect(shown.textContent).toMatch(/Shown once/);
  expect(shown.textContent).toMatch(/ddash doesn't keep the private key/);
  expect(shown.textContent).toMatch(/198\.18\.40\.5\/32/);
});

it("forgets the config when the Dialog closes", async () => {
  await show();
  await add(await open(), "Anna's phone");

  fireEvent.click(screen.getByRole("button", { name: "Done" }));
  await settle();

  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.body.textContent).not.toContain(privateKey);
  // Opening it again starts over at the name.
  const again = await open();
  expect((within(again).getByRole("textbox", { name: "Name" }) as HTMLInputElement).value).toBe("");
  expect(document.body.textContent).not.toContain(privateKey);
});

it("draws the QR code with a quiet zone of 4 modules", async () => {
  await show();
  await add(await open(), "Anna's phone");

  const svg = screen.getByRole("img", { name: /as a QR code/ });
  const size = Number(svg.getAttribute("viewBox")!.split(" ")[2]);
  const modules = [...svg.querySelector("path")!.getAttribute("d")!.matchAll(/M(\d+) (\d+)/g)].map((m) => [Number(m[1]), Number(m[2])]);
  expect(modules.length).toBeGreaterThan(100);
  const xs = modules.map(([x]) => x);
  const ys = modules.map(([, y]) => y);
  expect(Math.min(...xs, ...ys)).toBe(4);
  expect(Math.max(...xs, ...ys)).toBe(size - 5);
});

it("downloads the config as a file named after the device", async () => {
  const saved: { name?: string; blob?: Blob } = {};
  vi.stubGlobal(
    "URL",
    class extends URL {
      static createObjectURL = (b: Blob) => ((saved.blob = b), "blob:config");
      static revokeObjectURL = () => {};
    },
  );
  const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
    saved.name = this.download;
  });
  await show();
  await add(await open(), "Anna's phone");

  fireEvent.click(screen.getByRole("button", { name: "Download" }));

  expect(click).toHaveBeenCalledOnce();
  expect(saved.name).toBe("anna-s-phone.conf");
  expect(await saved.blob!.text()).toBe(config);
  click.mockRestore();
});

it("names the file as WireGuard names an interface", () => {
  expect(configFile("Anna's phone")).toBe("anna-s-phone.conf");
  expect(configFile("  Living room tablet (old) ")).toBe("living-room-tab.conf");
  expect(configFile("✨")).toBe("remote-access.conf");
});

it("says why it can't add a device while network has no Remote Access endpoint", async () => {
  const why = "Add a device needs network's Remote Access endpoint, the firewall's public hostname: set it in network's config";
  await show({ GetRemoteAccess: () => Response.json({ ...remoteAccess, addUnavailable: why }) });

  const dialog = await open();
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Name" }), { target: { value: "Anna's phone" } });

  expect(dialog.textContent).toContain(why);
  expect((within(dialog).getByRole("button", { name: "Add" }) as HTMLButtonElement).disabled).toBe(true);
});

it("shows network's error and stays on the name when adding fails", async () => {
  await show({ AddRemoteAccessPeer: () => Response.json({ code: "failed_precondition", message: "the Remote Access subnet 198.18.40.0/24 is full" }, { status: 400 }) });

  const dialog = await open();
  await add(dialog, "Anna's phone");

  expect(screen.getByRole("dialog", { name: "Add a device" }).textContent).toMatch(/198\.18\.40\.0\/24 is full/);
  expect(screen.queryByRole("img", { name: /QR code/ })).toBeNull();
});

it("shows network's warning beside a config whose apply failed", async () => {
  const warning = "pfSense holds the peer, but applying it failed (pfSense answered 500): the device connects once WireGuard's changes are applied in pfSense (VPN > WireGuard).";
  await show({ AddRemoteAccessPeer: () => Response.json({ ...added, warning }) });

  await add(await open(), "Anna's phone");

  const shown = screen.getByRole("dialog", { name: "Anna's phone is added" });
  expect(shown.textContent).toContain(warning);
  expect(within(shown).getByRole("img", { name: /QR code/ })).toBeTruthy();
});

it("adds a device from the Remote Access section of the Network Page", async () => {
  const calls = await showNetwork("/network", { GetRemoteAccess: () => Response.json(remoteAccess), AddRemoteAccessPeer: () => Response.json(added) });

  await add(await open(screen.getByRole("region", { name: "Remote Access" })), "Anna's phone");

  expect(calls.filter((c) => c.method === "AddRemoteAccessPeer").map((c) => c.body)).toEqual([{ name: "Anna's phone" }]);
  expect(screen.getByRole("dialog", { name: "Anna's phone is added" })).toBeTruthy();
});
