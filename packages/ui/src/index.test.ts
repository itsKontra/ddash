import { expect, it } from "vitest";
import * as ui from "./index";

it("exports exactly the spec's component set, plus cx, matches and the AppIcon helpers", () => {
  expect(Object.keys(ui).sort()).toEqual(
    [
      // atoms
      "Pill",
      "StatusDot",
      "Button",
      "Icon",
      "Skeleton",
      "AppIcon",
      "dashboardIcon",
      "iconName",
      // layout
      "PageTitle",
      "Panel",
      "SectionHeading",
      "HeroCard",
      "MetricTile",
      "WidgetCard",
      // forms
      "Field",
      "TextInput",
      "TextArea",
      "Select",
      "Checkbox",
      "Switch",
      "Segmented",
      "SearchField",
      "IconPicker",
      "AppIconPicker",
      "FormActions",
      // data
      "DataTable",
      "Tabs",
      "StatusList",
      "Facts",
      "useQueryState",
      "matches",
      // feedback
      "EmptyState",
      "Notice",
      "CodeBlock",
      "ErrorFrame",
      // charts
      "Ring",
      "Donut",
      "Bars",
      "Meter",
      "Sparkline",
      "UptimeBars",
      // overlays
      "Sheet",
      "Menu",
      "Dialog",
      "ConfirmDialog",
      "cx",
    ].sort(),
  );
});

// No component takes className: the type check fails if one does.
type Props<C> = C extends (...args: infer A) => unknown ? A[0] : unknown;
type TakesClassName = {
  [K in keyof typeof ui]: "className" extends keyof Props<(typeof ui)[K]> ? K : never;
}[keyof typeof ui];
const noClassName: [TakesClassName] extends [never] ? true : TakesClassName = true;
void noClassName;
