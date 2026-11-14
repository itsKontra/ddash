-- The stale images already notified: one row per container (by
-- "<host>/<name>") and registry digest, so each new digest is notified
-- once, across restarts.
CREATE TABLE notified (
  container TEXT NOT NULL,
  digest    TEXT NOT NULL,
  at        TEXT NOT NULL,
  PRIMARY KEY (container, digest)
);
