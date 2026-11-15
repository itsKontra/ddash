-- The App's place among the Apps the Operator pinned to the Overview's
-- Apps Widget, from 1; 0 when it isn't pinned.
ALTER TABLE app ADD COLUMN pin INTEGER NOT NULL DEFAULT 0;
