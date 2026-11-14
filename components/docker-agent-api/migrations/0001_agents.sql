-- The Docker Agents the Operator added, by the Docker host's name. code is
-- the one-time enrolment code while the Agent isn't paired; token and
-- fingerprint are kept from pairing on (the token is the Agent's only
-- credential, so this DB is as secret as the config).
CREATE TABLE agent (
  name        TEXT PRIMARY KEY,
  url         TEXT NOT NULL,
  roots       TEXT NOT NULL DEFAULT '',
  code        TEXT NOT NULL DEFAULT '',
  token       TEXT NOT NULL DEFAULT '',
  fingerprint TEXT NOT NULL DEFAULT '',
  added       TEXT NOT NULL,
  paired      TEXT NOT NULL DEFAULT ''
);
