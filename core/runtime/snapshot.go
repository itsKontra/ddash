package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// switchRelease readies name's DB for Release rel before its process
// launches. On a Release change it either restores the snapshot taken from
// rel, moving the current DB aside first, or snapshots the current DB,
// keeping one generation. A DB moved aside is never deleted.
func (r *Runtime) switchRelease(ctx context.Context, name, rel string) error {
	var last, snapRelease, snapFile string
	err := r.opts.DB.QueryRowContext(ctx,
		`SELECT release, snapshot_release, snapshot_file FROM component_release WHERE name = ?`, name,
	).Scan(&last, &snapRelease, &snapFile)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = r.opts.DB.ExecContext(ctx, `INSERT INTO component_release (name, release) VALUES (?, ?)`, name, rel)
		return err
	case err != nil:
		return err
	case last == rel:
		return nil
	}

	db, dir := r.dbPath(name), r.snapshotDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if snapFile != "" && snapRelease == rel && exists(filepath.Join(dir, snapFile)) {
		aside, err := moveDB(db, dir, name, last)
		if err != nil {
			return fmt.Errorf("move %s's DB of Release %s aside: %w", name, last, err)
		}
		if err := renameDB(filepath.Join(dir, snapFile), db); err != nil {
			return fmt.Errorf("restore %s's snapshot of Release %s: %w", name, rel, err)
		}
		r.log.Info("restored snapshot", "component", name, "release", rel, "moved_aside", aside)
		snapRelease, snapFile = "", ""
	} else if exists(db) {
		file := freeName(dir, name, last)
		if err := copyDB(db, filepath.Join(dir, file)); err != nil {
			return fmt.Errorf("snapshot %s's DB of Release %s: %w", name, last, err)
		}
		if snapFile != "" {
			removeDB(filepath.Join(dir, snapFile)) // one generation
		}
		r.log.Info("snapshot", "component", name, "release", last, "file", file)
		snapRelease, snapFile = last, file
	}
	_, err = r.opts.DB.ExecContext(ctx,
		`UPDATE component_release SET release = ?, snapshot_release = ?, snapshot_file = ? WHERE name = ?`,
		rel, snapRelease, snapFile, name)
	return err
}

func (r *Runtime) snapshotDir() string { return filepath.Join(r.opts.DataDir, "db", "snapshots") }

// A DB is its SQLite file plus, unless it was closed cleanly, its WAL. The
// shared-memory file is rebuilt on open, so it is dropped.
var dbSuffixes = []string{"", "-wal"}

// freeName names a file in dir for name's DB of Release rel:
// <name>.<rel>.db, or <name>.<rel>.<n>.db when that is taken.
func freeName(dir, name, rel string) string {
	file := name + "." + rel + ".db"
	for n := 2; exists(filepath.Join(dir, file)); n++ {
		file = fmt.Sprintf("%s.%s.%d.db", name, rel, n)
	}
	return file
}

// moveDB moves the DB at db into dir under a free name for Release rel and
// returns that name, "" when there was no DB.
func moveDB(db, dir, name, rel string) (string, error) {
	if !exists(db) {
		return "", nil
	}
	file := freeName(dir, name, rel)
	return file, renameDB(db, filepath.Join(dir, file))
}

func renameDB(from, to string) error {
	_ = os.Remove(to + "-shm")
	_ = os.Remove(from + "-shm")
	for _, s := range dbSuffixes {
		if err := os.Rename(from+s, to+s); err != nil && (s == "" || !errors.Is(err, fs.ErrNotExist)) {
			return err
		}
	}
	return nil
}

func copyDB(from, to string) error {
	for _, s := range dbSuffixes {
		if err := copyFile(from+s, to+s); err != nil && (s == "" || !errors.Is(err, fs.ErrNotExist)) {
			return err
		}
	}
	return nil
}

func removeDB(db string) {
	for _, s := range dbSuffixes {
		_ = os.Remove(db + s)
	}
}

// copyFile copies from to to, through a temp file renamed into place.
func copyFile(from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(filepath.Dir(to), ".copy-*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, src)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), to)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

func exists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}
