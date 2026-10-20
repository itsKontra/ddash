-- Operator sessions: one row per signed-in browser. The cookie holds a
-- random token and only its SHA-256 is stored here. Times are Unix
-- milliseconds. Used only by auth.
CREATE TABLE session (
  id        TEXT PRIMARY KEY,  -- hex SHA-256 of the cookie's token
  username  TEXT NOT NULL,     -- preferred_username
  sub       TEXT NOT NULL,     -- OIDC subject
  created   INTEGER NOT NULL,  -- when the Operator signed in
  last_seen INTEGER NOT NULL   -- the last authenticated request
);

CREATE INDEX session_last_seen ON session (last_seen);
