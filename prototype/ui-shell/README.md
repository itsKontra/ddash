# PROTOTYPE: UI shell (throwaway)

Answers [UI shell: left nav from Nav Entries and the startpage Widget grid](https://github.com/itsKontra/ddash/issues/16).
Lives only on the `prototype/ui-shell` branch and is never merged.

    cd prototype/ui-shell && npm install && npm run dev

Then open `/?variant=A`, `B` or `C` (← / → or the bottom bar to switch).

- **A · Sidebar + sized grid:** labelled sidebar, Hero over a 4-column grid, Widgets declare S/M/L sizes, drag to arrange.
- **B · Icon rail + lanes:** icon rail with live badges, a fixed Status column, three lanes, Widgets have no sizes.
- **C · Grouped nav + summary stack:** Nav Entries grouped by category, one column of ExpandableCards, problems float up.

Core pages in every variant: Plugins (catalogue), Settings (Theme picker, light/dark) and UI kit (the proposed `@ddash/ui` set and tokens).
