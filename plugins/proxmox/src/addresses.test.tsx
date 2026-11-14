import { create } from "@bufbuild/protobuf";
import { cleanup, render, screen } from "@testing-library/react";
import { WorkloadSchema } from "@ddash/proto/proxmox-api/proxmox_api_pb";
import { afterEach, expect, it } from "vitest";
import { Addresses } from "./workloads";

afterEach(cleanup);

it("lists a Docker host's own addresses and only counts those on its Docker bridges", () => {
  const w = create(WorkloadSchema, {
    nics: [
      { name: "eth0", ips: ["198.18.1.201"] },
      { name: "docker0", ips: ["172.17.0.1"] },
      { name: "br-0c9e9f210078", ips: ["172.18.0.1", "fd00::1"] },
      { name: "veth554ff85", ips: [] },
    ],
  });
  render(<Addresses workload={w} />);
  expect(screen.getByText("198.18.1.201")).toBeTruthy();
  expect(screen.queryByText("172.17.0.1")).toBeNull();
  expect(screen.getByText("and 3 on Docker bridges")).toBeTruthy();
});
