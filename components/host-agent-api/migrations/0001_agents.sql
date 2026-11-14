-- The Host Agents the Operator added, by their Workload's VMID. code is
-- the one-time enrolment code while the Agent isn't paired; token and
-- fingerprint are kept from pairing on (the token is the Agent's only
-- credential, so this DB is as secret as the config).
CREATE TABLE agent (
  vmid        INTEGER PRIMARY KEY,
  name        TEXT NOT NULL DEFAULT '',
  url         TEXT NOT NULL,
  code        TEXT NOT NULL DEFAULT '',
  token       TEXT NOT NULL DEFAULT '',
  fingerprint TEXT NOT NULL DEFAULT '',
  added       TEXT NOT NULL,
  paired      TEXT NOT NULL DEFAULT ''
);
