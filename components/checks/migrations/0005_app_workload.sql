-- The Workload behind an App Check's App, as apps declares it (kind ''
-- = none; always none for a manual Check).
ALTER TABLE checks ADD COLUMN workload_kind TEXT NOT NULL DEFAULT ''
  CHECK (workload_kind IN ('', 'lxc', 'vm', 'docker', 'external'));
ALTER TABLE checks ADD COLUMN workload_host TEXT NOT NULL DEFAULT '';
ALTER TABLE checks ADD COLUMN workload_id   TEXT NOT NULL DEFAULT '';
