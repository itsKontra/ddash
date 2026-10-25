// Package icons is ddash's only icon source: lucide's icons, pinned per
// Release, as the element lists lucide-static ships.
package icons

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"sync"
)

// nodesGz is lucide-static's icon-nodes.json, written by
// packages/ui/scripts/icons.ts and gzipped by ./gzip (`make generate`).
//
//go:embed lucide/icon-nodes.json.gz
var nodesGz []byte

// tagsGz is lucide-static's tags.json (each icon's search words), generated
// the same way.
//
//go:embed lucide/tags.json.gz
var tagsGz []byte

// Fallback is the icon an unknown name draws.
const Fallback = "puzzle"

// all decodes the embedded icons on first use.
var all = sync.OnceValue(func() map[string]json.RawMessage { return decode[json.RawMessage](nodesGz) })

// tags decodes the embedded tags on first use, lower-cased.
var tags = sync.OnceValue(func() map[string][]string {
	m := decode[[]string](tagsGz)
	for _, t := range m {
		for i := range t {
			t[i] = strings.ToLower(t[i])
		}
	}
	return m
})

// decode gunzips and unmarshals a generated file, which is well-formed.
func decode[T any](gz []byte) map[string]T {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		panic(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		panic(err)
	}
	var m map[string]T
	if err := json.Unmarshal(b, &m); err != nil {
		panic(err)
	}
	return m
}

// Nodes returns each named icon's lucide element list ([tag, attrs]
// pairs), the Fallback's for a name lucide doesn't have.
func Nodes(names []string) map[string]json.RawMessage {
	m := all()
	out := make(map[string]json.RawMessage, len(names))
	for _, n := range names {
		nodes, ok := m[n]
		if !ok {
			nodes = m[Fallback]
		}
		out[n] = nodes
	}
	return out
}

// MaxResults caps a Search, about a picker's grid of icons.
const MaxResults = 96

// Search returns the names of the icons q matches, case-insensitively, best
// first: the exact name, then names starting with q, then names holding it,
// then icons with a tag word starting with q; alphabetical within each. A
// blank q matches nothing.
func Search(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	out := []string{}
	if q == "" {
		return out
	}
	var ranked [4][]string
	for name, t := range tags() {
		switch {
		case name == q:
			ranked[0] = append(ranked[0], name)
		case strings.HasPrefix(name, q):
			ranked[1] = append(ranked[1], name)
		case strings.Contains(name, q):
			ranked[2] = append(ranked[2], name)
		case slices.ContainsFunc(t, func(tag string) bool { return hasWordPrefix(tag, q) }):
			ranked[3] = append(ranked[3], name)
		}
	}
	for _, r := range ranked {
		slices.Sort(r)
		out = append(out, r...)
	}
	return out[:min(len(out), MaxResults)]
}

// hasWordPrefix reports whether tag, or one of its words, starts with q.
func hasWordPrefix(tag, q string) bool {
	if strings.HasPrefix(tag, q) {
		return true
	}
	for _, w := range strings.Fields(tag) {
		if strings.HasPrefix(w, q) {
			return true
		}
	}
	return false
}
