// @ddash/plugin: what a Plugin is written with. The shell maps it in the
// import map as a singleton, so every Plugin shares the shell's QueryClient.
export {
  definePlugin,
  type Badge,
  type Concern,
  type ConcernAction,
  type ConcernTone,
  type NavEntry,
  type PluginDefinition,
  type Status,
  type Tone,
  type Widget,
  type WidgetSize,
} from "./define";
export { createQueryClient, isNotConfigured, refreshOf, Refresh, tiers, useQuery, type QueryClient, type QueryOptions } from "./query";
export { settleDelay, useSettled } from "./settled";
export { useTail, type Tail } from "./tail";
export { InstalledPlugins, usePluginLink } from "./link";
export {
  useAction,
  useStreamingAction,
  type Action,
  type ActionRequest,
  type ConfirmRequest,
  type RunOptions,
  type StreamingAction,
} from "./action";
export { CoreScope, PluginHost, PluginScope, useClient, useConfirm, usePlugin, type Ask } from "./scope";
export { confirmed, coreTransport, isDisruptive, pluginHeader, pluginTransport, type Fetch, type PluginRef } from "./transport";
export { planEnded, PlanNotices, PlanSteps, stepState, type PlanOutcome, type PlanStep, type StepState } from "./plan";
