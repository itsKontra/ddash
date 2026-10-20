-- The last state a Notification was sent for, per item, so a restart
-- neither repeats a Notification nor misses a recovery. item is
-- "check:<id>" for a Check; an item without a row was never notified down.
CREATE TABLE notified (
  item   TEXT    PRIMARY KEY,
  state  TEXT    NOT NULL CHECK (state IN ('up', 'down')),
  -- Unix ms when the item entered state.
  since  INTEGER NOT NULL,
  -- Why it was down; empty when up.
  reason TEXT    NOT NULL DEFAULT ''
);
