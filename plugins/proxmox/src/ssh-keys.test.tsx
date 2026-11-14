import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { settle } from "@ddash/plugin/testing";
import { bodies, daysAgo, show } from "./testing";

afterEach(cleanup);

const laptop = {
  name: "laptop",
  publicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGx laptop",
  type: "ssh-ed25519",
  fingerprint: "SHA256:3f9Qh0c4b4Yp",
  created: daysAgo(2),
  generated: true,
};
const privateKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----";

it("lists the stored SSH Keys, and is linked from the Workloads Page", async () => {
  await show("/proxmox", { ListSSHKeys: () => Response.json({ keys: [laptop] }) });
  fireEvent.click(screen.getByRole("link", { name: "SSH Keys" }));
  await settle();

  expect(screen.getByRole("heading", { level: 1, name: "SSH Keys" })).toBeTruthy();
  const row = screen.getByRole("row", { name: /laptop/ });
  expect(row.textContent).toContain("SHA256:3f9Qh0c4b4Yp");
  expect(row.textContent).toContain("generated in ddash");
});

it("shows a generated key's private key once: closing the Dialog forgets it", async () => {
  const calls = await show("/proxmox/ssh-keys", {
    GenerateSSHKey: () => Response.json({ key: laptop, privateKey }),
  });

  fireEvent.click(screen.getByRole("button", { name: "Generate a key" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "laptop" } });
  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Generate" }));
  await settle();

  expect(bodies(calls, "GenerateSSHKey")).toEqual([{ name: "laptop" }]);
  const shown = screen.getByRole("dialog");
  expect(shown.textContent).toContain("Shown once: ddash doesn't keep it");
  expect(within(shown).getByRole("region", { name: "Private key" }).textContent).toBe(privateKey);
  expect(within(shown).getByRole("button", { name: "Download id_ed25519_laptop" })).toBeTruthy();

  fireEvent.click(within(shown).getByRole("button", { name: "Done" }));
  await settle();
  expect(screen.queryByText(/BEGIN OPENSSH PRIVATE KEY/)).toBeNull();

  // Generating again starts from a name: the old key is gone.
  fireEvent.click(screen.getByRole("button", { name: "Generate a key" }));
  expect(screen.getByRole("textbox", { name: "Name" })).toBeTruthy();
  expect(screen.queryByText(/BEGIN OPENSSH PRIVATE KEY/)).toBeNull();
});

it("downloads the private key as id_ed25519_<name>", async () => {
  await show("/proxmox/ssh-keys", { GenerateSSHKey: () => Response.json({ key: laptop, privateKey }) });
  const urls: Blob[] = [];
  URL.createObjectURL = (b: Blob) => {
    urls.push(b);
    return "blob:key";
  };
  URL.revokeObjectURL = () => {};
  let downloaded = "";
  const click = HTMLAnchorElement.prototype.click;
  HTMLAnchorElement.prototype.click = function (this: HTMLAnchorElement) {
    downloaded = this.download;
  };

  fireEvent.click(screen.getByRole("button", { name: "Generate a key" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "laptop" } });
  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Generate" }));
  await settle();
  fireEvent.click(screen.getByRole("button", { name: "Download id_ed25519_laptop" }));
  HTMLAnchorElement.prototype.click = click;

  expect(downloaded).toBe("id_ed25519_laptop");
  expect(await urls[0].text()).toBe(privateKey);
});

it("stores a pasted public key, and deletes a key once confirmed", async () => {
  const calls = await show("/proxmox/ssh-keys", { ListSSHKeys: () => Response.json({ keys: [laptop] }) });

  fireEvent.click(screen.getByRole("button", { name: "Paste a public key" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Name" }), { target: { value: "desktop" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Public key" }), { target: { value: "ssh-ed25519 AAAAdesk me@desktop\n" } });
  fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Add" }));
  await settle();
  expect(bodies(calls, "AddSSHKey")).toEqual([{ name: "desktop", publicKey: "ssh-ed25519 AAAAdesk me@desktop" }]);

  fireEvent.click(within(screen.getByRole("row", { name: /laptop/ })).getByRole("button", { name: "Delete" }));
  await settle();
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete" }));
  await settle();
  expect(bodies(calls, "DeleteSSHKey")).toEqual([{ name: "laptop" }]);
});
