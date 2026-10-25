# Chart components in @ddash/ui: hand-rolled SVG or a library

Research for [#72](https://github.com/itsKontra/ddash/issues/72), a child of the map [#69](https://github.com/itsKontra/ddash/issues/69). Researched on 2026-10-25 against package registries (registry.npmjs.org, bundlephobia), the libraries' published sources (unpacked with `npm pack`), their official docs, and a size build with Vite 8.3.4. The question is which approach to take for three new charts: a progress ring, a donut with legend, and horizontal or stacked bars. They sit beside the existing `Sparkline` and `UptimeBars`.

## Answer in brief

**Recommendation: hand-roll all three in SVG inside `@ddash/ui`, the same way `Sparkline` and `UptimeBars` are built.** If later charts need arc padding or rounded arc corners, take `d3-shape`'s `arc()`/`pie()` (3.4 KiB gzip, no React) for the path maths only. Leave rendering, colour and accessibility in `@ddash/ui`.

Why:

- **Size.** Every page loads `@ddash/ui` as one vendor file, so a library behind it is paid by every page, chart or not. Recharts, the smallest full React chart library that covers all three charts, adds about 122 KiB gzip once tree-shaken to just these three charts. That is roughly twice react-router. Hand-rolled SVG costs a few hundred bytes per chart.
- **Theming.** Hand-rolled SVG can use the Theme's Tailwind utilities (`fill-up`, `stroke-primary`, `fill-muted`) like the existing charts, so light, dark and any Theme work with no extra code. Canvas libraries (Chart.js, uPlot, ECharts' default renderer) cannot read `var(--x)`. SVG libraries can, but they ship hard-coded light-mode default colours that would all have to be overridden.
- **Accessibility.** A ring is semantically a `meter` (one value) and a donut is a labelled image beside a legend. Hand-rolled SVG can say exactly that. Recharts' accessibility layer instead marks the chart `role="application"` and adds keyboard tooltip navigation, which is the wrong model for a single-value ring.
- **Fit.** There is no new dependency in the import-map singleton, no React peer-range risk, no extra React context or store, and nothing new for the style lint to allow.

What hand-rolling costs: no ready-made tooltips, axes or animation. These three charts need none of them. Use native SVG `<title>` hover text as `UptimeBars` does, a legend in HTML, and CSS transitions gated on `motion-safe:`. The decision should be revisited if `@ddash/ui` ever needs real cartesian charts with axes, ticks, zoom or crosshairs. Even then, the size cost argues for lazy-loading that chart and not baking a library into the singleton.

## 1. What the singleton and the lint imply

- `@ddash/ui` is one of the `importMapPackages` that the shell serves "as ESM singletons from /vendor/<release>/. Plugins and the shell never bundle them." (`packages/vite-preset/src/index.ts`). The vendor build (`web/vendor.config.ts`) emits one ESM file per package, with an `export *` entry for `@ddash/ui`. Anything `@ddash/ui` imports is therefore built into the `@ddash/ui` vendor output and fetched by every UI that imports `@ddash/ui`, which is all of them.
- Plugins would still never bundle a chart library if `@ddash/ui` wrapped it, so any library is technically compatible with the singleton. The real question is cost and control, not feasibility.
- The style lint (`packages/plugin-check/src/style.ts`) holds Plugins to "only @ddash/ui and Theme tokens": no hard-coded colours (hex, `rgb()`/`hsl()`/`oklch()`, palette utilities), no colour, fill or stroke `style` props, and no other design-system imports. It lists UI kits by name; chart libraries are not on the list today. If a Plugin imported a library directly, each Plugin would bundle its own copy, so the component must live in `@ddash/ui` either way.
- Theme tokens are CSS variables set on `:root` and `:root.dark` (`packages/ui/src/theme.css`) and exposed as inline Tailwind utilities (`packages/ui/src/tokens.css`: `@theme inline { --color-up: var(--up); ... }`). That is why `fill-up` and `stroke-primary` follow Theme and dark-mode changes "with no rebuild".
- The existing charts already follow this pattern. `Sparkline` is a `<svg role="img" aria-label>` with `<polyline className="stroke-primary">`. `UptimeBars` draws a `<rect className="fill-up">` per bucket with a `<title>` for hover (`packages/ui/src/index.tsx`).

