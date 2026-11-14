-- The settings of each disabled Public Hostname, kept while its ingress
-- rule and CNAME are gone, so Enable restores them.
CREATE TABLE disabled_public_hostname (
  hostname       TEXT NOT NULL,
  path           TEXT NOT NULL DEFAULT '',
  service        TEXT NOT NULL,
  -- The rule's originRequest JSON object; empty for none.
  origin_request TEXT NOT NULL DEFAULT '',
  -- Unix seconds.
  disabled_at    INTEGER NOT NULL,
  PRIMARY KEY (hostname, path)
);
