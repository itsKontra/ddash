-- nginx-config's own SSH key pair, generated on first Start: the ed25519
-- seed. Exactly one row.
CREATE TABLE key (
  id   INTEGER PRIMARY KEY CHECK (id = 1),
  seed BLOB NOT NULL
);
