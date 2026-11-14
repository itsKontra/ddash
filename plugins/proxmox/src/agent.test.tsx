import { cleanup, fireEvent, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { settle } from "@ddash/plugin/testing";
import { agents, bodies, show } from "./testing";

afterEach(cleanup);

const panel = (name: string) => screen.getByRole("region", { name });

const setup = {
  enrolmentCode: "K7QX-29FD-MV3P",
  installCommand: "curl -fsSL https://ddash.home.example/agent/host/install.sh | sudo sh -s -- --code K7QX-29FD-MV3P",
  scriptUrl: "https://ddash.home.example/agent/host/install.sh",
  checksumsUrl: "https://ddash.home.example/agent/host/SHA256SUMS",
  uninstallCommand: "curl -fsSL https://ddash.home.example/agent/host/install.sh | sudo sh -s -- --uninstall",
};

it("adds a Host Agent with the root-equivalent warning and its URL pre-filled, then shows the install command and what to verify", async () => {
  const calls = await show("/proxmox/workloads/101", {
    AddAgent: () => Response.json({ agent: { vmid: 101, state: "AGENT_STATE_ENROLLING" }, setup }),
  });

  const agent = panel("Host Agent");
  expect(agent.textContent).toContain("root-equivalent control");
  const url = within(agent).getByRole("textbox", { name: "Agent URL" }) as HTMLInputElement;
  expect(url.value).toBe("https://198.18.1.10:7444");

  fireEvent.click(within(agent).getByRole("button", { name: "Add Host Agent" }));
  await settle();

  expect(bodies(calls, "AddAgent")).toEqual([{ vmid: 101, name: "nginx", url: "https://198.18.1.10:7444" }]);
  expect(calls.find((c) => c.method === "AddAgent")?.url).toBe("/api/c/host-agent-api/ddash.hostagentapi.HostAgentApiService/AddAgent");
  expect(within(panel("Host Agent")).getByRole("region", { name: "Install command" }).textContent).toContain("--code K7QX-29FD-MV3P");
  expect(within(panel("Host Agent")).getByRole("link", { name: "install.sh" }).getAttribute("href")).toBe(setup.scriptUrl);
  expect(within(panel("Host Agent")).getByRole("link", { name: "SHA-256 sums" }).getAttribute("href")).toBe(setup.checksumsUrl);
});

it("keeps showing an enrolling Agent's install command, and pairs it on Pair now", async () => {
  const calls = await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_ENROLLING", { message: "waiting for the Agent at https://198.18.2.10:7444" })),
    GetAgentSetup: () => Response.json({ setup }),
  });

  const agent = panel("Host Agent");
  expect(within(agent).getByText("waiting to pair")).toBeTruthy();
  expect(within(agent).getByRole("region", { name: "Install command" }).textContent).toContain("K7QX-29FD-MV3P");
  // Updates and SSH need a paired Agent.
  expect(screen.queryByRole("region", { name: "Updates" })).toBeNull();

  fireEvent.click(within(agent).getByRole("button", { name: "Pair now" }));
  await settle();
  expect(bodies(calls, "PairAgent")).toEqual([{ vmid: 102 }]);
});

it("offers only Update Agent (and Remove) on an Agent of another Release", async () => {
  const calls = await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_OUTDATED", { release: "v0.0.1", message: "it runs Release v0.0.1 and ddash runs v0.0.3" })),
  });

  const agent = panel("Host Agent");
  expect(within(agent).getAllByRole("button").map((b) => b.textContent)).toEqual(["Update Agent", "Remove"]);
  expect(screen.queryByRole("region", { name: "Updates" })).toBeNull();

  fireEvent.click(within(agent).getByRole("button", { name: "Update Agent" }));
  await settle();
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Update Agent" }));
  await settle();
  expect(bodies(calls, "UpdateAgent")).toEqual([{ vmid: 102 }]);
});

it("pairs again, with a new code, when the Agent's certificate changed", async () => {
  const calls = await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(agents(102, "AGENT_STATE_FINGERPRINT_CHANGED", { message: "its certificate isn't the one pinned at pairing" })),
  });

  fireEvent.click(within(panel("Host Agent")).getByRole("button", { name: "Pair again" }));
  await settle();
  expect(bodies(calls, "PairAgent")).toEqual([{ vmid: 102, reset: true }]);
});

it("removes an Agent once confirmed and says how to uninstall it", async () => {
  let listed = agents(102, "AGENT_STATE_PAIRED");
  const calls = await show("/proxmox/workloads/102", {
    ListAgents: () => Response.json(listed),
    RemoveAgent: () => {
      listed = { agents: [] };
      return Response.json({ uninstall: [setup.uninstallCommand] });
    },
  });

  fireEvent.click(within(panel("Host Agent")).getByRole("button", { name: "Remove" }));
  await settle();
  fireEvent.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Remove" }));
  await settle();

  expect(bodies(calls, "RemoveAgent")).toEqual([{ vmid: 102 }]);
  expect(within(panel("Host Agent")).getByRole("region", { name: "Uninstall command" }).textContent).toBe(setup.uninstallCommand);
  // Ready to add it again.
  expect(within(panel("Host Agent")).getByRole("button", { name: "Add Host Agent" })).toBeTruthy();
});
