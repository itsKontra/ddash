// Package config keeps each owner's config (a Component's, or the core's) in
// the core DB as binary proto, holding only what differs from the defaults.
package config

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	configpb "github.com/itsKontra/ddash/proto/ddash/config"
)

// Store is the core's config table.
type Store struct {
	db *sql.DB
}

// New returns the Store in the core DB db.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Load returns owner's stored config as a message of type desc, with every
// unset field that has a default (option (ddash.config.field).default) set
// to it. An owner with nothing stored gets the defaults alone.
func (s *Store) Load(ctx context.Context, owner string, desc protoreflect.MessageDescriptor) (proto.Message, error) {
	msg, err := s.stored(ctx, owner, desc)
	if err != nil {
		return nil, err
	}
	if err := applyDefaults(msg); err != nil {
		return nil, fmt.Errorf("load %s config: %w", owner, err)
	}
	return msg.Interface(), nil
}

// stored returns owner's stored config as a message of type desc, without
// defaults: only what differs from them.
func (s *Store) stored(ctx context.Context, owner string, desc protoreflect.MessageDescriptor) (protoreflect.Message, error) {
	value, _, err := s.row(ctx, owner)
	if err != nil {
		return nil, err
	}
	msg := dynamicpb.NewMessage(desc)
	if err := proto.Unmarshal(value, msg); err != nil {
		return nil, fmt.Errorf("load %s config: %w", owner, err)
	}
	return msg, nil
}

// row reads owner's stored config; ok is false when there is none.
func (s *Store) row(ctx context.Context, owner string) (value []byte, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT value FROM config WHERE owner = ?`, owner).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("load %s config: %w", owner, err)
	}
	return value, true, nil
}

// Saved reports whether config was ever saved for owner.
func (s *Store) Saved(ctx context.Context, owner string) (bool, error) {
	_, ok, err := s.row(ctx, owner)
	return ok, err
}

// applyDefaults sets m's unset fields to their defaults, and those of the
// messages m holds.
func applyDefaults(m protoreflect.Message) error {
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if fd.Message() != nil && !fd.IsMap() {
			if fd.IsList() {
				list := m.Get(fd).List()
				for j := range list.Len() {
					if err := applyDefaults(list.Get(j).Message()); err != nil {
						return err
					}
				}
			} else if m.Has(fd) {
				if err := applyDefaults(m.Mutable(fd).Message()); err != nil {
					return err
				}
			}
			continue
		}
		text := proto.GetExtension(fd.Options(), configpb.E_Field).(*configpb.Field).GetDefault()
		if text == "" {
			continue
		}
		if fd.IsList() || !fd.HasPresence() {
			return fmt.Errorf("%s has a default, so it must be a proto3 optional scalar", fd.FullName())
		}
		if m.Has(fd) {
			continue
		}
		v, err := parseDefault(fd, text)
		if err != nil {
			return fmt.Errorf("%s: default %q: %w", fd.FullName(), text, err)
		}
		m.Set(fd, v)
	}
	return nil
}

func parseDefault(fd protoreflect.FieldDescriptor, text string) (protoreflect.Value, error) {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(text), nil
	case protoreflect.BoolKind:
		b, err := strconv.ParseBool(text)
		return protoreflect.ValueOfBool(b), err
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		n, err := strconv.ParseInt(text, 10, 64)
		return protoreflect.ValueOfInt64(n), err
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		n, err := strconv.ParseInt(text, 10, 32)
		return protoreflect.ValueOfInt32(int32(n)), err
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		n, err := strconv.ParseUint(text, 10, 64)
		return protoreflect.ValueOfUint64(n), err
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		n, err := strconv.ParseUint(text, 10, 32)
		return protoreflect.ValueOfUint32(uint32(n)), err
	case protoreflect.EnumKind:
		v := fd.Enum().Values().ByName(protoreflect.Name(text))
		if v == nil {
			return protoreflect.Value{}, fmt.Errorf("%s has no value %s", fd.Enum().FullName(), text)
		}
		return protoreflect.ValueOfEnum(v.Number()), nil
	}
	return protoreflect.Value{}, fmt.Errorf("a %s field can't have a default", fd.Kind())
}

// Delete forgets owner's config, so it has the defaults alone again.
func (s *Store) Delete(ctx context.Context, owner string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM config WHERE owner = ?`, owner); err != nil {
		return fmt.Errorf("delete %s config: %w", owner, err)
	}
	return nil
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
