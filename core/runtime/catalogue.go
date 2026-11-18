package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Catalogue is a Release's catalogue.json: every Plugin and Component in
// the Release, with what each requires.
type Catalogue struct {
	Release    string           `json:"release"`
	Plugins    []PluginEntry    `json:"plugins"`
	Components []ComponentEntry `json:"components"`
}

// PluginEntry is one Plugin's plugin.yaml.
type PluginEntry struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Author      string   `json:"author"`
	Requires    []string `json:"requires"`
	// Headless: the Plugin has no UI of its own, so no bundle in the
	// Release (no .ddapp) and no Widgets, Nav Entry, Pages, Concerns or
	// Commands. It is only the Components it requires and their config
	// forms, as a Notification channel's Plugin is.
	Headless bool `json:"headless,omitempty"`
}

// ComponentEntry is one Component's component.yaml.
type ComponentEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Requires    []string `json:"requires"`
}

// A ReleaseSource provides one Release: its catalogue, its Component
// binaries on local disk, and, as an fs.FS, its Plugins' files by
// "<plugin>/<file>", e.g. "docker/index.js", each from the Plugin's .ddapp.
type ReleaseSource interface {
	// Release names the Release, even while its catalogue can't be read.
	Release() string
	// Catalogue returns the Release's checked catalogue. An error wrapping
	// ErrDownload is passing: the runtime retries.
	Catalogue(ctx context.Context) (Catalogue, error)
	// Component makes the Component's binary, unpacked from its .ct
	// (verified against the Release's SHA256SUMS), available on local disk.
	Component(ctx context.Context, name string) (Binary, error)
	// File makes the Release's file name, verified against its SHA256SUMS,
	// available on local disk as it is, e.g. a Host Agent binary.
	File(ctx context.Context, name string) (Binary, error)
	// Plugin makes the Plugin's .ddapp available through Open.
	Plugin(ctx context.Context, name string) error
	fs.FS
}

// ErrDownload means a Release's file couldn't be downloaded. It passes:
// whoever asked retries.
var ErrDownload = errors.New("download failed")

// Binary is a file on local disk with its checksum: a Component's
// executable, which the subprocess launcher pins to it, or a Release file.
type Binary struct {
	Path     string
	Checksum []byte
}

// Dir is a Release laid out in a local directory, as DDASH_RELEASE_DIR names
// it: catalogue.json, SHA256SUMS, the .ct and .ddapp files and the Host Agent
// binaries, flat, as release assets. It is used in place, but for the
// Component binaries, which are unpacked into Unpack.
type Dir struct {
	// Path is the Release dir. It may be read-only.
	Path string
	// Unpack is where Component binaries are unpacked, one per Component
	// (<unpack>/<name>), e.g. under /data. Component needs it.
	Unpack string
}

// Open opens the Plugin's file "<plugin>/<file>" in its .ddapp. In a dev
// Release dir a .ddapp may be a directory instead (a link to the Plugin's
// dist/, rebuilt in place).
func (d Dir) Open(name string) (fs.File, error) {
	plugin, file, ok := splitAppPath(name)
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return openApp(filepath.Join(d.Path, AppFile(plugin)), file)
}

// Release reads the Release's name from catalogue.json; "" when it can't.
func (d Dir) Release() string {
	var c Catalogue
	b, _ := os.ReadFile(filepath.Join(d.Path, CatalogueFile))
	_ = json.Unmarshal(b, &c)
	return c.Release
}

// Catalogue reads and checks the directory's catalogue.json.
func (d Dir) Catalogue(context.Context) (Catalogue, error) {
	b, err := os.ReadFile(filepath.Join(d.Path, CatalogueFile))
	if err != nil {
		return Catalogue{}, err
	}
	return parseCatalogue(b)
}

// Component checks dir/<name>.ct against dir/SHA256SUMS and unpacks its
// binary into d.Unpack.
func (d Dir) Component(ctx context.Context, name string) (Binary, error) {
	if d.Unpack == "" {
		return Binary{}, fmt.Errorf("component %s: the Release dir has nowhere to unpack it", name)
	}
	ct, err := d.File(ctx, ComponentFile(name))
	if err != nil {
		return Binary{}, err
	}
	dest := filepath.Join(d.Unpack, name)
	sum, err := unpackComponent(ct.Path, name, dest)
	if err != nil {
		return Binary{}, err
	}
	return Binary{Path: dest, Checksum: sum}, nil
}

