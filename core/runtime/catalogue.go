package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
}

// ComponentEntry is one Component's component.yaml.
type ComponentEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Requires    []string `json:"requires"`
}

// A ReleaseSource provides a Release's catalogue.
type ReleaseSource interface {
	Catalogue(ctx context.Context) (Catalogue, error)
}

// Dir is a Release laid out in a local directory, as DDASH_RELEASE_DIR names
// it: catalogue.json at its root.
type Dir string

// Catalogue reads and checks the directory's catalogue.json.
func (d Dir) Catalogue(context.Context) (Catalogue, error) {
	var c Catalogue
	b, err := os.ReadFile(filepath.Join(string(d), "catalogue.json"))
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("catalogue.json: %w", err)
	}
	if err := c.check(); err != nil {
		return c, fmt.Errorf("catalogue.json: %w", err)
	}
	return c, nil
}

// check refuses a requires that doesn't resolve and a requires cycle.
func (c Catalogue) check() error {
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
