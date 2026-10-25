import { showPlugin, type Answers } from "@ddash/plugin/testing";
import plugin from "./index";

/** The network Plugin's scope in tests. */
export const network = { name: "network", requires: ["network", "pfsense-api"] };

/** Its scope with AdGuard installed. */
export const withAdguard = { name: "network", requires: ["network", "pfsense-api", "adguard-api"] };

/** Shows the network Plugin at path (e.g. "/network") with its Components answering by method name, and returns the calls. */
export const show = (path: string, answers: Answers = {}, scope = network) => showPlugin(plugin, scope, path, { answers });
