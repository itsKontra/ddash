// Package overview keeps the Overview layout: which Widgets are placed, in
// what order and at what size, and which Widgets the shell has seen. The
// shell reconciles it against the installed Plugins' Widgets; the core only
// stores it.
package overview

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	corepb "github.com/itsKontra/ddash/proto/ddash/core"
)

// Store keeps the layout in the core DB.
type Store struct{ db *sql.DB }

// New is the layout kept in db, the core DB.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Get returns the saved layout, or an empty one before the first save.
func (s *Store) Get(ctx context.Context) (*corepb.Layout, error) {
	var value []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM overview_layout WHERE id = 1`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return &corepb.Layout{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read layout: %w", err)
	}
	layout := &corepb.Layout{}
	if err := proto.Unmarshal(value, layout); err != nil {
		return nil, fmt.Errorf("decode layout: %w", err)
	}
	return layout, nil
}

// Save replaces the layout. A layout the shell couldn't have made is
// InvalidArgument and changes nothing.
func (s *Store) Save(ctx context.Context, layout *corepb.Layout) error {
	if err := Check(layout); err != nil {
		return err
	}
	value, err := proto.Marshal(layout)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO overview_layout (id, value, updated_at) VALUES (1, ?, ?)
		ON CONFLICT (id) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		value, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("save layout: %w", err)
	}
	return nil
}

// Sizes are the Widget sizes: S 1×1, M 2×1, L 2×2.
var Sizes = []string{"S", "M", "L"}

// Check refuses, with InvalidArgument, a layout whose keys aren't
// <plugin>.<widget-id>, that places a Widget twice or at an unknown size, or
// that places a Widget it hasn't seen.
func Check(layout *corepb.Layout) error {
	seen := map[string]bool{}
	for _, key := range layout.GetSeen() {
		if err := checkKey(key); err != nil {
			return err
		}
		seen[key] = true
	}
	placed := map[string]bool{}
	for _, e := range layout.GetEntries() {
		key := e.GetWidget()
		if err := checkKey(key); err != nil {
			return err
		}
		switch {
		case placed[key]:
			return status.Errorf(codes.InvalidArgument, "widget %q is placed twice", key)
		case !seen[key]:
			return status.Errorf(codes.InvalidArgument, "widget %q is placed but not seen", key)
		case !slices.Contains(Sizes, e.GetSize()):
			return status.Errorf(codes.InvalidArgument, "widget %q has size %q, want one of %s", key, e.GetSize(), strings.Join(Sizes, ", "))
		}
		placed[key] = true
	}
	return nil
}

func checkKey(key string) error {
	plugin, id, ok := strings.Cut(key, ".")
	if !ok || plugin == "" || id == "" {
		return status.Errorf(codes.InvalidArgument, "widget %q: want <plugin>.<widget-id>", key)
	}
	return nil
}
