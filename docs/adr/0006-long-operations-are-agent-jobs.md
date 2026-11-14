# Long operations are Agent jobs, followed past the Action

A pull, a package update or a backup takes minutes, past `ActionDeadline` (60 s). Such an operation runs as a **job** on its Agent (or as a Proxmox task): the Action starts it and streams its steps for up to 45 s, then returns while the job goes on; the Component that started it follows it in the background, under its own life rather than the request's, and a getter (compose's `ListRuns`, proxmox's runs) shows every step to whoever looks, from any tab. When the job ends, the Component collects it with a `CollectJob` Action in the starting Action's Audit Log chain, which fails with the job's error when the job failed, so the chain records how it really ended; a run that ended after its Action returned, or failed, also sends an "Action outcome" Notification. We chose this because ADR-0004's "an admitted Action runs to its end" can't hold an operation that outlasts the deadline, and a closed tab must never leave a half-done update unreported.

## Considered Options

- **A longer deadline for some Actions**: the host's shutdown waits for every admitted Action, so a 20-minute pull would hold a restart of ddash for 20 minutes.
- **Amending the Action's own Audit Log entry**: needs a core API for Components to write the Audit Log; a chained `CollectJob` entry records the same outcome with what exists.

## Consequences

- Extends ADR-0004, which rejected background jobs "for Actions that last seconds": these last minutes.
- A job lives in its Agent's memory. When ddash restarts mid-job, the job goes on and stays listed until collected; its collect then runs in a new chain. When the Agent restarts mid-job, the job is lost and the run says so.
- One run per Compose Project (one job per target on an Agent) at a time.
