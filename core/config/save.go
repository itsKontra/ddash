package config

import (
	"context"
	"fmt"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/structpb"

	corepb "github.com/itsKontra/ddash/proto/ddash/core"
)

// Input is what a config form saves.
type Input struct {
	// Values are the config by proto field name. An absent field is unset,
	// meaning its default. A secret here replaces the stored one.
	Values *structpb.Struct
	// ClearSecrets are secret fields to clear. A secret neither in Values
	// nor here keeps its stored value.
	ClearSecrets []string
}

// Edit is a candidate config for an owner, not yet saved.
type Edit struct {
	owner string
	// config is the candidate as it would be stored: only what differs
	// from the defaults.
	config protoreflect.Message
	// Changes are its fields that differ from the stored config, a
	// secret's as "changed" or "cleared".
	Changes []*corepb.ConfigChange
}

// Edit builds owner's candidate config from a form's input: the input's
// values, plus every stored secret the input neither replaces nor clears.
// Input that isn't a config of type desc at all is InvalidArgument.
func (s *Store) Edit(ctx context.Context, owner string, desc protoreflect.MessageDescriptor, in Input) (*Edit, error) {
	invalid := func(format string, args ...any) error {
		return status.Errorf(codes.InvalidArgument, "invalid config: "+format, args...)
	}
	stored, err := s.stored(ctx, owner, desc)
	if err != nil {
		return nil, err
	}
	cand := dynamicpb.NewMessage(desc)
	if in.Values != nil {
		b, err := protojson.Marshal(in.Values)
		if err != nil {
			return nil, invalid("%v", err)
		}
		if err := protojson.Unmarshal(b, cand); err != nil {
			return nil, invalid("%v", err)
		}
	}
	secrets := topSecrets(desc)
	for _, name := range in.ClearSecrets {
		if !slices.ContainsFunc(secrets, func(fd protoreflect.FieldDescriptor) bool { return string(fd.Name()) == name }) {
			return nil, invalid("%s is no secret to clear", name)
		}
	}
	for _, fd := range secrets {
		name := string(fd.Name())
		switch {
		case slices.Contains(in.ClearSecrets, name):
			cand.Clear(fd)
		case in.Values.GetFields()[name] == nil && stored.Has(fd):
			cand.Set(fd, stored.Get(fd))
		}
	}
	return &Edit{owner: owner, config: cand, Changes: changes(stored, cand)}, nil
}

// changes lists the top-level fields that differ between old and new.
func changes(old, new protoreflect.Message) []*corepb.ConfigChange {
	var out []*corepb.ConfigChange
	fds := old.Descriptor().Fields()
	for i := range fds.Len() {
		fd := fds.Get(i)
		oldText, newText := "", ""
		if old.Has(fd) {
			oldText = fieldText(fd, old.Get(fd))
		}
		if new.Has(fd) {
			newText = fieldText(fd, new.Get(fd))
		}
		if oldText == newText {
			continue
		}
		c := &corepb.ConfigChange{Field: string(fd.Name())}
		switch {
		case !options(fd).GetSecret():
			c.Old, c.New = oldText, newText
		case newText == "":
			c.Change = "cleared"
		default:
			c.Change = "changed"
		}
		out = append(out, c)
	}
	return out
}

// Starter starts the owner's Component with cfg, its candidate config with
// the defaults applied, and reports whether Start accepted it.
type Starter func(ctx context.Context, cfg proto.Message) error

// Save checks the edit against its fields' rules, hands the candidate to
// start, and stores it only when start returns nil. A candidate the rules
// reject is an "invalid config" error (sdk.FieldErrorsOf reads it) and
// never reaches start.
func (s *Store) Save(ctx context.Context, e *Edit, start Starter) error {
	withDefaults := proto.Clone(e.config.Interface()).ProtoReflect()
	if err := applyDefaults(withDefaults); err != nil {
		return fmt.Errorf("save %s config: %w", e.owner, err)
	}
	if err := Validate(withDefaults).Err(); err != nil {
		return err
	}
	if err := start(ctx, withDefaults.Interface()); err != nil {
		return err
	}
	return s.Put(ctx, e.owner, e.config.Interface())
}
