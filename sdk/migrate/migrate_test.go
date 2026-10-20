package migrate_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/itsKontra/ddash/sdk/migrate"
)

func open(t *testing.T) *sql.DB {
	t.Helper()
	db, err := migrate.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var twoMigrations = fstest.MapFS{
	"0001_notes.sql":  {Data: []byte("CREATE TABLE note (id INTEGER PRIMARY KEY, text TEXT NOT NULL);")},
	"0002_author.sql": {Data: []byte("ALTER TABLE note ADD COLUMN author TEXT;")},
}

func TestApplyRunsEveryMigrationInOrder(t *testing.T) {
	db := open(t)

	if err := migrate.Apply(t.Context(), db, twoMigrations); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec("INSERT INTO note (text, author) VALUES ('hi', 'op')"); err != nil {
		t.Fatalf("schema after migrating: %v", err)
	}
}

func TestApplyIsANoOpOnAnUpToDateDB(t *testing.T) {
	db := open(t)
	if err := migrate.Apply(t.Context(), db, twoMigrations); err != nil {
		t.Fatal(err)
	}

	if err := migrate.Apply(t.Context(), db, twoMigrations); err != nil {
		t.Fatalf("second apply: %v", err)
	}
}

func TestApplyRunsOnlyNewMigrations(t *testing.T) {
	db := open(t)
	first := fstest.MapFS{"0001_notes.sql": twoMigrations["0001_notes.sql"]}
	if err := migrate.Apply(t.Context(), db, first); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO note (text) VALUES ('kept')"); err != nil {
		t.Fatal(err)
	}

	if err := migrate.Apply(t.Context(), db, twoMigrations); err != nil {
		t.Fatal(err)
	}

	var text string
	if err := db.QueryRow("SELECT text FROM note WHERE author IS NULL").Scan(&text); err != nil || text != "kept" {
		t.Fatalf("row after upgrade = %q, %v; want kept", text, err)
	}
}

func TestApplyRefusesADBWhoseSchemaIsAhead(t *testing.T) {
	db := open(t)
	if err := migrate.Apply(t.Context(), db, twoMigrations); err != nil {
		t.Fatal(err)
	}
	older := fstest.MapFS{"0001_notes.sql": twoMigrations["0001_notes.sql"]}

	err := migrate.Apply(t.Context(), db, older)

	if !errors.Is(err, migrate.ErrSchemaAhead) {
		t.Fatalf("err = %v, want ErrSchemaAhead", err)
	}
}

func TestAFailedMigrationLeavesTheDBAtThePreviousVersion(t *testing.T) {
	db := open(t)
	broken := fstest.MapFS{
		"0001_notes.sql":  twoMigrations["0001_notes.sql"],
		"0002_broken.sql": {Data: []byte("ALTER TABLE note ADD COLUMN author TEXT; THIS IS NOT SQL;")},
	}

	if err := migrate.Apply(t.Context(), db, broken); err == nil {
		t.Fatal("broken migration applied without error")
	}

	if err := migrate.Apply(t.Context(), db, twoMigrations); err != nil {
		t.Fatalf("retry with the fixed migration: %v", err)
	}
}

func TestApplyRejectsGapsInNumbering(t *testing.T) {
	db := open(t)
	gap := fstest.MapFS{
		"0001_notes.sql": twoMigrations["0001_notes.sql"],
		"0003_later.sql": twoMigrations["0002_author.sql"],
	}

	if err := migrate.Apply(t.Context(), db, gap); err == nil {
		t.Fatal("applied migrations with a gap in their numbering")
	}
}
