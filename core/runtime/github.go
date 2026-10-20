package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// GitHubOptions configure a GitHub release source.
type GitHubOptions struct {
	// Repo is the GitHub repo, "owner/name".
	Repo string
	// Release is the release's tag: the Release this source provides.
	Release string
	// Token authenticates every request when set (DDASH_GITHUB_TOKEN),
	// as a private repo needs.
	Token string
	// DataDir is /data, where the downloads are kept.
	DataDir string
	// Client makes the requests. Nil uses http.DefaultClient.
	Client *http.Client
	// API is GitHub's REST API. Default https://api.github.com.
	API string
}

// GitHubSource is a Release published as a GitHub release: its assets are
// a Release dir's files. It downloads each file once into DataDir and
// verifies it against the release's SHA256SUMS:
//
//	catalogue.json, SHA256SUMS                  the running Release's
//	components/<name>/<release>/<name>          Component binaries
//	plugins/<name>/<release>/<name>.plugin.js   Plugin bundles, and .css
//
// What is already downloaded needs no network, so installed Plugins keep
// running while GitHub is unreachable.
type GitHubSource struct {
	o GitHubOptions

	// indexing is held while reading or downloading the Release's index:
	// catalogue.json and SHA256SUMS.
	indexing sync.Mutex

	mu     sync.Mutex
	sums   []byte            // the Release's SHA256SUMS, once read
	assets map[string]string // asset name → its API URL, once listed
}

// GitHub returns the release source for the GitHub release o.Release.
func GitHub(o GitHubOptions) *GitHubSource {
	if o.Client == nil {
		o.Client = http.DefaultClient
	}
	if o.API == "" {
		o.API = "https://api.github.com"
	}
	return &GitHubSource{o: o}
}

// Release is the release's tag.
func (g *GitHubSource) Release() string { return g.o.Release }

// Catalogue returns the Release's catalogue.json, kept in DataDir: it is
// downloaded once per Release, with SHA256SUMS.
func (g *GitHubSource) Catalogue(ctx context.Context) (Catalogue, error) {
	g.indexing.Lock()
	defer g.indexing.Unlock()
	cat, sums, err := g.local()
	if err == nil {
		g.setSums(sums)
		return cat, nil
	}
	// SHA256SUMS first: a catalogue.json of this Release vouches for it.
	if sums, err = g.fetch(ctx, SumsFile); err != nil {
		return Catalogue{}, err
	}
	b, err := g.fetch(ctx, CatalogueFile)
	if err != nil {
		return Catalogue{}, err
	}
	if cat, err = parseCatalogue(b); err != nil {
		return Catalogue{}, fmt.Errorf("Release %s: %w", g.o.Release, err)
	}
	if cat.Release != g.o.Release {
		return Catalogue{}, fmt.Errorf("Release %s: its catalogue.json is of Release %s", g.o.Release, cat.Release)
	}
	if err := writeFile(filepath.Join(g.o.DataDir, SumsFile), sums, 0o644); err != nil {
		return Catalogue{}, err
	}
	if err := writeFile(filepath.Join(g.o.DataDir, CatalogueFile), b, 0o644); err != nil {
		return Catalogue{}, err
	}
	g.setSums(sums)
	return cat, nil
}

func (g *GitHubSource) setSums(sums []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sums = sums
}

// local reads the kept catalogue.json and SHA256SUMS, if they are of this
// Release.
func (g *GitHubSource) local() (Catalogue, []byte, error) {
	b, err := os.ReadFile(filepath.Join(g.o.DataDir, CatalogueFile))
	if err != nil {
		return Catalogue{}, nil, err
	}
	cat, err := parseCatalogue(b)
	if err != nil {
		return Catalogue{}, nil, err
	}
	if cat.Release != g.o.Release {
		return Catalogue{}, nil, fmt.Errorf("kept catalogue.json is of Release %s", cat.Release)
	}
	sums, err := os.ReadFile(filepath.Join(g.o.DataDir, SumsFile))
	return cat, sums, err
}

// Component downloads the Component's binary, unless it already has it.
func (g *GitHubSource) Component(ctx context.Context, name string) (Binary, error) {
	dest := filepath.Join(g.o.DataDir, "components", name, g.o.Release, name)
	sum, err := g.ensure(ctx, name, dest, 0o755)
	if err != nil {
		return Binary{}, err
	}
	return Binary{Path: dest, Checksum: sum}, nil
}

