-- The Operator's display settings for discovered Apps, keyed by the first
-- server_name of their Proxy Host. Rows outlive their Proxy Host. Empty
-- text keeps the App's default.
CREATE TABLE overlay (
  server_name TEXT PRIMARY KEY,
  name        TEXT NOT NULL DEFAULT '',
  icon        TEXT NOT NULL DEFAULT '',
  group_name  TEXT NOT NULL DEFAULT '',
  hidden      INTEGER NOT NULL DEFAULT 0
);

-- Apps the Operator added by hand, e.g. UIs not behind nginx.
CREATE TABLE manual_app (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  url        TEXT NOT NULL,
  icon       TEXT NOT NULL DEFAULT '',
  group_name TEXT NOT NULL DEFAULT ''
);
