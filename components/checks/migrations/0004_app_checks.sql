-- App Checks join checks: kind 'app', type 'http', keyed by app_id. SQLite
-- can't widen a CHECK constraint, so checks is rebuilt. Migrations run in a
-- transaction with foreign keys on, where dropping checks would cascade into
-- its beats; so check_event and check_stat are rebuilt too, pointing at the
-- new table, and the old three are dropped children first. Renaming
-- checks_new to checks then rewrites the children's references.
CREATE TABLE checks_new (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  kind            TEXT    NOT NULL CHECK (kind IN ('manual', 'app')),
  name            TEXT    NOT NULL DEFAULT '',
  type            TEXT    NOT NULL CHECK (type IN ('tcp', 'icmp', 'dns', 'http')),
  target          TEXT    NOT NULL,
  dns_name        TEXT    NOT NULL DEFAULT '',
  dns_record_type TEXT    NOT NULL DEFAULT '',
  muted           INTEGER NOT NULL DEFAULT 0,
  disabled        INTEGER NOT NULL DEFAULT 0,
  state           TEXT    NOT NULL DEFAULT 'pending',
  failures        INTEGER NOT NULL DEFAULT 0,
  since           INTEGER NOT NULL,
  last_beat       INTEGER,
  latency_ms      REAL    NOT NULL DEFAULT 0,
  reason          TEXT    NOT NULL DEFAULT '',
  -- The App an App Check follows (apps' App id); NULL for a manual Check.
  app_id          TEXT    UNIQUE,
  -- An App Check is probed only while its App is shown (1) ...
  app_enabled     INTEGER NOT NULL DEFAULT 1,
  -- ... and present: Unix ms when its App left apps' set, NULL while there.
  gone_at         INTEGER,
  -- HTTPS: Unix ms when the certificate seen by the last up beat expires.
  cert_expires    INTEGER,
  CHECK ((kind = 'app') = (app_id IS NOT NULL))
);

INSERT INTO checks_new (id, kind, name, type, target, dns_name, dns_record_type, muted, disabled,
                        state, failures, since, last_beat, latency_ms, reason)
SELECT id, kind, name, type, target, dns_name, dns_record_type, muted, disabled,
       state, failures, since, last_beat, latency_ms, reason
FROM checks;

-- Keep AUTOINCREMENT's high-water mark, so a deleted Check's id is never reused.
UPDATE sqlite_sequence SET seq = (SELECT seq FROM sqlite_sequence WHERE name = 'checks') WHERE name = 'checks_new';
INSERT INTO sqlite_sequence (name, seq)
SELECT 'checks_new', seq FROM sqlite_sequence
WHERE name = 'checks' AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'checks_new');

CREATE TABLE check_event_new (
  id         INTEGER PRIMARY KEY,
  check_id   INTEGER NOT NULL REFERENCES checks_new (id) ON DELETE CASCADE,
  at         INTEGER NOT NULL,
  up         INTEGER NOT NULL,
  state      TEXT    NOT NULL,
  changed    INTEGER NOT NULL,
  latency_ms REAL,
  reason     TEXT    NOT NULL DEFAULT ''
);
INSERT INTO check_event_new SELECT id, check_id, at, up, state, changed, latency_ms, reason FROM check_event;

CREATE TABLE check_stat_new (
  check_id    INTEGER NOT NULL REFERENCES checks_new (id) ON DELETE CASCADE,
  period      TEXT    NOT NULL CHECK (period IN ('minute', 'hour', 'day')),
  bucket      INTEGER NOT NULL,
  up          INTEGER NOT NULL DEFAULT 0,
  down        INTEGER NOT NULL DEFAULT 0,
  maint       INTEGER NOT NULL DEFAULT 0,
  latency_avg REAL,
  latency_min REAL,
  latency_max REAL,
  PRIMARY KEY (check_id, period, bucket)
) WITHOUT ROWID;
INSERT INTO check_stat_new
SELECT check_id, period, bucket, up, down, maint, latency_avg, latency_min, latency_max FROM check_stat;

DROP TABLE check_event;
DROP TABLE check_stat;
DROP TABLE checks;
DELETE FROM sqlite_sequence WHERE name = 'checks';

ALTER TABLE checks_new RENAME TO checks;
ALTER TABLE check_event_new RENAME TO check_event;
ALTER TABLE check_stat_new RENAME TO check_stat;

CREATE INDEX check_event_by_check ON check_event (check_id, at);
CREATE INDEX check_event_by_age ON check_event (changed, at);
CREATE INDEX check_stat_by_age ON check_stat (period, bucket);
