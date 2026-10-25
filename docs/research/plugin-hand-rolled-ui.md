# What UI the Plugins hand-roll instead of using @ddash/ui

Research for #71, under map #69 (modern UI redesign and Plugin components).

**Question.** Which repeated UI patterns in the six Plugins (`plugins/*/src`) and the shell (`web/src`) are built from raw Tailwind utilities rather than a `@ddash/ui` component, and how often? Which `@ddash/ui` components does each Plugin use today?

**Source.** The code at `origin/main` 9b5f92b. Every `file:line` below is in that tree. Test files (`*.test.tsx`, `testing.tsx`) are excluded. `@ddash/ui` means `packages/ui/src/index.tsx`.

## Summary

- `@ddash/ui` covers the atoms: StatusDot, Badge, Button, Stat, Segmented, Switch, Menu, Sparkline and UptimeBars. It also covers two overlays (Sheet, ConfirmDialog) and the page and Overview frames (PageHeader, WidgetCard, Hero). It has nothing for **layout or structure**. Every table, panel, section heading, fact list, form field, empty state, error line and filter control is hand-rolled.
- The six Plugins hold **8 data tables** with the same markup and **3 local copies of a Panel/Section component**, plus 8 more inline copies of that panel. There are **2 copies of a `Facts` key/value list**, **4 local form-field components** with **3 different input styles**, **4 local `Failed` error components**, about **30 hand-written error lines** and about **20 empty-state lines**.
- No Plugin has a **search**, **tabs** or a real **filter bar**. The only filters are one checkbox (network's "Blocked only") and the shell's Audit Log selects. So the explorer table in the redesign is new capability, not a migration.
- The copies have drifted: table cells use `px-2` or `px-3`, a header is sometimes missing `text-xs`, and only 2 of the 8 tables scroll sideways at phone width. "X unavailable" reads muted in some places and `text-down` in others. Inputs come in `h-9 bg-card`, `py-1.5 bg-background` and `h-7 text-xs` variants. A shared component removes all of this drift.
- The only **kicker label** (uppercase, tracked, muted) in the codebase is in the shell's Add Widget sheet (`web/src/overview.tsx:323`).

## 1. Which @ddash/ui components each Plugin uses

✓ means imported in at least one non-test source file of that Plugin. The file is named where it helps.

| @ddash/ui export | apps | checks | docker | network | proxmox | reverse-proxy | shell (`web/src`) |
|---|---|---|---|---|---|---|---|
| PageHeader | ✓ page | ✓ page | ✓ containers | ✓ network, device | ✓ workloads, detail | ✓ health | ✓ app, overview, catalogue, settings, audit-log |
| Button | ✓ page, edit | ✓ page, maintenance | | ✓ adopt | ✓ power | ✓ health | ✓ app, overview, catalogue, config-form |
| StatusDot | ✓ page, widget | ✓ page, detail, widget | ✓ containers | ✓ 6 files | ✓ workloads, detail | ✓ health, hosts, widget | ✓ app, boundary, catalogue, config-form |
| Badge | | ✓ page, detail, maintenance | ✓ containers | ✓ 5 files | ✓ workloads, detail, power | | ✓ app, audit-log |
| Stat | | ✓ widget, detail | ✓ widget | ✓ adguard, devices-widget | ✓ widget | | |
| Sheet | ✓ edit | ✓ page, detail, maintenance | | ✓ adopt | | | ✓ overview |
| Segmented | | ✓ page, detail, maintenance | | | | | ✓ overview |
| Switch | | | | ✓ adblocking, direct | | | ✓ settings, config-form |
| Menu | | | | | ✓ power | | |
| Sparkline | | | | ✓ adguard, throughput | | | |
| UptimeBars | | ✓ detail | | | | | |
| AppIcon | ✓ page, widget | | | | | | |
| WidgetCard, Hero | | | | | | | ✓ overview |
| ConfirmDialog | (through `useAction`'s `confirm`, which `packages/plugin/src/scope.tsx` renders) | | | | | | |
| helpers | `toneText`, `iconName` | | | `type Tone` | `type Tone` | | `cx`, `type Tone` |

No Plugin imports `cx`, `toneBg` or `dashboardIcon`. docker and reverse-proxy use the fewest components (four and three). network uses the widest set: nine components, every one except Segmented, Menu and UptimeBars.

## 2. Hand-rolled patterns, by kind

Counts are copies in source, not render sites.

### 2.1 Data tables (8 in Plugins, 1 in the shell)

They share one recipe. A `rounded-ui border border-border bg-card` wrapper or panel holds a `w-full text-sm` table. The header row is `border-b border-border text-left text-xs text-muted-foreground` with `font-medium` cells. The first column is a `w-6 py-2 pl-4` StatusDot, the second a `th scope="row"` with `font-medium`, then `px-2 py-2` muted cells, and `border-b border-border last:border-b-0` on each row.

| Table | Where | Notes |
|---|---|---|
| Checks | `plugins/checks/src/page.tsx:40-58`, row `:92-149` | status column, inline Badges in the name cell, row Actions as ghost Buttons |
| Containers (per Docker host) | `plugins/docker/src/containers.tsx:48-52`, row `:60-69` | no header row |
| Workloads | `plugins/proxmox/src/workloads.tsx:24-69`, row `:78-107` | the only one with `overflow-x-auto` besides Proxy Hosts; header lacks `text-xs` (`:26`) |
| Proxy Hosts | `plugins/reverse-proxy/src/hosts.tsx:81-98`, row `:110-136` | own `cell = "px-3 py-2 align-top"` (`:37`), not `px-2` |
| Devices | `plugins/network/src/devices-table.tsx:34-85` | own `cell = "px-2 py-2"` (`:25`) |
| Gateways | `plugins/network/src/network.tsx:97-131` | right-aligned numeric columns |
| Remote Access peers | `plugins/network/src/remote-access.tsx:53-107` | nested list in a cell |
| Queries | `plugins/network/src/queries.tsx:33-74` | own `cell = "px-2 py-1.5"` (`:15`), `pl-0`/`pr-0` edge cells (inside a panel body) |
| Config form repeated rows (shell) | `web/src/config-form.tsx:394` | editable; a different kind |

There are also **3 compact label/value tables** in Widgets: `plugins/docker/src/widget.tsx:48-54` with its row at `:62-69`, and `plugins/proxmox/src/widget.tsx:39-52`.

None of the tables has sorting, search, filters, tabs or a sticky header. 6 of 8 have no horizontal scroll, so at phone width the wide ones (Workloads has 11 columns) only work where `overflow-x-auto` was added by hand.

### 2.2 Panels: card sections with a header bar (3 local components, 8 inline copies)

The recipe: a `section aria-labelledby` with `rounded-ui border border-border bg-card`, a header of `flex items-center gap-2|3 border-b border-border px-4 py-3` around an `h2 font-semibold` and an optional aside, then the body.

- Local components with near-identical markup:
  - `Section` in `plugins/network/src/network.tsx:36-48`, used 6 times (`:19`, `:22`, `:25`, `:28`, `:54`, `:94`)
  - `Panel` in `plugins/network/src/device.tsx:57-70`, used 5 times (`:48`, `:88`, `:112`, `:135`, `:154`)
  - `Panel` in `plugins/proxmox/src/detail.tsx:51-63`, used twice (`:42`, `:86`)
- Inline header-bar copies:
  - per Docker host, `plugins/docker/src/containers.tsx:34-44`
  - Proxy Hosts, `plugins/reverse-proxy/src/hosts.tsx:65-77`
- Padded-body variant (`p-4` with an `h2 mb-3`, no bar):
  - Config health, `plugins/reverse-proxy/src/health.tsx:31-33`
  - Warnings, `plugins/reverse-proxy/src/hosts.tsx:47-49`
  - the Maintenance list, `plugins/checks/src/maintenance.tsx:65-68` (`px-4 py-3`, `h2 text-sm mb-2`)
  - Appearance in the shell, `web/src/settings.tsx:45-47`, which uses `font-medium` rather than `font-semibold`
- Headerless card around a table: `plugins/checks/src/page.tsx:40` and `plugins/proxmox/src/workloads.tsx:24`.

The network and device panels also pad each body by hand. Sections at `plugins/network/src/network.tsx:55-71`, `devices-table.tsx:30-32`, `remote-access.tsx:48-50,108`, `throughput.tsx:106,109` and `adguard.tsx:109,113` each add `px-4 py-3`.

### 2.3 Section headings without a card (2 local components, 4 inline)

- `Section` in `plugins/apps/src/page.tsx:73-83` (`h2 mb-3 font-semibold`), used 3 times (`:46`, `:51`, `:56`)
- `Section` in `plugins/checks/src/detail.tsx:73-83` (`h3 text-sm font-semibold`), used 3 times (`:17`, `:25`, `:45`)
- Sub-labels:
  - `plugins/network/src/adguard.tsx:91` (`h3 text-xs text-muted-foreground`)
  - `web/src/catalogue.tsx:207-208` (`h2 text-sm font-semibold`)
  - `web/src/config-form.tsx:60-61`
- Kicker label: `web/src/overview.tsx:323` (`text-xs font-semibold tracking-wide text-muted-foreground uppercase`), the only one in the codebase.

### 2.4 Key/value facts (2 copies)

The same `dl grid grid-cols-3 gap-x-4 gap-y-2` with `dt text-muted-foreground` and `dd col-span-2 tabular-nums`:

- `plugins/proxmox/src/detail.tsx:87-94`
- `Facts` in `plugins/network/src/device.tsx:73-84`, used 3 times (`:89`, `:114`, `:137`)

### 2.5 Metric rows around Stat (8 sites)

`Stat` exists, but each Plugin builds the row that holds it, either `flex gap-6` or `grid grid-cols-3 gap-3`:

- `plugins/checks/src/widget.tsx:30`
- `plugins/docker/src/widget.tsx:42`
- `plugins/proxmox/src/widget.tsx:34`
- `plugins/network/src/devices-widget.tsx:22`
- `plugins/network/src/adguard.tsx:60`, plus a `flex-wrap` variant at `:114`
- `plugins/checks/src/detail.tsx:18,27` (grid)

None has an icon chip or a tinted tile. That look is new.

### 2.6 Status lines and status lists (about 14)

A StatusDot, a `font-medium` name, muted detail and an `ml-auto` muted value on the right. This is the same shape as a Hero problem line (`packages/ui/src/index.tsx:236-243`).

- Lists:
  - `plugins/checks/src/widget.tsx:36-43`
  - `plugins/checks/src/detail.tsx:49-58`
  - `plugins/network/src/network.tsx:58-69`
  - `plugins/network/src/vpn.tsx:32-43`
  - `plugins/reverse-proxy/src/hosts.tsx:51-62`
  - `plugins/checks/src/maintenance.tsx:69-82`, which starts each line with a Badge rather than a dot
  - `plugins/network/src/adguard.tsx:92-99`, the "Top" name/count list
- Single lines:
  - `plugins/reverse-proxy/src/health.tsx:39-42,57-65`
  - `plugins/reverse-proxy/src/widget.tsx:19-22`
  - `plugins/network/src/adguard.tsx:55-58`
  - `plugins/apps/src/page.tsx:124-127`
  - `plugins/reverse-proxy/src/hosts.tsx:155-158`
- Inline dot and word inside facts: `plugins/proxmox/src/detail.tsx:70-74` and `plugins/network/src/device.tsx:95-98`.
- In the shell: `web/src/catalogue.tsx:176-180` and `web/src/config-form.tsx:119-123` (Health).

### 2.7 Empty states (about 20)

A bare `p text-sm text-muted-foreground`, padded `px-4 py-3` inside panels:

- apps: `page.tsx:43`, `widget.tsx:53`
- checks: `page.tsx:38`, `detail.tsx:33,47`
- proxmox: `detail.tsx:34`, `power.tsx:120`
- network:
  - `device.tsx:35,126,146,165`
  - `devices-table.tsx:32`
  - `queries.tsx:30-31`
  - `throughput.tsx:93,112`
  - `remote-access.tsx:100` (inline "nothing")
- reverse-proxy: `hosts.tsx:79`
- shell: `overview.tsx:344`, `audit-log.tsx:101`

Loading text ("Loading…", "…") is equally ad hoc: `web/src/app.tsx:158`, `web/src/overview.tsx:250`, `web/src/config-form.tsx:35`, `plugins/proxmox/src/power.tsx:110` and `plugins/checks/src/detail.tsx:20,33`.

### 2.8 Errors, warnings and notices (about 30 error lines, 4 local components)

- **Local `Failed` components**:
  - `plugins/apps/src/edit.tsx:61-68`
  - `plugins/network/src/adopt.tsx:22-29`
  - `plugins/proxmox/src/power.tsx:81-87`
  - `web/src/catalogue.tsx:247-253`, which wraps the shell's `ErrorFrame`
- **Inline error lines** (`text-down`, usually `text-sm`, sometimes `role="alert"`):
  - apps: `page.tsx:173`
  - checks: `page.tsx:37,115,210`, `detail.tsx:16`, `maintenance.tsx:84,164`
  - docker: `containers.tsx:22,46`
  - proxmox: `workloads.tsx:22`, `detail.tsx:24`
  - network:
    - `network.tsx:55,95`
    - `device.tsx:25`
    - `devices-table.tsx:30`
    - `remote-access.tsx:48`
    - `throughput.tsx:106`
    - `adguard.tsx:109`
    - `queries.tsx:28`
    - `adblocking.tsx:28`
    - `direct.tsx:22`
  - reverse-proxy: `health.tsx:21`
  - shell: `audit-log.tsx:136`
- **Component-unavailable lines that read muted instead**:
  - `plugins/apps/src/page.tsx:34,41`
  - `plugins/reverse-proxy/src/health.tsx:36`
  - `plugins/reverse-proxy/src/hosts.tsx:70-76`

  The same situation is shown in two different tones.
- **Warn lines** (`text-warn`):
  - `plugins/network/src/network.tsx:56`
  - `plugins/network/src/remote-access.tsx:50`
  - `plugins/network/src/throughput.tsx:110`
  - `plugins/network/src/vpn.tsx:30`
  - `plugins/network/src/adblocking.tsx:25`
- **Notice boxes**:
  - the discovery error at `plugins/apps/src/page.tsx:36-38` (`rounded-ui border bg-muted p-3`)
  - the shell's Unreachable banner at `web/src/app.tsx:69` (`border-warn`)
  - SetupHint at `web/src/config-form.tsx:60`
- `ErrorFrame` (`web/src/boundary.tsx:28-37`) is the shell's one error card, but it isn't in `@ddash/ui`, so Plugins can't use it.

### 2.9 Forms (4 local field components, 3 input styles)

- **Input class constants**:
  - `plugins/apps/src/edit.tsx:9`
  - `plugins/network/src/adopt.tsx:7` (adds `tabular-nums`)
  - `web/src/config-form.tsx:238`

  All three are `h-9 w-full rounded-ui border border-border bg-card px-3 text-sm`.
- **Other input and select styles**:
  - checks uses `rounded-ui border bg-background px-3 py-1.5` (no fixed height) at `plugins/checks/src/page.tsx:239` and `plugins/checks/src/maintenance.tsx:149`
  - apps has a compact `h-7 text-xs` select at `plugins/apps/src/page.tsx:152`
  - the shell has a `px-2 py-1` select at `web/src/audit-log.tsx:71`
- **Field wrappers** (label, control, help):
  - `Field` and `Text` in `plugins/apps/src/edit.tsx:11-46`
  - `Field` in `plugins/checks/src/page.tsx:227-243`
  - `Text` in `plugins/network/src/adopt.tsx:9-20`
  - `Labelled` in `plugins/checks/src/maintenance.tsx:181-188`
  - inline label-plus-Segmented at `plugins/checks/src/page.tsx:192-195,200-203`
  - an inline select field at `plugins/checks/src/maintenance.tsx:141-158`
  - in the shell, `Field` and the `fieldset` at `web/src/config-form.tsx:202,342`
- **Raw checkboxes**: `plugins/apps/src/edit.tsx:125-128` and `plugins/network/src/queries.tsx:24-27`. `ConfirmDialog` inside `@ddash/ui` uses a raw checkbox too.
- **Form layout** in the 6 sheet forms:
  - `flex flex-col gap-4` bodies at `apps/src/edit.tsx:116,167`, `checks/src/page.tsx:190`, `checks/src/maintenance.tsx:131` and `network/src/adopt.tsx:48,90`
  - an intro paragraph at `apps/src/edit.tsx:117`, `checks/src/maintenance.tsx:132` and `network/src/adopt.tsx:49,92`
  - a `flex gap-2` footer, with a destructive button pushed `ml-auto`, at `apps/src/edit.tsx:131,174`, `checks/src/page.tsx:214`, `checks/src/maintenance.tsx:168` and `network/src/adopt.tsx:68,97`
- **Help and footnote text** (`p text-xs text-muted-foreground`), 16 sites, for example:
  - `plugins/network/src/network.tsx:71`
  - `plugins/network/src/remote-access.tsx:108`
  - `plugins/network/src/throughput.tsx:120`
  - `plugins/network/src/queries.tsx:76`
  - `plugins/network/src/adguard.tsx:129`
  - `plugins/checks/src/page.tsx:206`

### 2.10 Filters, search, tabs, paging

- **Filters**: the "Blocked only" checkbox at `plugins/network/src/queries.tsx:24-27`. The shell's Audit Log has a filter bar of three labelled selects (`web/src/audit-log.tsx:43-52`, `Select` at `:63-81`).
- **Search**: none anywhere.
- **Tabs**: none. The nearest thing is the window switch at `plugins/checks/src/detail.tsx:24`, which uses `Segmented`.
- **Paging**: one "Older" button, a raw `<button>` rather than `Button`, at `web/src/audit-log.tsx:103-109`.

### 2.11 Lists and card grids

- **Card grid**: App cards at `plugins/apps/src/page.tsx:98-133`, each `li` a `rounded-ui border bg-card p-3`. Unmatched settings rows at `:148`. The Widget's icon grid at `plugins/apps/src/widget.tsx:58-73`.
- **Divided list** (`ul divide-y divide-border rounded-ui border bg-card`), shell only:
  - `web/src/settings.tsx:26`
  - `web/src/audit-log.tsx:91`
  - `web/src/catalogue.tsx:31,156,212`
- **Bordered item rows**: the Add Widget list at `web/src/overview.tsx:328`.
- **Radio cards**: the Theme picker at `web/src/settings.tsx:49-73`.
- **Disclosure rows** (a chevron that expands): `web/src/audit-log.tsx:152-158` and `web/src/catalogue.tsx:112-121`.
- **Pill chips** that duplicate `Badge`: Component chips at `web/src/catalogue.tsx:103`.

### 2.12 Smaller repeats

- **Back link** "← Proxmox" / "← Network" in `PageHeader` actions, identical at `plugins/proxmox/src/detail.tsx:15-19` and `plugins/network/src/device.tsx:16-20`. The two detail Pages also share the same not-found and error skeleton: `proxmox/src/detail.tsx:20-37` and `network/src/device.tsx:21-38`.
- **Row title links** (`hover:underline`):
  - `plugins/proxmox/src/workloads.tsx:85`
  - `plugins/network/src/devices-table.tsx:64`
  - `plugins/reverse-proxy/src/hosts.tsx:143,182`
  - `plugins/apps/src/workload.tsx:15`
  - a button styled as a link at `plugins/checks/src/page.tsx:98`
- **Code blocks** (`pre`, `rounded-ui bg-muted`):
  - `plugins/reverse-proxy/src/health.tsx:43`
  - `web/src/audit-log.tsx:174`
  - `web/src/config-form.tsx:67`
  - `web/src/boundary.tsx:35`
- **Modal**: the config dialog at `web/src/config-form.tsx:439-462` copies the overlay markup of `Sheet` and `ConfirmDialog` instead of reusing either.
- **Sidebar and Nav Entries**: `web/src/app.tsx:75-144`, shell only.

## 3. What this suggests for the component set (input to #69, not a decision)

These are the hand-rolled shapes that have at least two copies, the components that would absorb them, and their sites (counted from §2).

| Candidate | Replaces | Sites |
|---|---|---|
| **Panel** (title, optional aside or Actions, body padding, optional flush body for tables) | §2.2 | 3 local components (network ×2, proxmox) + 8 inline (checks 2, docker 1, proxmox 1, reverse-proxy 3, shell 1) |
| **DataTable / explorer table** (columns, a status column, row header, overflow at phone width; later search, filters and tabs) | §2.1, §2.10 | 8 Plugin tables + 3 compact Widget tables |
| **SectionHeading / Kicker** | §2.3 | 2 local components + 4 inline |
| **Facts** (key/value list) | §2.4 | 2 copies, 4 uses |
| **StatRow / metric tile** (icon chip, tone) | §2.5 | 8 rows |
| **StatusList / StatusLine** | §2.6 | about 14 |
| **EmptyState** and a **Notice / InlineError** with fixed tones | §2.7, §2.8 | about 20 empty states, about 30 errors, 4 local `Failed` |
| **Field**, **TextInput**, **Select**, **Checkbox**, **FormActions** | §2.9 | 4 local field components, 3 input styles, 2 raw checkboxes, 6 sheet forms |
| **FilterBar / SearchInput / Tabs** | §2.10 | new: today only network's checkbox and the shell's Audit Log |
| **BackLink** (or a `back` prop on PageHeader) | §2.12 | 2 |
| **Dialog** (a centred modal sharing Sheet's overlay) | §2.12 | 1 (shell) |
| **Moving `ErrorFrame` into @ddash/ui** | §2.8 | lets Plugins show a crash or failure card the way the shell does |

### Per-Plugin migration footprint (what each one would touch)

| Plugin | Tables | Panels / sections | Facts | Stat rows | Forms | Other |
|---|---|---|---|---|---|---|
| apps | none | `Section` ×3 (`page.tsx:73`) | none | none | `Field`/`Text` (`edit.tsx:11-46`), 2 forms, a raw checkbox, the compact select (`page.tsx:152`) | App card grid, discovery notice, `Failed` |
| checks | Checks (`page.tsx:40`) | the Maintenance list panel, `Section` ×3 (`detail.tsx:73`) | none | 3 (`widget.tsx:30`, `detail.tsx:18,27`) | `Field` (`page.tsx:227`), `Labelled` (`maintenance.tsx:181`), 2 forms | status lists ×3 |
| docker | Containers (`containers.tsx:48`), compact host table (`widget.tsx:48`) | per-host panel (`containers.tsx:34`) | none | 1 | none | none |
| network | Devices, Gateways, Remote Access, Queries | `Section` ×6 (`network.tsx:36`), `Panel` ×5 (`device.tsx:57`) | `Facts` ×3 | 2 | `Text` (`adopt.tsx:9`), 2 forms, a raw checkbox filter | `Failed`, status lists ×2, back link, about 12 error and warn lines |
| proxmox | Workloads (`workloads.tsx:24`), compact kind table (`widget.tsx:39`) | `Panel` ×2 (`detail.tsx:51`) | 1 | 1 | none | `Failed` (`power.tsx:81`), back link |
| reverse-proxy | Proxy Hosts (`hosts.tsx:81`) | Config health, Warnings, Proxy Hosts | none | none | none | code block, the muted unavailable notes |
| shell | config-form rows | Appearance | none | none | `config-form.tsx` fields and dialog, Audit Log selects | divided lists, disclosure rows, ErrorFrame, Sidebar, kicker |

## Method

I read every non-test `.tsx` under `plugins/{apps,checks,docker,network,proxmox,reverse-proxy}/src` and `web/src`, and `packages/ui/src/index.tsx`. Usage comes from each file's `from "@ddash/ui"` import. The patterns are grouped by markup shape, the class strings checked by hand, and the error, warn, empty and footnote counts cross-checked with `grep` for `text-down`, `text-warn`, `<p className="text-sm text-muted-foreground"` and `<p className="… text-xs text-muted-foreground"`. Counts marked "about" have borderline cases (for example, an error span inside a table cell).