## 2. Candidates (versions current on 2026-10-25, from registry.npmjs.org)

| Package | Version | Renders | React peer | Runtime deps |
|---|---|---|---|---|
| recharts | 3.10.1 | SVG | react, react-dom, react-is `^16.8 … ^19.0` | 11, incl. `@reduxjs/toolkit`, `react-redux`, `immer`, `reselect`, `es-toolkit`, `victory-vendor` (d3) |
| @visx/shape + @visx/group | 4.0.0 | SVG primitives | `^18.0.0 \|\| ^19.0.0` | `@visx/scale`, `@visx/curve`, `@visx/vendor` (d3), `classnames` |
| chart.js + react-chartjs-2 | 4.6.1 / 5.3.1 | Canvas | react `^16.8 … ^19.0` (wrapper) | `@kurkle/color` |
| uplot | 1.6.32 | Canvas | none (no React wrapper) | none |
| @nivo/pie, @nivo/bar | 0.99.0 | SVG (+canvas variants) | react `^16.14 … ^19.0` | `@nivo/core` (lodash, d3-*, `@react-spring/web`, …) |
| echarts | 6.1.0 | Canvas (SVG optional) | none (wrapper `echarts-for-react`) | `zrender`, `tslib` |
| apexcharts | 8.5.0 | SVG | none (wrapper `react-apexcharts`) | licence field: "SEE LICENSE IN LICENSE.md", not MIT |
| victory | 37.3.6 | SVG | react `>=16.6.0` | 27 `victory-*` packages |
| @unovis/react + @unovis/ts | 1.7.1 | SVG | `>=16.8 … ^19` | ~70, incl. d3, three, leaflet, maplibre-gl, `@emotion/css` |
| @tremor/react | 3.18.7 | Recharts underneath | react `^18.0.0` only | recharts, `@headlessui/react`, … |
| @mui/x-charts | 9.16.0 | SVG | needs `@mui/material` and `@emotion/*` | |
| d3-shape | 3.2.0 | path strings only | none | `d3-path` |

Ruled out early:

- `@mui/x-charts` needs `@mui/material` and Emotion, and `@mui/*` is a forbidden design system in the style lint.
- `@tremor/react` declares React `^18.0.0` only, which excludes React 19.3.1. It is also a whole UI kit (Headless UI) on top of Recharts.
- `@unovis/ts` pulls three, leaflet, maplibre-gl and `@emotion/css`.
- uPlot is "a fast, memory-efficient Canvas 2D-based chart for plotting time series, lines, areas, ohlc & bars" (uPlot README). It has no ring or donut.
- ApexCharts is not MIT-licensed (npm `license` field) and is 270 KiB gzip on bundlephobia.
- ECharts is 368 KiB gzip on bundlephobia for the full package.

## 3. Bundle size

Two measures. Bundlephobia gives the whole package's minified and gzipped size (its `/api/size` endpoint). The tree-shaken measure is a build of only what the three charts need, with React external, `vite@8.3.4` lib mode with minification, and gzip -9. Each entry imports a ring (where the library has one), a donut with legend, and stacked horizontal bars.

| Option | Bundlephobia (whole package) | Tree-shaken to these charts |
|---|---|---|
| Recharts 3.10.1 (`RadialBarChart`, `PieChart`, `BarChart`, `Legend`, axes) | 561.6 KiB min / 149.9 KiB gzip | **527.0 KiB min / 122.1 KiB gzip** |
| Nivo 0.99 (`ResponsivePie`, `ResponsiveBar`) | @nivo/pie alone 67.1 KiB gzip | 385.6 KiB min / 101.7 KiB gzip |
| Chart.js 4.6.1 + react-chartjs-2 (Arc, Bar, scales, Legend, Tooltip registered) | 69.6 KiB + 1.0 KiB gzip | 216.0 KiB min / 63.9 KiB gzip |
| visx 4.0 (`Pie`, `Bar`, `Group`) | @visx/shape 10.7 KiB gzip | 15.9 KiB min / 4.9 KiB gzip |
| d3-shape 3.2 (`arc`, `pie`) | 5.7 KiB gzip | 10.9 KiB min / 3.4 KiB gzip |
| Hand-rolled SVG (`Sparkline`- and `UptimeBars`-sized components) | n/a | well under 1 KiB gzip per chart (estimate from the existing ~30-line components) |

