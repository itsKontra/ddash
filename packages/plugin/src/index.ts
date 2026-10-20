// @ddash/plugin: what a Plugin is written with. The shell maps it in the
// import map as a singleton, so every Plugin shares the shell's QueryClient.
export {
  definePlugin,
  type Badge,
  type NavEntry,
  type PluginDefinition,
  type Status,
  type Tone,
  type Widget,
  type WidgetSize,
} from "./define";
export { createQueryClient, refreshOf, Refresh, tiers, useQuery, type QueryOptions } from "./query";
export { PluginHost, PluginScope, useClient, usePlugin } from "./scope";
export { coreTransport, pluginHeader, pluginTransport, type Fetch, type PluginRef } from "./transport";
