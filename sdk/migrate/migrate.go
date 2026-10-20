// Package migrate opens SQLite files and applies embedded SQL migrations to
// them. Every Component's DB and the core DB go through it.
//
// Migrations are files named NNNN_description.sql, numbered from 0001 without
// gaps. The DB's PRAGMA user_version records how many have been applied.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrSchemaAhead means the DB was migrated by a newer Release than the code
// opening it. Roll back by restoring the snapshot, never by migrating down.
var ErrSchemaAhead = errors.New("schema is ahead of this Release")

// Open opens the SQLite file at path, creating it if needed, in WAL mode with
// foreign keys on and a busy timeout.
func Open(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return db, nil
}

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

// Apply runs the migrations in the root of fsys that db hasn't seen yet, in
// order, each in its own transaction. It returns ErrSchemaAhead when db has
// seen more migrations than fsys holds.
func Apply(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	files, err := list(fsys)
	if err != nil {
		return err
	}
	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current > len(files) {
		return fmt.Errorf("%w: DB is at migration %d, this Release knows %d", ErrSchemaAhead, current, len(files))
	}
	for i := current; i < len(files); i++ {
		if err := applyOne(ctx, db, fsys, files[i], i+1); err != nil {
			return err
		}
	}
	return nil
}

func list(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".sql" {
			continue
		}
		if !migrationName.MatchString(e.Name()) {
			return nil, fmt.Errorf("migration %s: want a name like 0001_create_notes.sql", e.Name())
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)
	for i, name := range files {
		n, _ := strconv.Atoi(migrationName.FindStringSubmatch(name)[1])
		if n != i+1 {
			return nil, fmt.Errorf("migration %s: want number %04d, migrations are numbered from 0001 without gaps", name, i+1)
		}
	}
	return files, nil
}

func applyOne(ctx context.Context, db *sql.DB, fsys fs.FS, name string, version int) error {
	body, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("migration %s: %w", name, err)
	}
	// PRAGMA takes no parameters; version is an int we computed.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return fmt.Errorf("migration %s: record version: %w", name, err)
	}
	return tx.Commit()
}
