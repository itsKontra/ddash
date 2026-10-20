-- Every Check, with what the Operator set and its last state, so a restart
-- resumes where it left off. kind is 'manual' for now; App Checks join it.
CREATE TABLE checks (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  kind            TEXT    NOT NULL CHECK (kind IN ('manual')),
  name            TEXT    NOT NULL DEFAULT '',
  type            TEXT    NOT NULL CHECK (type IN ('tcp', 'icmp', 'dns')),
  target          TEXT    NOT NULL,
  dns_name        TEXT    NOT NULL DEFAULT '',
  dns_record_type TEXT    NOT NULL DEFAULT '',
  muted           INTEGER NOT NULL DEFAULT 0,
  disabled        INTEGER NOT NULL DEFAULT 0,
  -- The last state: pending, up or down.
  state           TEXT    NOT NULL DEFAULT 'pending',
  -- Failed beats in a row.
  failures        INTEGER NOT NULL DEFAULT 0,
  -- Unix ms when the Check entered state.
  since           INTEGER NOT NULL,
  -- Unix ms of the last beat; NULL before the first.
  last_beat       INTEGER,
  latency_ms      REAL    NOT NULL DEFAULT 0,
  reason          TEXT    NOT NULL DEFAULT ''
);

-- One row per beat. up is the probe's result, state the Check's state
-- after it, changed whether the beat changed the state.
CREATE TABLE check_event (
  id         INTEGER PRIMARY KEY,
  check_id   INTEGER NOT NULL REFERENCES checks (id) ON DELETE CASCADE,
  at         INTEGER NOT NULL, -- Unix ms
  up         INTEGER NOT NULL,
  state      TEXT    NOT NULL,
  changed    INTEGER NOT NULL,
  latency_ms REAL,
  reason     TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX check_event_by_check ON check_event (check_id, at);
