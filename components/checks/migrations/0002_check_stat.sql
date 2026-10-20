-- Each Check's beats summed per minute, hour and (UTC) day, for uptime and
-- UptimeBars. A daily sweep keeps minute buckets 24 h, hour buckets 30 d
-- and day buckets 365 d. Latency is over the bucket's up beats; beats in
-- Maintenance count only in maint.
CREATE TABLE check_stat (
  check_id    INTEGER NOT NULL REFERENCES checks (id) ON DELETE CASCADE,
  period      TEXT    NOT NULL CHECK (period IN ('minute', 'hour', 'day')),
  bucket      INTEGER NOT NULL, -- Unix ms of the bucket's start
  up          INTEGER NOT NULL DEFAULT 0,
  down        INTEGER NOT NULL DEFAULT 0,
  maint       INTEGER NOT NULL DEFAULT 0,
  latency_avg REAL,
  latency_min REAL,
  latency_max REAL,
  PRIMARY KEY (check_id, period, bucket)
) WITHOUT ROWID;

CREATE INDEX check_stat_by_age ON check_stat (period, bucket);

-- The daily sweep drops unchanged beats by age.
CREATE INDEX check_event_by_age ON check_event (changed, at);
