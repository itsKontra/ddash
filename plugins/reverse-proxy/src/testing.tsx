import { renderScoped as render, type Answers } from "@ddash/plugin/testing";
import plugin from "./index";

/** The reverse-proxy Plugin's scope in tests. */
export const reverseProxy = { name: "reverse-proxy", requires: ["nginx-config", "apps", "checks"] };

/**
 * Renders children in the reverse-proxy Plugin's scope against fakes
 * answering by method name, with the installed Plugins named, and returns
 * the calls, the router starting at path. A method without an answer gets
 * 501 unimplemented.
 */
export const renderScoped = (children: React.ReactNode, answers: Answers, installed: string[] = ["reverse-proxy"], path = "/") =>
  render(reverseProxy, children, {
    answers,
    installed,
    path,
    unanswered: (method) => Response.json({ code: "unimplemented", message: method }, { status: 501 }),
  });

/** The reverse-proxy Page. */
export const Page = () => {
  const P = plugin.pages![0].Component!;
  return <P />;
};
