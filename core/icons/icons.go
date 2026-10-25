// Package icons is ddash's only icon source: lucide's icons, pinned per
// Release, as the element lists lucide-static ships.
package icons

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"io"
	"sync"
)

// nodesGz is lucide-static's icon-nodes.json, gzipped by
// packages/ui/scripts/icons.ts (`make generate`).
//
//go:embed lucide/icon-nodes.json.gz
var nodesGz []byte

// Fallback is the icon an unknown name draws.
const Fallback = "puzzle"

// all decodes the embedded icons on first use.
var all = sync.OnceValue(func() map[string]json.RawMessage {
	zr, err := gzip.NewReader(bytes.NewReader(nodesGz))
	if err != nil {
		panic(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		panic(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		panic(err)
	}
	return m
})

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
