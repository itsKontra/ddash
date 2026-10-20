// Package coredb is the core DB: one SQLite file holding the core's own
// tables, migrated by the same runner the Components use.
package coredb

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/itsKontra/ddash/sdk/migrate"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open opens the core DB at path, creating it if needed, and applies the
// core's migrations.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := migrate.Open(path)
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(migrations, "migrations")
	if err == nil {
		err = migrate.Apply(ctx, db, sub)
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("core DB: %w", err)
	}
	return db, nil
}
