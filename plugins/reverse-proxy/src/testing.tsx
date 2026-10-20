import { act, render } from "@testing-library/react";
import { createQueryClient, InstalledPlugins, PluginHost, PluginScope } from "@ddash/plugin";
import { vi } from "vitest";
import plugin from "./index";

export const settle = () => act(() => new Promise((r) => setTimeout(r, 50)));

export type Answers = Record<string, () => Response>;

/**
 * Renders children in the reverse-proxy Plugin's scope (requires
 * nginx-config, apps and checks) against fakes answering by method name,
 * with the installed Plugins named, and returns the methods called. A
 * method without an answer gets 501 unimplemented.
 */
export function renderScoped(children: React.ReactNode, answers: Answers, installed: string[] = ["reverse-proxy"]) {
  const calls: string[] = [];
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const method = String(input).split("/").pop()!;
    calls.push(method);
    return answers[method]?.() ?? Response.json({ code: "unimplemented", message: method }, { status: 501 });
  });
  render(
    <PluginHost client={createQueryClient()}>
      <InstalledPlugins names={installed}>
        <PluginScope plugin={{ name: "reverse-proxy", requires: ["nginx-config", "apps", "checks"] }} fetch={fetch}>
          {children}
        </PluginScope>
      </InstalledPlugins>
    </PluginHost>,
  );
  return calls;
}

/** The reverse-proxy Page. */
export const Page = () => {
  const P = plugin.pages![0].Component!;
  return <P />;
};
