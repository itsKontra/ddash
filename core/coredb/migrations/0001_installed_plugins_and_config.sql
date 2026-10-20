-- The Plugins the Operator installed. Used only by runtime.
CREATE TABLE installed_plugin (
  name         TEXT PRIMARY KEY,
  release      TEXT NOT NULL,
  installed_at TEXT NOT NULL
);

-- Each owner's config: a Component's name, or "core". value is the owner's
-- config message in binary proto encoding, holding only what differs from
-- its defaults. Used only by config.
CREATE TABLE config (
  owner      TEXT PRIMARY KEY,
  value      BLOB NOT NULL,
  updated_at TEXT NOT NULL
);
