# Mission: ddash's Plugin runtime

## Why
I maintain the ddash core. I need a working mental model of how Plugins and Components are installed, launched, connected and served, so I can change the loader and runtime without breaking the contract every Plugin relies on.

## Success looks like
- I can trace a Plugin from Catalogue install to a running Component process and a rendered Widget, naming the file that owns each step.
- I can state the minimum a Component and a Plugin must ship, and predict what fails when a piece is missing.
- I can judge whether a proposed core change breaks the host↔Component contract (handshake, Describe, descriptors, requires).
- I can explain why ADR 0001 and ADR 0002 chose what they chose, and what would have to change to revisit them.

## Constraints
- Learn from this repo's real code, ADRs and issues first; external docs only for the libraries it builds on (go-plugin, gRPC/Connect, import maps).
- Short lessons; one tangible win each.

## Out of scope
- Writing a polished user-facing Plugin UI (Tailwind/`@ddash/ui` design discipline).
- Release CI and the GitHub release pipeline, except where install depends on it.
