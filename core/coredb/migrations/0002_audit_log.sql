-- The Audit Log: one row per hop of every Action, linked by chain_id. A row
-- is inserted when the gate admits the Action and completed when it ends;
-- outcome is NULL while it runs. Used only by appstate.
CREATE TABLE audit_entry (
  id          INTEGER PRIMARY KEY,
  time        TEXT NOT NULL,             -- RFC 3339, UTC, when admitted
  actor_kind  TEXT NOT NULL,             -- operator, component or core
  username    TEXT NOT NULL,             -- preferred_username or Component name
  sub         TEXT NOT NULL,             -- OIDC subject, Operators only
  chain_id    TEXT NOT NULL,
  target      TEXT NOT NULL,             -- a Component's name, or core
  method      TEXT NOT NULL,
  disruptive  INTEGER NOT NULL,
  request     TEXT NOT NULL,             -- protojson, redacted and truncated
  outcome     TEXT,                      -- ok, interrupted or a gRPC code
  message     TEXT NOT NULL DEFAULT '',  -- the error message of a failed one
  duration_ms INTEGER
);

CREATE INDEX audit_entry_time ON audit_entry (time);
CREATE INDEX audit_entry_chain ON audit_entry (chain_id);
