package scrub

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// A JSON value as decoded here keeps its object keys in order, so a
// re-encoded fixture reads like the answer it was recorded from: object,
// []any, string, json.Number, bool or nil.
type object []member

type member struct {
	key   string
	value any
}

// get returns the value of key, or nil.
func (o object) get(key string) any {
	for _, m := range o {
		if m.key == key {
			return m.value
		}
	}
	return nil
}

// set replaces the value of key, if o has it.
func (o object) set(key string, v any) {
	for i := range o {
		if o[i].key == key {
			o[i].value = v
		}
	}
}

// decodeJSON reads one JSON document; ok is false for anything else.
func decodeJSON(b []byte) (v any, ok bool) {
	t := bytes.TrimSpace(b)
	if len(t) == 0 || t[0] != '{' && t[0] != '[' {
		return nil, false
	}
	d := json.NewDecoder(bytes.NewReader(t))
	d.UseNumber()
	v, err := decodeValue(d)
	if err != nil {
		return nil, false
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, false
	}
	return v, true
}

func decodeValue(d *json.Decoder) (any, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		o := object{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeValue(d)
			if err != nil {
				return nil, err
			}
			o = append(o, member{key: k.(string), value: v})
		}
		_, err := d.Token()
		return o, err
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, err := decodeValue(d)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err := d.Token()
		return a, err
	case json.Delim('}'), json.Delim(']'):
		return nil, errors.New("unexpected delimiter")
	}
	return tok, nil
}

// encodeJSON writes v indented by two spaces, ending in a newline.
func encodeJSON(v any) []byte {
	var b bytes.Buffer
	writeValue(&b, v, 0)
	b.WriteByte('\n')
	return b.Bytes()
}

func writeValue(b *bytes.Buffer, v any, depth int) {
	indent := func(d int) { b.WriteString("\n" + strings.Repeat("  ", d)) }
	switch v := v.(type) {
	case object:
		if len(v) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, m := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			indent(depth + 1)
			writeString(b, m.key)
			b.WriteString(": ")
			writeValue(b, m.value, depth+1)
		}
		indent(depth)
		b.WriteByte('}')
	case []any:
		if len(v) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, e := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			indent(depth + 1)
			writeValue(b, e, depth+1)
		}
		indent(depth)
		b.WriteByte(']')
	case string:
		writeString(b, v)
	case json.Number:
		b.WriteString(v.String())
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	default:
		b.WriteString("null")
	}
}

func writeString(b *bytes.Buffer, s string) {
	e := json.NewEncoder(b)
	e.SetEscapeHTML(false)
	_ = e.Encode(s)
	b.Truncate(b.Len() - 1) // Encode's newline
}

// walkStrings calls fn with every string in v, keys included, and keeps
// what it returns.
func walkStrings(v any, fn func(string) string) any {
	switch v := v.(type) {
	case object:
		for i := range v {
			v[i].key = fn(v[i].key)
			v[i].value = walkStrings(v[i].value, fn)
		}
		return v
	case []any:
		for i := range v {
			v[i] = walkStrings(v[i], fn)
		}
		return v
	case string:
		return fn(v)
	}
	return v
}
