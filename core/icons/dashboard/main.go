// Command dashboard writes core/icons/dashboard-icons/aliases.json.gz: every
// homarr-labs/dashboard-icons icon's name and search aliases, from the
// upstream metadata.json at the pinned commit (`make generate`). The logos
// themselves stay on jsDelivr; this is only what the App icon search reads.
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// commit pins dashboard-icons; bump it to pick up new icons.
const commit = "d3b9234dd56f3af5862f06741f0dce101193b3df"

const out = "core/icons/dashboard-icons/aliases.json.gz"

func main() {
	url := fmt.Sprintf("https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons@%s/metadata.json", commit)
	res, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		log.Fatalf("GET %s: %s", url, res.Status)
	}
	b, err := io.ReadAll(res.Body)
	if err != nil {
		log.Fatal(err)
	}
	var meta map[string]struct {
		Aliases []string `json:"aliases"`
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		log.Fatal(err)
	}
	aliases := make(map[string][]string, len(meta))
	for name, m := range meta {
		a := []string{}
		for _, s := range m.Aliases {
			a = append(a, strings.ToLower(s))
		}
		aliases[name] = a
	}
	// encoding/json sorts map keys, so the same commit gives the same bytes.
	j, err := json.Marshal(aliases)
	if err != nil {
		log.Fatal(err)
	}
	var gz bytes.Buffer
	zw, err := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := zw.Write(j); err != nil {
		log.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(out, gz.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}
