import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { WorkloadSchema } from "@ddash/proto/apps/apps_pb";
import { Kind, WorkloadRefSchema } from "@ddash/proto/ddash/workload/workload_pb";
import { Kind as GuestKind } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { expect, it } from "vitest";
import { ago, bytes, kindLabel, workloadPage, workloadText } from "./index";

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

it("says how long ago a time was, as of now", () => {
  const now = Date.parse("2026-10-20T12:00:00Z");
  const at = (iso: string) => timestampFromMs(Date.parse(iso));
  expect(
    [at("2026-10-20T11:59:30Z"), at("2026-10-20T11:58:00Z"), at("2026-10-20T09:00:00Z"), at("2026-10-16T12:00:00Z"), undefined].map((t) => ago(t, now)),
  ).toEqual(["<1 min ago", "2 min ago", "3 h ago", "4 d ago", "never"]);
});
