package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"

	configpb "github.com/itsKontra/ddash/proto/ddash/config"
	corepb "github.com/itsKontra/ddash/proto/ddash/core"
)

// Form returns owner's config form: the fields of desc, owner's stored
// values without its secrets, and which secrets are set. errors are why the
// stored config doesn't start, as the caller knows them.
func (s *Store) Form(ctx context.Context, owner string, desc protoreflect.MessageDescriptor) (*corepb.GetConfigFormResponse, error) {
	fields, err := Fields(desc)
	if err != nil {
		return nil, err
	}
	stored, err := s.stored(ctx, owner, desc)
	if err != nil {
		return nil, err
	}
	resp := &corepb.GetConfigFormResponse{Fields: fields}
	for _, fd := range topSecrets(desc) {
		if stored.Has(fd) {
			resp.SecretsSet = append(resp.SecretsSet, string(fd.Name()))
			stored.Clear(fd)
		}
	}
	if resp.Values, err = toStruct(stored.Interface()); err != nil {
		return nil, fmt.Errorf("%s config form: %w", owner, err)
	}
	return resp, nil
}

// Fields describes desc's fields as a form, refusing a config message of a
// shape the form doesn't support.
func Fields(desc protoreflect.MessageDescriptor) ([]*corepb.FormField, error) {
	return fields(desc, true)
}

func fields(desc protoreflect.MessageDescriptor, top bool) ([]*corepb.FormField, error) {
	var out []*corepb.FormField
	fds := desc.Fields()
	for i := range fds.Len() {
		f, err := field(fds.Get(i), top)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func field(fd protoreflect.FieldDescriptor, top bool) (*corepb.FormField, error) {
	unsupported := func(why string) error {
		return fmt.Errorf("config field %s: %s isn't supported in a config form", fd.FullName(), why)
	}
	opts := options(fd)
	f := &corepb.FormField{
		Name:     string(fd.Name()),
		Label:    cmpOr(opts.GetLabel(), sentence(string(fd.Name()))),
		Help:     opts.GetHelp(),
		Required: opts.GetRequired(),
		Secret:   opts.GetSecret(),
		Default:  opts.GetDefault(),
		Repeated: fd.IsList(),
		Min:      opts.Min,
		Max:      opts.Max,
	}
	f.Placeholder = cmpOr(f.Default, opts.GetPlaceholder())
	if opts.GetFormat() != configpb.Field_FORMAT_UNSPECIFIED {
		f.Format = FormatName(opts.GetFormat())
	}
	if oneof := fd.ContainingOneof(); oneof != nil && !oneof.IsSynthetic() {
		return nil, unsupported("a oneof")
	}
	switch {
	case fd.IsMap():
		return nil, unsupported("a map")
	case fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind:
		if !top || !fd.IsList() {
			return nil, unsupported("a message other than a top-level repeated one")
		}
		cols, err := fields(fd.Message(), false)
		if err != nil {
			return nil, err
		}
		f.Kind, f.Columns = corepb.FormField_KIND_TABLE, cols
	case fd.Kind() == protoreflect.StringKind:
		f.Kind = corepb.FormField_KIND_STRING
	case fd.Kind() == protoreflect.Int64Kind:
		f.Kind = corepb.FormField_KIND_INT
	case fd.Kind() == protoreflect.BoolKind:
		f.Kind = corepb.FormField_KIND_BOOL
	case fd.Kind() == protoreflect.EnumKind:
		f.Kind = corepb.FormField_KIND_ENUM
		f.Options = enumOptions(fd.Enum())
	default:
		return nil, unsupported("a " + fd.Kind().String())
	}
	if f.Secret && (!top || fd.IsList() || f.Kind != corepb.FormField_KIND_STRING) {
		return nil, unsupported("a secret other than a top-level string")
	}
	if f.Default != "" && (fd.IsList() || !fd.HasPresence()) {
		return nil, fmt.Errorf("config field %s has a default, so it must be a proto3 optional scalar", fd.FullName())
	}
	return f, nil
}

func enumOptions(ed protoreflect.EnumDescriptor) []*corepb.EnumOption {
	prefix := upperSnake(string(ed.Name())) + "_"
	var out []*corepb.EnumOption
	values := ed.Values()
	for i := range values.Len() {
		v := values.Get(i)
		if v.Number() == 0 {
			continue
		}
		label, _ := proto.GetExtension(v.Options(), configpb.E_Value).(*configpb.EnumValue)
		out = append(out, &corepb.EnumOption{
			Value: string(v.Name()),
			Label: cmpOr(label.GetLabel(), sentence(strings.TrimPrefix(string(v.Name()), prefix))),
		})
	}
	return out
}

// options are fd's (ddash.config.field) options.
func options(fd protoreflect.FieldDescriptor) *configpb.Field {
	if opts, _ := proto.GetExtension(fd.Options(), configpb.E_Field).(*configpb.Field); opts != nil {
		return opts
	}
	return &configpb.Field{}
}

// topSecrets are desc's secret fields.
func topSecrets(desc protoreflect.MessageDescriptor) []protoreflect.FieldDescriptor {
	var out []protoreflect.FieldDescriptor
	fds := desc.Fields()
	for i := range fds.Len() {
		if options(fds.Get(i)).GetSecret() {
			out = append(out, fds.Get(i))
		}
	}
	return out
}

// FormatName is how a form names a format: "url", "ip_range", ...
func FormatName(f configpb.Field_Format) string {
	return strings.ToLower(strings.TrimPrefix(f.String(), "FORMAT_"))
}

// sentence turns "public_url" or "PUBLIC_URL" into "Public url".
func sentence(name string) string {
	s := strings.ToLower(strings.ReplaceAll(name, "_", " "))
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// upperSnake turns "IpRange" into "IP_RANGE".
func upperSnake(name string) string {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte('_')
		}
		b.WriteRune(r)
	}
	return strings.ToUpper(b.String())
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// toStruct is m as protojson with proto field names, as a Struct.
func toStruct(m proto.Message) (*structpb.Struct, error) {
	b, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(m)
	if err != nil {
		return nil, err
	}
	s := &structpb.Struct{}
	if err := protojson.Unmarshal(b, s); err != nil {
		return nil, err
	}
	return s, nil
}

// fieldText is v, a value of fd, as the JSON text the Audit Log records.
func fieldText(fd protoreflect.FieldDescriptor, v protoreflect.Value) string {
	switch {
	case fd.IsList():
		parts := make([]string, v.List().Len())
		for i := range parts {
			parts[i] = scalarText(fd, v.List().Get(i))
		}
		return "[" + strings.Join(parts, ",") + "]"
	default:
		return scalarText(fd, v)
	}
}

func scalarText(fd protoreflect.FieldDescriptor, v protoreflect.Value) string {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return strconv.Quote(v.String())
	case protoreflect.EnumKind:
		if ev := fd.Enum().Values().ByNumber(v.Enum()); ev != nil {
			return strconv.Quote(string(ev.Name()))
		}
		return strconv.Itoa(int(v.Enum()))
	case protoreflect.MessageKind, protoreflect.GroupKind:
		b, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(v.Message().Interface())
		if err != nil {
			return "?"
		}
		return compactJSON(b)
	default:
		return v.String()
	}
}

// compactJSON is b without the whitespace protojson varies on purpose.
func compactJSON(b []byte) string {
	var out bytes.Buffer
	if json.Compact(&out, b) != nil {
		return string(b)
	}
	return out.String()
}
