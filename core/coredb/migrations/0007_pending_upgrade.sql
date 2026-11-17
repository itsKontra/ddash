-- The one-off upgrade steps the runtime still has to run, in Go, on its
-- next start with a catalogue; each row is deleted once its step ran.
-- Used only by runtime.
CREATE TABLE pending_upgrade (
  name TEXT PRIMARY KEY
);

-- Notification channels became Plugins (ADR-0010): a ddash that kept a
-- channel Component's config before has that channel's Plugin installed
-- for it, if the config names a destination.
INSERT INTO pending_upgrade (name)
SELECT 'channel-plugins' WHERE EXISTS (SELECT 1 FROM config WHERE owner IN ('ntfy-api', 'telegram-api'));
