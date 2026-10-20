-- The Operator's Workload override for a discovered App (kind '' = match
-- it from the upstream). Kinds are WorkloadRef's: lxc, vm, docker,
-- external; host and id as WorkloadRef holds them.
ALTER TABLE overlay ADD COLUMN workload_kind TEXT NOT NULL DEFAULT ''
  CHECK (workload_kind IN ('', 'lxc', 'vm', 'docker', 'external'));
ALTER TABLE overlay ADD COLUMN workload_host TEXT NOT NULL DEFAULT '';
ALTER TABLE overlay ADD COLUMN workload_id   TEXT NOT NULL DEFAULT '';
