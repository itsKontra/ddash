-- The Overview layout: one row, its value a ddash.core.Layout in binary
-- proto encoding. Used only by overview.
CREATE TABLE overview_layout (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  value      BLOB NOT NULL,
  updated_at TEXT NOT NULL
);
