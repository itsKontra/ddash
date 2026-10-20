import { create } from "@bufbuild/protobuf";
import { WorkloadSchema } from "@ddash/proto/apps/apps_pb";
import { Kind, WorkloadRefSchema } from "@ddash/proto/ddash/workload/workload_pb";
import { Kind as GuestKind } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { expect, it } from "vitest";
import { bytes, kindLabel, workloadPage, workloadText } from "./index";

it("names Workload kinds, proxmox-api's guest kinds alike", () => {
  expect([Kind.LXC, Kind.VM, Kind.DOCKER, Kind.EXTERNAL, Kind.UNSPECIFIED].map(kindLabel)).toEqual(["LXC", "VM", "Docker", "External", "Workload"]);
  expect([GuestKind.LXC, GuestKind.VM].map(kindLabel)).toEqual(["LXC", "VM"]);
});

it("reads a Workload as kind, name and state, falling back to its reference", () => {
  const ref = (kind: Kind, host: string, id: string) => create(WorkloadRefSchema, { kind, host, id });
  expect(workloadText(create(WorkloadSchema, { ref: ref(Kind.LXC, "pve", "102"), name: "media", state: "running" }))).toBe("LXC media · running");
  expect(workloadText(create(WorkloadSchema, { ref: ref(Kind.EXTERNAL, "198.18.1.5", "") }))).toBe("External 198.18.1.5");
});

it("finds the Page that shows a Workload", () => {
  const ref = (kind: Kind, id = "") => create(WorkloadRefSchema, { kind, host: "h", id });
  expect(workloadPage(ref(Kind.VM, "200"))).toEqual({ plugin: "proxmox", path: "workloads/200" });
  expect(workloadPage(ref(Kind.DOCKER, "sonarr"))).toEqual({ plugin: "docker", path: "" });
  expect(workloadPage(ref(Kind.EXTERNAL))).toBeUndefined();
});

it("counts bytes in binary units", () => {
  expect([512, 1024, 268_435_456, 1_073_741_824, 8_400_000].map(bytes)).toEqual(["512 B", "1.0 KiB", "256 MiB", "1.0 GiB", "8.0 MiB"]);
});
