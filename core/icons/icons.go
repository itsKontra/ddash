// Package icons is ddash's icon source: lucide's icons, pinned per Release,
// as the element lists lucide-static ships; and the names of the
// homarr-labs/dashboard-icons logos an App may use, pinned the same way,
// whose images the browser loads from jsDelivr.
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

// appAliasesGz is every dashboard-icons name with its search aliases,
// written by ./dashboard (`make generate`).
//
//go:embed dashboard-icons/aliases.json.gz
var appAliasesGz []byte

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

// appAliases decodes the embedded dashboard-icons names on first use.
var appAliases = sync.OnceValue(func() map[string][]string { return decode[[]string](appAliasesGz) })

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

// Search returns the names of the lucide icons q matches, case-insensitively,
// best first: the exact name, then names starting with q, then names holding
// it, then icons with a tag word starting with q; alphabetical within each.
// A blank q matches nothing.
func Search(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	return search(tags(), q, q)
}

// AppSearch returns the dashboard-icons names q matches, ranked as Search
// ranks them, with aliases for tags. Spaces in q match the names' dashes,
// so "home assistant" finds home-assistant.
func AppSearch(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	return search(appAliases(), strings.Join(strings.Fields(q), "-"), q)
}

// search ranks the names in words that name (for the name) or q (for a
// word) matches, as Search describes, up to MaxResults.
func search(words map[string][]string, name, q string) []string {
	out := []string{}
	if q == "" {
		return out
	}
	var ranked [4][]string
	for n, t := range words {
		switch {
		case n == name:
			ranked[0] = append(ranked[0], n)
		case strings.HasPrefix(n, name):
			ranked[1] = append(ranked[1], n)
		case strings.Contains(n, name):
			ranked[2] = append(ranked[2], n)
		case slices.ContainsFunc(t, func(tag string) bool { return hasWordPrefix(tag, q) }):
			ranked[3] = append(ranked[3], n)
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
