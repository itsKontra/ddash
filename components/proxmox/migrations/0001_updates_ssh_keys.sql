-- The SSH Keys the Operator stored: public keys only. A generated key's
-- private half was handed out once and never written here.
CREATE TABLE ssh_key (
  name        TEXT PRIMARY KEY,
  public_key  TEXT NOT NULL,
  type        TEXT NOT NULL,
  fingerprint TEXT NOT NULL UNIQUE,
  created     TEXT NOT NULL,
  generated   INTEGER NOT NULL DEFAULT 0
);

-- A Workload's update settings, by VMID; a Workload without a row takes
-- a ddash Snapshot before each update.
CREATE TABLE update_setting (
  vmid     INTEGER PRIMARY KEY,
  snapshot INTEGER NOT NULL
);

-- The update running or last run on each Workload, as protojson, and the
-- Host Agent job it waits on (so a restart can collect it).
CREATE TABLE update_run (
  vmid INTEGER PRIMARY KEY,
  run  TEXT NOT NULL,
  job  TEXT NOT NULL DEFAULT ''
);

-- The last "Update all running", as protojson (one row, id 1).
CREATE TABLE bulk_update (
  id   INTEGER PRIMARY KEY,
  bulk TEXT NOT NULL
);