// Plugin downloads the Plugin's bundles, unless it already has them.
func (g *GitHubSource) Plugin(ctx context.Context, name string) error {
	for _, file := range []string{name + ".plugin.js", name + ".plugin.css"} {
		if _, err := g.ensure(ctx, file, g.bundlePath(name, file), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Open opens a downloaded Plugin bundle, <name>.plugin.js or .css.
func (g *GitHubSource) Open(name string) (fs.File, error) {
	plugin, ok := strings.CutSuffix(name, ".plugin.js")
	if !ok {
		plugin, ok = strings.CutSuffix(name, ".plugin.css")
	}
	if !ok || !fs.ValidPath(name) || strings.Contains(name, "/") {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return os.Open(g.bundlePath(plugin, name))
}

func (g *GitHubSource) bundlePath(plugin, file string) string {
	return filepath.Join(g.o.DataDir, "plugins", plugin, g.o.Release, file)
}

// Latest returns the tag of the repo's latest release.
func (g *GitHubSource) Latest(ctx context.Context) (string, error) {
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := g.getJSON(ctx, "/releases/latest", &rel); err != nil {
		return "", err
	}
	return rel.TagName, nil
}

// ensure makes sure dest holds the asset file, verified against its
// checksum, downloading it if not, and returns the checksum.
func (g *GitHubSource) ensure(ctx context.Context, file, dest string, perm os.FileMode) ([]byte, error) {
	g.mu.Lock()
	sums := g.sums
	g.mu.Unlock()
	if sums == nil {
		if _, err := g.Catalogue(ctx); err != nil {
			return nil, err
		}
		g.mu.Lock()
		sums = g.sums
		g.mu.Unlock()
	}
	want, err := checksum(sums, file)
	if err != nil {
		return nil, fmt.Errorf("Release %s: %w", g.o.Release, err)
	}
	if got, err := fileSum(dest); err == nil && bytes.Equal(got, want) {
		return want, nil
	}
	b, err := g.fetch(ctx, file)
	if err != nil {
		return nil, err
	}
	if got := sha256.Sum256(b); !bytes.Equal(got[:], want) {
		return nil, fmt.Errorf("%w: %s of Release %s doesn't match its checksum in %s", ErrDownload, file, g.o.Release, SumsFile)
	}
	if err := writeFile(dest, b, perm); err != nil {
		return nil, err
	}
	return want, nil
}

// fetch downloads the release's asset file.
func (g *GitHubSource) fetch(ctx context.Context, file string) ([]byte, error) {
	b, err := g.fetchAsset(ctx, file)
	if err != nil {
		return nil, fmt.Errorf("%w: %s of Release %s: %v", ErrDownload, file, g.o.Release, err)
	}
	return b, nil
}

func (g *GitHubSource) fetchAsset(ctx context.Context, file string) ([]byte, error) {
	url, err := g.assetURL(ctx, file)
	if err != nil {
		return nil, err
	}
	res, err := g.do(ctx, url, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(res.Body)
}

// assetURL finds file among the release's assets.
func (g *GitHubSource) assetURL(ctx context.Context, file string) (string, error) {
	g.mu.Lock()
	assets := g.assets
	g.mu.Unlock()
	if assets == nil {
		var rel struct {
			Assets []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"assets"`
		}
		if err := g.getJSON(ctx, "/releases/tags/"+g.o.Release, &rel); err != nil {
			return "", err
		}
		assets = map[string]string{}
		for _, a := range rel.Assets {
			assets[a.Name] = a.URL
		}
		g.mu.Lock()
		g.assets = assets
		g.mu.Unlock()
	}
	url, ok := assets[file]
	if !ok {
		return "", fmt.Errorf("the GitHub release has no asset %s", file)
	}
	return url, nil
}

func (g *GitHubSource) getJSON(ctx context.Context, p string, v any) error {
	res, err := g.do(ctx, g.o.API+path.Join("/repos", g.o.Repo, p), "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(v)
}

// do GETs url, authenticated when there is a token. The client follows
// GitHub's redirect for an asset to its storage host without the token.
func (g *GitHubSource) do(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "ddash")
	if g.o.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.o.Token)
	}
	res, err := g.o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", req.URL.Path, res.Status)
	}
	return res, nil
}

// writeFile writes b to name atomically, creating its directory.
func writeFile(name string, b []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".download-*")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), perm)
	}
	if err == nil {
		err = os.Rename(f.Name(), name)
	}
	if err != nil {
		_ = os.Remove(f.Name())
	}
	return err
}

// fileSum is the sha256 of the file at name.
func fileSum(name string) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

var _ ReleaseSource = (*GitHubSource)(nil)
