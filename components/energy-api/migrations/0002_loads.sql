-- The Victron's loads per hour (#324): the house and heat pump, and the
-- use of the 5 minutes it has no readings for. Rows of algo 1 lack them
-- and are attributed again.
ALTER TABLE hour ADD COLUMN house     REAL NOT NULL DEFAULT 0;
ALTER TABLE hour ADD COLUMN heat_pump REAL NOT NULL DEFAULT 0;
ALTER TABLE hour ADD COLUMN unsplit   REAL NOT NULL DEFAULT 0;
