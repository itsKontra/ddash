-- Maintenance in force, one per scope, so it survives a restart. scope is
-- the key ("everything", "check:<id>", "workload:<kind>:..."); the other
-- columns spell it out. Ended or expired Maintenance is deleted.
CREATE TABLE maintenance (
  scope         TEXT    PRIMARY KEY,
  kind          TEXT    NOT NULL CHECK (kind IN ('everything', 'check', 'workload')),
  -- kind 'check': the Check covered (deleted with it).
  check_id      INTEGER NOT NULL DEFAULT 0,
  -- kind 'workload': the Workload whose App Checks are covered.
  workload_kind TEXT    NOT NULL DEFAULT '',
  workload_host TEXT    NOT NULL DEFAULT '',
  workload_id   TEXT    NOT NULL DEFAULT '',
  started       INTEGER NOT NULL, -- Unix ms
  -- Unix ms when it ends by itself; NULL lasts until ended.
  ends          INTEGER,
  -- It also ends once every probed Check it covers is up since started.
  until_up      INTEGER NOT NULL DEFAULT 0
);

-- Whether a beat was in Maintenance (counted only in check_stat.maint).
ALTER TABLE check_event ADD COLUMN maint INTEGER NOT NULL DEFAULT 0;
