-- The items reported with ReportState (the nginx config, the VPN Tunnel),
-- in their last reported state, so a restart of checks alone keeps them.
-- item is "report:<caller's item>", as in notified.
CREATE TABLE reported (
  item   TEXT    PRIMARY KEY,
  name   TEXT    NOT NULL,
  state  TEXT    NOT NULL CHECK (state IN ('up', 'down')),
  -- Unix ms when the item entered state.
  since  INTEGER NOT NULL,
  reason TEXT    NOT NULL DEFAULT '',
  -- The path of ddash's UI a Notification's Click leads to.
  path   TEXT    NOT NULL DEFAULT ''
);

-- notified also remembers a Flapping Notification ('flapping': nothing
-- more until the item has been in one state for 30 min) and, for a
-- "cert:<id>" item, the certificate an expiry Notification was sent for
-- ('expiring', since = that certificate's expiry). It references nothing,
-- so it is rebuilt in place.
CREATE TABLE notified_new (
  item   TEXT    PRIMARY KEY,
  state  TEXT    NOT NULL CHECK (state IN ('up', 'down', 'flapping', 'expiring')),
  since  INTEGER NOT NULL,
  reason TEXT    NOT NULL DEFAULT ''
);
INSERT INTO notified_new (item, state, since, reason) SELECT item, state, since, reason FROM notified;
DROP TABLE notified;
ALTER TABLE notified_new RENAME TO notified;
