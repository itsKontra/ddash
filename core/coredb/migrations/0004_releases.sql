-- Each installed Plugin's plugin.yaml as its Release's catalogue.json last
-- listed it (runtime.PluginEntry as JSON), so installed Plugins show while
-- that catalogue can't be downloaded. Used only by runtime.
ALTER TABLE installed_plugin ADD COLUMN manifest TEXT NOT NULL DEFAULT '';

-- The Release each Component's DB was last opened by, and the one snapshot
-- of it kept from before the last Release change: db/snapshots/<file>,
-- taken from Release snapshot_release. Used only by runtime.
CREATE TABLE component_release (
  name             TEXT PRIMARY KEY,
  release          TEXT NOT NULL,
  snapshot_release TEXT NOT NULL DEFAULT '',
  snapshot_file    TEXT NOT NULL DEFAULT ''
);
