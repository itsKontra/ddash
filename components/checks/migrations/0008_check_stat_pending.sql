-- A failed beat that leaves its Check pending (not yet down) counts in
-- pending, left out of uptime like maint. Buckets from before this
-- counted those beats in down and stay as they were.
ALTER TABLE check_stat ADD COLUMN pending INTEGER NOT NULL DEFAULT 0;
