-- A Not-proxied App's link to its own Host Override, by its host and
-- domain (pfSense has no stable id); empty domain for none.
ALTER TABLE app ADD COLUMN host_override_host TEXT NOT NULL DEFAULT '';
ALTER TABLE app ADD COLUMN host_override_domain TEXT NOT NULL DEFAULT '';
