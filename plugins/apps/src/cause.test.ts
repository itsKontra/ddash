import { create } from "@bufbuild/protobuf";
import { WorkloadSchema, type Workload } from "@ddash/proto/apps/apps_pb";
import { HostSchema, ProjectState, type Host } from "@ddash/proto/compose/compose_pb";
import { State } from "@ddash/proto/ddash/container/container_pb";
import { Kind } from "@ddash/proto/ddash/workload/workload_pb";
import { Kind as GuestKind, WorkloadPermissionsSchema, WorkloadSchema as GuestSchema } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { describe, expect, it } from "vitest";
import { exitCode, workloadCause, type WorkloadReads } from "./cause";

const workload = (kind: Kind, host: string, id: string, name: string, state: string): Workload => create(WorkloadSchema, { ref: { kind, host, id }, name, state });

const n8n = workload(Kind.LXC, "pve", "206", "n8n", "stopped");
const power = [create(WorkloadPermissionsSchema, { vmid: 206, privileges: ["VM.Audit", "VM.PowerMgmt"] })];
const guest = (status: string) => create(GuestSchema, { kind: GuestKind.LXC, vmid: 206, node: "pve", name: "n8n", status });

describe("a guest", () => {
  it("stopped is the cause, fixed by its Proxmox detail's Start", () => {
    expect(workloadCause(n8n, { permissions: power })).toEqual({ text: "LXC 206 n8n is stopped", fix: { kind: "guest", label: "Start LXC", vmid: "206" } });
    const vm = workload(Kind.VM, "pve", "110", "home-assistant", "stopped");
    expect(workloadCause(vm, { permissions: [create(WorkloadPermissionsSchema, { vmid: 110, privileges: ["VM.PowerMgmt"] })] })?.fix).toEqual({ kind: "guest", label: "Start VM", vmid: "110" });
  });

  it("Protected, or not known to be powerable yet, has no fix", () => {
    const audit = [create(WorkloadPermissionsSchema, { vmid: 206, privileges: ["VM.Audit"] })];
    expect(workloadCause(n8n, { permissions: audit })).toEqual({ text: "LXC 206 n8n is stopped" });
    expect(workloadCause(n8n, {})).toEqual({ text: "LXC 206 n8n is stopped" });
  });

  it("is read from proxmox-api first: started since apps' last sync, it is no cause", () => {
    expect(workloadCause(n8n, { guests: [guest("running")], permissions: power })).toBeUndefined();
    const running = workload(Kind.LXC, "pve", "206", "n8n", "running");
    expect(workloadCause(running, { guests: [guest("stopped")], permissions: power })?.text).toBe("LXC 206 n8n is stopped");
  });

  it("paused names its state, with no fix: its Page offers no Start", () => {
    expect(workloadCause(n8n, { guests: [guest("paused")], permissions: power })).toEqual({ text: "LXC 206 n8n is paused" });
  });
});

const paperless = workload(Kind.DOCKER, "apps", "paperless-webserver-1", "paperless-webserver-1", "exited");

function composeWith(state: State, status: string, project: { protected?: boolean; runId?: string } = {}): WorkloadReads {
  const container = (name: string, s: State, st: string) => ({ container: { name, state: s, status: st } });
  const hosts: Host[] = [
    create(HostSchema, {
      name: "apps",
      projects: [
        {
          host: "apps",
          name: "paperless",
          state: ProjectState.PARTIAL,
          ...project,
          services: [
            { name: "db", containers: [container("paperless-db-1", State.RUNNING, "Up 3 hours")] },
            { name: "webserver", containers: [container("paperless-webserver-1", state, status)] },
          ],
        },
      ],
    }),
  ];
  return { compose: hosts };
}

describe("a container", () => {
  it("exited names its Compose Project and exit code, fixed by the Project's Restart", () => {
    expect(workloadCause(paperless, composeWith(State.EXITED, "Exited (137) 6 minutes ago"))).toEqual({
      text: "Compose Project paperless exited (137)",
      fix: { kind: "project", host: "apps", project: "paperless" },
    });
    expect(workloadCause(paperless, composeWith(State.DEAD, "Dead"))?.text).toBe("Compose Project paperless is dead");
  });

  it("in a Protected Compose Project, or one running an Action, has no fix", () => {
    expect(workloadCause(paperless, composeWith(State.EXITED, "Exited (137) 6 minutes ago", { protected: true }))).toEqual({ text: "Compose Project paperless exited (137)" });
    expect(workloadCause(paperless, composeWith(State.EXITED, "Exited (0) 1 second ago", { runId: "r1" }))).toEqual({ text: "Compose Project paperless exited (0)" });
  });

  it("is read from compose first: restarted since apps' last sync, it is no cause", () => {
    expect(workloadCause(paperless, composeWith(State.RUNNING, "Up 2 seconds"))).toBeUndefined();
  });

  it("no Compose Project lists, e.g. behind a socket-proxy, is named alone and has no fix", () => {
    expect(workloadCause(paperless, {})).toEqual({ text: "Container paperless-webserver-1 exited" });
    expect(workloadCause(workload(Kind.DOCKER, "media", "grav", "grav", "paused"), {})).toEqual({ text: "Container grav is paused" });
  });

  it("paused or restarting names its state, with no fix", () => {
    expect(workloadCause(paperless, composeWith(State.RESTARTING, "Restarting (1) 2 seconds ago"))).toEqual({ text: "Compose Project paperless is restarting" });
  });
});

it("is none for a running Workload, none at all, or an external host", () => {
  expect(workloadCause(workload(Kind.LXC, "pve", "204", "grafana", "running"), { permissions: power })).toBeUndefined();
  expect(workloadCause(undefined, {})).toBeUndefined();
  expect(workloadCause(workload(Kind.EXTERNAL, "198.18.1.20", "", "198.18.1.20", ""), {})).toBeUndefined();
  expect(workloadCause(workload(Kind.LXC, "pve", "206", "", ""), {})).toBeUndefined();
});

it("reads Docker's exit code from a container's status", () => {
  expect(exitCode("Exited (137) 9 months ago")).toBe("137");
  expect(exitCode("Exited (0) Less than a second ago")).toBe("0");
  expect(exitCode("Up 3 hours")).toBeUndefined();
});
