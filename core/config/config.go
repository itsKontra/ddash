// Package config keeps each owner's config (a Component's, or the core's) in
// the core DB as binary proto, holding only what differs from the defaults.
package config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Store is the core's config table.
type Store struct {
	db *sql.DB
}

// New returns the Store in the core DB db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Load returns owner's stored config as a message of type desc. An owner
// with nothing stored gets an empty message.
func (s *Store) Load(ctx context.Context, owner string, desc protoreflect.MessageDescriptor) (proto.Message, error) {
	var value []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM config WHERE owner = ?`, owner).Scan(&value)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("load %s config: %w", owner, err)
	}
	msg := dynamicpb.NewMessage(desc)
	if err := proto.Unmarshal(value, msg); err != nil {
		return nil, fmt.Errorf("load %s config: %w", owner, err)
	}
	return msg, nil
}

// Put stores cfg as owner's config, replacing what was there. It is how
// config is seeded until forms save it.
func (s *Store) Put(ctx context.Context, owner string, cfg proto.Message) error {
	value, err := proto.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("put %s config: %w", owner, err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO config (owner, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (owner) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		owner, value, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("put %s config: %w", owner, err)
	}
	return nil
}
