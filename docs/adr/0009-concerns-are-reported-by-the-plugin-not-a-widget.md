# Concerns are reported by the Plugin, not by a Widget

What needs the Operator reaches the Overview through one optional export on the Plugin's definition, `useConcerns`, a hook returning the Plugin's Concerns: each with a tone (down, warn, info), a subject, a title, a cause, whether it is quiet, and the Actions that resolve it. The core runs it for every installed Plugin while the Overview is open, in the Plugin's scope and behind an error boundary, whether or not any of the Plugin's Widgets is placed, and draws one list, "Needs you", worst first. An Action is the Plugin's own: a `run` (its `useAction`, with the confirm its Page asks) or a `sheet` the core draws in the Plugin's scope (its Plan, its form), so the core adds no second path for changes. We chose the Plugin because what needs the Operator belongs to the network, not to a card: a new Device needs adopting whether or not the Network Widget is placed, and a Plugin without any Widget must be able to say so.

## Considered Options

- **On each Widget, next to `useStatus`**: the Concerns would vanish with the card the Operator removed or never placed, and one Plugin with two Widgets would report the same thing twice.
- **Only for Plugins with a placed Widget**: cheaper (no hooks run for unplaced Plugins), but the list would then depend on the layout, which is the problem the list is meant to solve.
- **A `Status` with structured entries instead of a summary**: keeps one export, but ties Concerns to Widgets again and makes every Widget's status carry Actions.

## Consequences

- Every installed Plugin's `useConcerns` runs on the Overview, so its queries do too, at their own tiers.
- A Concern names its own Actions; the core can't offer one the Plugin didn't, nor run one without the Plugin's confirm or Plan.
- A `sheet` is called on every render while open, so it must return elements of stable components; the core keeps the Sheet as clicked open once its Concern goes away, so a Plan in flight isn't torn down when it fixed the cause.
- The Widgets' `Status` stays as it is: the hero still reads it until it takes its names from the Concerns.