// File returns dir/<name>, checked against dir/SHA256SUMS.
func (d Dir) File(_ context.Context, name string) (Binary, error) {
	sums, err := os.ReadFile(filepath.Join(d.Path, SumsFile))
	if err != nil {
		return Binary{}, err
	}
	want, err := checksum(sums, name)
	if err != nil {
		return Binary{}, err
	}
	p := filepath.Join(d.Path, name)
	got, err := fileSum(p)
	if err != nil {
		return Binary{}, err
	}
	if !bytes.Equal(got, want) {
		return Binary{}, fmt.Errorf("%s doesn't match its checksum in %s", name, SumsFile)
	}
	return Binary{Path: p, Checksum: want}, nil
}

// Plugin does nothing: the .ddapp files are already in the dir.
func (d Dir) Plugin(context.Context, string) error { return nil }

// CatalogueFile is the Release's catalogue, at the root of a Release dir.
const CatalogueFile = "catalogue.json"

// parseCatalogue reads and checks a catalogue.json.
func parseCatalogue(b []byte) (Catalogue, error) {
	var c Catalogue
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("catalogue.json: %w", err)
	}
	if err := c.Check(); err != nil {
		return c, fmt.Errorf("catalogue.json: %w", err)
	}
	return c, nil
}

// checksum finds file's checksum in sums, in sha256sum's format.
func checksum(sums []byte, file string) ([]byte, error) {
	for line := range strings.Lines(string(sums)) {
		sum, name, ok := strings.Cut(strings.TrimRight(line, "\r\n"), "  ")
		if ok && strings.TrimPrefix(name, "*") == file {
			b, err := hex.DecodeString(sum)
			if err != nil || len(b) != sha256.Size {
				return nil, fmt.Errorf("%s: bad checksum %q for %s", SumsFile, sum, file)
			}
			return b, nil
		}
	}
	return nil, fmt.Errorf("%s has no checksum in %s", file, SumsFile)
}

// ReservedPluginNames are the core's own top-level paths. A Plugin's Pages
// live under /<name>/, so no Plugin may take one of these names.
var ReservedPluginNames = []string{"api", "auth", "vendor", "assets", "agent", "plugins", "settings", "healthz"}

// Check refuses a Plugin named after a core path, a requires that doesn't
// resolve and a requires cycle.
func (c Catalogue) Check() error {
	for _, p := range c.Plugins {
		if slices.Contains(ReservedPluginNames, p.Name) {
			return fmt.Errorf("plugin name %s clashes with the core's /%s", p.Name, p.Name)
		}
	}
	for _, p := range c.Plugins {
		for _, req := range p.Requires {
			if _, ok := c.component(req); !ok {
				return fmt.Errorf("plugin %s requires %s, which isn't in the Release", p.Name, req)
			}
		}
	}
	for _, comp := range c.Components {
		for _, req := range comp.Requires {
			if _, ok := c.component(req); !ok {
				return fmt.Errorf("component %s requires %s, which isn't in the Release", comp.Name, req)
			}
		}
	}
	for _, comp := range c.Components {
		if _, err := c.startOrder([]string{comp.Name}); err != nil {
			return err
		}
	}
	return nil
}

func (c Catalogue) plugin(name string) (PluginEntry, bool) {
	i := slices.IndexFunc(c.Plugins, func(p PluginEntry) bool { return p.Name == name })
	if i < 0 {
		return PluginEntry{}, false
	}
	return c.Plugins[i], true
}

func (c Catalogue) component(name string) (ComponentEntry, bool) {
	i := slices.IndexFunc(c.Components, func(p ComponentEntry) bool { return p.Name == name })
	if i < 0 {
		return ComponentEntry{}, false
	}
	return c.Components[i], true
}

// startOrder returns roots and everything they require, transitively, each
// Component after the ones it requires.
func (c Catalogue) startOrder(roots []string) ([]string, error) {
	var order []string
	const (
		visiting = 1
		done     = 2
	)
	mark := map[string]int{}
	var visit func(name string, path []string) error
	visit = func(name string, path []string) error {
		switch mark[name] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("requires cycle: %v", append(path, name))
		}
		mark[name] = visiting
		comp, ok := c.component(name)
		if !ok {
			return fmt.Errorf("component %s isn't in the Release", name)
		}
		for _, req := range comp.Requires {
			if err := visit(req, append(path, name)); err != nil {
				return err
			}
		}
		mark[name] = done
		order = append(order, name)
		return nil
	}
	for _, root := range roots {
		if err := visit(root, nil); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// Pulls returns the Components installing plugin runs: its requires,
// transitively, each after the ones it requires. It is nil for a Plugin
// the Release lacks.
func (c Catalogue) Pulls(plugin string) []string {
	p, ok := c.plugin(plugin)
	if !ok {
		return nil
	}
	order, _ := c.startOrder(p.Requires) // the catalogue was checked on load
	return order
}
