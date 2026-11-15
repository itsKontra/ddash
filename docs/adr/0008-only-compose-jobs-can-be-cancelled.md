# Only Compose jobs can be cancelled

An Operator can cancel a Compose Project's run while it is in flight (`CancelRun`, a disruptive Action): the Docker Agent stops the Compose command its job runs, through the same context end and 10 s grace as a job's timeout, no later step of the run starts, and nothing is rolled back. The job and its running step end **cancelled**, a state of their own beside failed (and beside a timeout, which fails), and the run says who cancelled it and what it left half-done. No other job can be cancelled, neither a Host Agent's (a package upgrade, installing OpenSSH, reading the pending updates) nor an Agent's own update: the Agent refuses (`not_cancellable`, `FAILED_PRECONDITION` in ddash) and ddash offers no cancel for them. We chose this because a `docker compose` command stopped part-way leaves nothing that running it again doesn't mend, while `apt-get dist-upgrade`, `dnf upgrade` or `pacman -Syu` killed mid-transaction can leave the package database inconsistent, and an Agent killed mid-update can leave no Agent at all.

## Considered Options

- **Cancel for package upgrades too, behind a disruptive confirmation**: one rule for every job, but a confirmation doesn't make a half-applied transaction safe, and the Operator would need a shell on the host to repair it.
- **Cancel an upgrade only before its install step begins**: safe, but only the index refresh comes before it, so the button would be refused almost every time it is useful.

## Consequences

- An exception to ADR-0004, which rejected "a Cancel that stops a running Action between steps": a run outlasts its Action (ADR-0006), it is a Compose run only, and the Operator asks for the cancel, unlike a browser leaving.
- A cancelled job's `CollectJob` fails `CANCELED`, so the Action's Audit Log chain records the cancel, and the `CancelRun` entry records who.
- An Agent older than cancel answers 404 without an Error, which ddash reports as "update the Agent"; an Agent on another Release is refused before, as for every operation.
