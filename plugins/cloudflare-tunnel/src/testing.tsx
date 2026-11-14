import { create, toBinary, type DescMessage, type MessageInitShape } from "@bufbuild/protobuf";
import { base64Encode } from "@bufbuild/protobuf/wire";
import { showPlugin, streamed, type Answers, type Call } from "@ddash/plugin/testing";
import plugin from "./index";

/** The cloudflare-tunnel Plugin's scope in tests. */
export const scope = { name: "cloudflare-tunnel", requires: ["cloudflare-tunnel", "cloudflare-api"] };

const target = "c1744f8b-faa1-48a4-9e5c-02ac921467fa.cfargotunnel.com";

/** GetTunnel's answer (Connect JSON): home, up, with two connectors of two connections each. */
export const tunnel = {
  setUp: true,
  tunnel: {
    id: "c1744f8b-faa1-48a4-9e5c-02ac921467fa",
    name: "home",
    status: "TUNNEL_STATUS_HEALTHY",
    remotelyManaged: true,
    cnameTarget: target,
    connectors: [
      {
        id: "5d1c0f3a-8e2b-4c7d-9a61-3b2e4f5a6c01",
        version: "2025.10.0",
        arch: "linux_amd64",
        runAt: "2026-11-10T06:01:09.112Z",
        connections: [
          { id: "c1", coloName: "fra06", originIp: "203.0.113.7", openedAt: "2026-11-10T06:01:12.551Z" },
          { id: "c2", coloName: "ams08", originIp: "203.0.113.7", openedAt: "2026-11-10T06:01:12.733Z" },
        ],
      },
      {
        id: "7a2e9b4c-1d3f-4e5a-8b6c-9d0e1f2a3b02",
        version: "2025.9.1",
        arch: "linux_arm64",
        runAt: "2026-11-12T19:44:00.871Z",
        connections: [
          { id: "c3", coloName: "fra10", originIp: "203.0.113.7", openedAt: "2026-11-12T19:44:03.104Z" },
          { id: "c4", coloName: "ams01", originIp: "203.0.113.7", openedAt: "2026-11-12T19:44:03.390Z" },
        ],
      },
    ],
  },
};

/** ListPublicHostnames' answer: two enabled, one of them DNS only, and one disabled. */
export const listed = {
  setUp: true,
  remotelyManaged: true,
  catchAll: "http_status:404",
  zones: ["home.example"],
  cnameTarget: target,
  publicHostnames: [
    { fields: { hostname: "request.home.example", service: "http://198.18.1.212:5055" }, enabled: true, dns: "DNS_STATE_OK", zone: "home.example" },
    { fields: { hostname: "ha.home.example", service: "http://198.18.1.101:8123" }, enabled: true, dns: "DNS_STATE_NOT_PROXIED", zone: "home.example" },
    { fields: { hostname: "video.home.example", service: "http://198.18.1.212:8096" }, zone: "home.example" },
  ],
};

/** A Plan of two steps (Connect JSON). */
export const plan = {
  steps: [
    {
      system: "SYSTEM_CLOUDFLARE_TUNNEL",
      title: "Add the ingress rule for grafana.home.example",
      values: [
        { label: "Hostname", after: "grafana.home.example" },
        { label: "Service", after: "http://198.18.1.204:3000" },
      ],
    },
    { system: "SYSTEM_CLOUDFLARE_DNS", title: "Create the CNAME grafana.home.example", values: [{ label: "Target", after: target }, { label: "Proxied", after: "yes" }] },
  ],
};

/** A step's state as RunAction streams it (Connect JSON). */
export const step = { pending: "STEP_STATE_PENDING", running: "STEP_STATE_RUNNING", done: "STEP_STATE_DONE", failed: "STEP_STATE_FAILED" } as const;

/** RunAction's stream of a run through n steps: each step's state as it changes, then its result. */
export function ranThrough(n: number) {
  const states: string[][] = [];
  const steps: string[] = Array.from({ length: n }, () => step.pending);
  for (let i = 0; i < n; i++) {
    steps[i] = step.running;
    states.push([...steps]);
    steps[i] = step.done;
    states.push([...steps]);
  }
  return () => streamed([...states.map((s) => ({ steps: s })), { done: n, steps }]);
}

/** RunAction's stream ending in a Connect error with a detail after messages (none by default). */
export function failing<D extends DescMessage>(code: string, message: string, schema: D, detail: MessageInitShape<D>, messages: unknown[] = []) {
  const value = base64Encode(toBinary(schema, create(schema, detail)), "std_raw");
  return () => streamed(messages, { error: { code, message, details: [{ type: schema.typeName, value }] } });
}

/** Shows the Page with the Components answering as given (the fixtures above by default), settles and returns the calls. */
export function show(answers: Answers = {}) {
  return showPlugin(plugin, scope, "/cloudflare-tunnel", {
    answers: {
      GetTunnel: () => Response.json(tunnel),
      ListPublicHostnames: () => Response.json(listed),
      PlanAction: () => Response.json({ plan }),
      ...answers,
    },
  });
}

/** The bodies of the calls to method. */
export const bodies = (calls: Call[], method: string) => calls.filter((c) => c.method === method).map((c) => c.body);
