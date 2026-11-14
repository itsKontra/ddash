# Notes

- Workspace lives in `docs/learn/` (user's choice), not the repo root.
- User is the ddash maintainer (git: itsKontra). Already wrote the ADRs, so teach the *code paths* behind them, not the decisions from scratch.
- Use the repo's GLOSSARY.md terms (Plugin vs Component, Action, Catalogue, Release). Never call a Component a "plugin".
- Workspace glossary: defer to the repo's GLOSSARY.md; add workspace-only terms (Lifecycle, Handshake, Endpoint, Broker) to docs/learn/GLOSSARY.md once the user can use them.
- Lesson plan: 1 two halves ✓, 2 handshake ✓, 3 reconcile ✓, 4 router ✓, 5 browser ✓, 6 releases ✓. Next: mixed interleaved review of 1–6, then learning records once the user reports results.
- Lesson 2 exercise surfaces: Components inherit the whole host env (go-plugin SkipHostEnv unset, subprocess.go:46 looks like it intends otherwise). Ask the user whether this is intended; follow up.
- Found while researching: doc comments in core/runtime/installed.go and reconcile.go have blank lines between `//` lines, which detaches them from their funcs. Mention to the user as a possible cleanup.
- User says 'finished/continue' without sharing quiz scores; no learning records written yet (no evidence). Ask for scores or misses at some point.
- Assets: course.css, quiz.js (MCQ), order.js (click-to-sequence drill). Reuse them.
- Findings surfaced to user (no code changed): env inheritance (L2), Down-from-Start never retried + timer-before-pass (L3), unaudited calls to down Components (L4), confirm only client-side (L5), rollback not offline (#66, fixed in 68a3af4) + one-generation snapshot vs multi-step rollback (L6).