For scale, react-router 8.5.0, already a singleton, is 63.7 KiB gzip on bundlephobia. Recharts would roughly double what the vendor files add beyond React for every page load, including pages that draw no chart. Most of Recharts' weight is its state layer (Redux Toolkit, immer, reselect) and d3, so tree-shaking does not remove it.

## 4. Theming through CSS variables (light and dark)

- **SVG libraries accept `var(--x)` as a fill or stroke.** It is a presentation attribute, so the browser resolves it. shadcn/ui's charts are built on Recharts ("We use Recharts under the hood") and recommend CSS variables: "You can use css variables (recommended) or color values in any color format". For Recharts v3 they say to use `var(--chart-1)` directly, with dark values in a `.dark` block ([ui.shadcn.com/docs/components/chart](https://ui.shadcn.com/docs/components/chart)). Dark mode then works as it does for the rest of ddash.
- **But every SVG library brings its own default colours, and they are light-mode ones.** In Recharts 3.10.1's published source: axis ticks `'#666'` (`CartesianAxis.js`), legend inactive `'#ccc'` (`DefaultLegendContent.js`), tooltip background `'#fff'` and text `'#000'` (`DefaultTooltipContent.js`), pie stroke `'#fff'` and fill `'#808080'` (`Pie.js`), bar and radial-bar background `'#eee'` (`Bar.js`, `RadialBar.js`). Its `darkTheme.js` is marked `@experimental` and only sets the grid stroke. `@ddash/ui` would have to override each of these, and keep overriding them as the library adds defaults. Nivo is themed through its own JavaScript `theme` object, not CSS.
- **Canvas libraries cannot use CSS variables directly.** Chart.js documents colours as hex, RGB(A) or HSL(A) strings, or a `CanvasPattern`/`CanvasGradient` ([chartjs.org colors](https://www.chartjs.org/docs/latest/general/colors.html)); the page says nothing about custom properties. `@ddash/ui` would have to read each token with `getComputedStyle`, pass the resolved strings in, and re-render whenever the Theme or `.dark` changes. uPlot and ECharts' canvas renderer have the same limitation.
- **Hand-rolled SVG uses the Theme utilities as they are**, like `UptimeBars`' `toneFill` map (`fill-up`, `fill-down`, `fill-warn`, `fill-maint`, `fill-muted`) and `Sparkline`'s `stroke-primary`. No resolution step, no overrides, and a new Theme works with no code change.

Open point for the Theme work on the map: a donut "with legend" for arbitrary categories (not up/down/warn) needs more categorical colours than the Theme has today (`primary`, `up`, `down`, `warn`, `maint`, `muted`). That holds for any of the options above. The Theme would need something like shadcn's `--chart-1…n` series tokens, with light and dark values.

## 5. Accessibility

- **Recharts:** `accessibilityLayer` defaults to `true` in v3 (`state/rootPropsSlice.js`, `chart/PolarChart.js`). shadcn describes it as adding "keyboard access and screen reader support". In the source, the root surface then gets `role="application"` and `tabIndex=0` (`container/RootSurface.js`), and arrow keys move the tooltip between data points (`state/keyboardEventsMiddleware.js`). That suits a dense cartesian chart, but `application` is the wrong role for a progress ring, which WAI-ARIA models as a [`meter`](https://www.w3.org/TR/wai-aria-1.2/#meter) with `aria-valuenow`, `aria-valuemin` and `aria-valuemax`. Overriding means passing your own `role` and turning the layer off.
- **Reduced motion:** Recharts 3.10.1 defaults `isAnimationActive` to `'auto'` on `Bar`, `Pie` and `RadialBar`, which resolves to "not SSR and not `prefers-reduced-motion`" (`animation/JavascriptAnimate.js`, `animation/CSSTransitionAnimate.js`, `util/usePrefersReducedMotion.js`). That is a good default. Hand-rolled SVG gets the same with Tailwind's `motion-safe:` and `motion-reduce:` variants on a CSS transition (for example, a ring's `stroke-dashoffset`), with no JavaScript.
- **Chart.js (canvas):** "it is up to the user to create the canvas element in a way that is accessible". Accessibility "has to be added with ARIA attributes on the canvas element", through `role` plus `aria-label` or fallback content ([chartjs.org accessibility](https://www.chartjs.org/docs/latest/general/accessibility.html)). Nothing inside the chart is exposed to assistive technology.
- **visx and d3-shape** are primitives with no accessibility built in: the caller adds roles and labels, which is the same work as hand-rolling.
- **Hand-rolled SVG** can do exactly what each chart means:
  - Ring: `role="meter"`, an `aria-label`, and the value attributes, with the number printed in the centre as text.
  - Donut: `role="img"` with an `aria-label` summary. The legend is real HTML text with each value, so the data is never carried by colour alone.
  - Bars: `role="img"` with a label, or a list, plus a `<title>` per segment for hover, as `UptimeBars` already does.

## 6. React 19 compatibility

React 19.3.1 is the pinned peer of `@ddash/ui` (`packages/ui/package.json`).

- In range: Recharts (`^19.0.0` for react, react-dom and react-is, so `react-is` would become a new dependency), visx 4.0 (`^18 || ^19`), react-chartjs-2 (`^19.0.0`), Nivo (`^19.0`).
- Out of range: `@tremor/react` (`^18.0.0` only).
- Recharts 3 keeps its own Redux store in a private React context (`state/RechartsReduxContext.js`) and depends on `react-redux` and `use-sync-external-store`. This works under one shared React, but it adds a second state layer to the singleton.
- Hand-rolled SVG has no peer range to track. Plugins ship in lockstep with the core (ADR-0001), so a library upgrade would be a coordinated Release, but avoiding the dependency avoids the upgrade entirely.

## 7. Scoring

| | Size added to every page (gzip) | CSS-variable theming, light and dark | Accessibility fit | React 19 | Singleton fit |
|---|---|---|---|---|---|
| **Hand-rolled SVG** | < 1 KiB per chart | Native Theme utilities | Exact roles (`meter`, `img` + legend) | n/a | Nothing new |
| d3-shape for maths + own SVG | +3.4 KiB | Native | Exact | n/a | One tiny dependency |
| visx | +4.9 KiB | Native (className on primitives) | Caller's job | yes | Small dependency |
| Chart.js | +63.9 KiB | Must resolve tokens in JavaScript, re-render on theme change | Canvas: label only | yes | Fits, heavy |
| Nivo | +101.7 KiB | JavaScript theme object | Some props | yes | Fits, heavy |
| Recharts | +122.1 KiB | `var()` works; must override hard-coded light defaults | `role="application"` by default; good reduced motion | yes (+react-is) | Fits, heaviest; second state layer |

visx and d3-shape save little over hand-rolled code for these shapes. A ring is one `<circle>` with `pathLength={100}` and a `stroke-dasharray`. Stacked bars are `<rect>`s, as in `UptimeBars`. A donut is a ring with several dash segments, or roughly 20 lines of arc maths. Keep d3-shape in reserve for padded or rounded arcs, not as a starting dependency.

## Sources

- npm registry metadata: `https://registry.npmjs.org/<pkg>/latest` for every package in §2 (version, dependencies, peerDependencies, license).
- Bundlephobia: `https://bundlephobia.com/api/size?package=<pkg>` (whole-package min and gzip).
- Tree-shaken sizes: a local build with vite@8.3.4 lib mode, React external, gzip -9. The entries import only the components named in §3.
- Recharts 3.10.1 published source (`npm pack recharts@3.10.1`): `es6/state/rootPropsSlice.js`, `es6/container/RootSurface.js`, `es6/state/keyboardEventsMiddleware.js`, `es6/animation/JavascriptAnimate.js`, `es6/util/usePrefersReducedMotion.js`, `es6/theme/darkTheme.js`, plus the files listed in §4 for default colours.
- uPlot 1.6.32 README (from the npm tarball).
- Chart.js docs: [Colors](https://www.chartjs.org/docs/latest/general/colors.html), [Accessibility](https://www.chartjs.org/docs/latest/general/accessibility.html).
- shadcn/ui: [Chart](https://ui.shadcn.com/docs/components/chart).
- WAI-ARIA 1.2: [meter role](https://www.w3.org/TR/wai-aria-1.2/#meter).
- Repo: `packages/ui/src/index.tsx` (`Sparkline`, `UptimeBars`), `packages/ui/src/theme.css`, `packages/ui/src/tokens.css`, `packages/vite-preset/src/index.ts`, `web/vendor.config.ts`, `packages/plugin-check/src/style.ts`.
