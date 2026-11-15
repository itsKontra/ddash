# Concerns are reported by the Plugin, not by a Widget

What needs the Operator reaches the Overview through one optional export on the Plugin's definition, `useConcerns`, a hook returning the Plugin's Concerns: each with a tone (down, warn, info), a subject, a title, a cause, whether it is quiet, and the Actions that resolve it. The core runs it for every installed Plugin, in the Plugin's scope and behind an error boundary, whether or not any of the Plugin's Widgets is placed, and draws one list, "Needs you", worst first. An Action is the Plugin's own: a `run` (its `useAction`, with the confirm its Page asks) or a `sheet` the core draws in the Plugin's scope (its Plan, its form), so the core adds no second path for changes. We chose the Plugin because what needs the Operator belongs to the network, not to a card: a new Device needs adopting whether or not the Network Widget is placed, and a Plugin without any Widget must be able to say so.

## Considered Options

- **On each Widget, next to `useStatus`**: the Concerns would vanish with the card the Operator removed or never placed, and one Plugin with two Widgets would report the same thing twice.
- **Only for Plugins with a placed Widget**: cheaper (no hooks run for unplaced Plugins), but the list would then depend on the layout, which is the problem the list is meant to solve.
- **A `Status` with structured entries instead of a summary**: keeps one export, but ties Concerns to Widgets again and makes every Widget's status carry Actions.

## Consequences

- Every installed Plugin's `useConcerns` runs on every Page, so its queries do too, at their own tiers: the command palette (Ctrl K) offers the loud Concerns' Actions on any Page, so the core collects them once for the whole shell rather than only while the Overview is open.
- A Concern names its own Actions; the core can't offer one the Plugin didn't, nor run one without the Plugin's confirm or Plan.
- A `sheet` is called on every render while open, so it must return elements of stable components; the core keeps the Sheet as clicked open once its Concern goes away, so a Plan in flight isn't torn down when it fixed the cause.
- A Plugin's Commands for the palette (`useCommands`) follow the same rules: each is a `run` or a `sheet`, the one its button on the Page has, drawn in the Plugin's scope on whatever Page the palette was opened, and a Command's `error` (why its last run failed) is shown by the shell there too, since the Page that would show it may not be mounted. Since `useCommands` runs on every Page too, it builds on the getters the Plugin polls there already (those of its Concerns and badge) and reads one only its Commands need once (`refresh: NONE`), leaving the polling to the Page.
- The Widgets' `Status` stays as it is: the hero still reads it until it takes its names from the Concerns.
