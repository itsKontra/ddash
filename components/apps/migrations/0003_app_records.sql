-- One record per App, under an opaque id that Checks key on, kept while
-- its URL changes. A discovered App is linked to its sources by their
-- ids; a Link-only App has none. Overlays and manual Apps become records
-- under the ids their Checks have; the first sync links them.
CREATE TABLE app (
  id            TEXT PRIMARY KEY,
  url           TEXT NOT NULL,
  -- Its further hostnames as last derived, one per line.
  aliases       TEXT NOT NULL DEFAULT '',
  -- Nginx Proxy Manager's id of its Proxy Host; 0 for none.
  proxy_host_id INTEGER NOT NULL DEFAULT 0,
  -- The Operator's settings; empty text keeps the default.
  name          TEXT NOT NULL DEFAULT '',
  icon          TEXT NOT NULL DEFAULT '',
  group_name    TEXT NOT NULL DEFAULT '',
  hidden        INTEGER NOT NULL DEFAULT 0,
  workload_kind TEXT NOT NULL DEFAULT ''
    CHECK (workload_kind IN ('', 'lxc', 'vm', 'docker', 'external')),
  workload_host TEXT NOT NULL DEFAULT '',
  workload_id   TEXT NOT NULL DEFAULT '',
  -- Added as Link only: a sync never removes it.
  link_only     INTEGER NOT NULL DEFAULT 0,
  -- A setting or an Ignore was ever set: kept as orphaned App settings
  -- when its sources disappear, rather than removed with them.
  touched       INTEGER NOT NULL DEFAULT 0,
  -- An overlay or manual App not yet through its first sync.
  migrated      INTEGER NOT NULL DEFAULT 0
);

-- The Warning kinds the Operator Ignores on an App, by WarningKind name.
CREATE TABLE app_ignore (
  app_id TEXT NOT NULL REFERENCES app (id) ON DELETE CASCADE,
  kind   TEXT NOT NULL,
  PRIMARY KEY (app_id, kind)
);

INSERT INTO app (id, url, name, icon, group_name, hidden, workload_kind, workload_host, workload_id, touched, migrated)
  SELECT 'proxy:' || server_name, 'https://' || server_name, name, icon, group_name, hidden,
         workload_kind, workload_host, workload_id, 1, 1
  FROM overlay;

INSERT INTO app (id, url, name, icon, group_name, link_only, touched, migrated)
  SELECT id, url, name, icon, group_name, 1, 1, 1 FROM manual_app;

DROP TABLE overlay;
DROP TABLE manual_app;
