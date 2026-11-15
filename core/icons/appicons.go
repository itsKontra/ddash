package icons

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sync/singleflight"
)

// DashboardIconsCommit pins homarr-labs/dashboard-icons: the commit whose
// names the App icon search knows and whose logos AppIcons serves. Bump it
// and run `make generate` to pick up new icons.
const DashboardIconsCommit = "d3b9234dd56f3af5862f06741f0dce101193b3df"

// DashboardIconsURL is where the pinned dashboard-icons are downloaded.
const DashboardIconsURL = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons@" + DashboardIconsCommit

// maxLogoBytes bounds one downloaded logo; upstream's are a few KB.
const maxLogoBytes = 2 << 20

// ErrNoAppIcon says dashboard-icons has no icon of that name.
var ErrNoAppIcon = errors.New("no such App icon")

// AppIcons serves the dashboard-icons logos from the core: each is
// downloaded once, the first time it is asked for, and kept in Dir. So
// the browser never asks a CDN, and a logo seen once draws without
// internet.
type AppIcons struct {
	// Dir keeps the downloaded logos, under the pinned commit.
	Dir string
	// URL is the upstream. Empty uses DashboardIconsURL.
	URL string
	// Client downloads. Nil uses one with a 10 s timeout.
	Client *http.Client

	flights singleflight.Group
}

// AppIcon is one logo: its file on disk and its Content-Type.
type AppIcon struct {
	Path        string
	ContentType string
}

// logoFormats are tried in order: upstream has every icon as PNG and most
// as SVG.
var logoFormats = []struct{ ext, contentType string }{
	{"svg", "image/svg+xml"},
	{"png", "image/png"},
}

// IsAppIcon reports whether dashboard-icons has an icon called name.
func IsAppIcon(name string) bool {
	_, ok := appAliases()[name]
	return ok
}

// Get returns the logo called name, downloading it if Dir doesn't hold it
// yet. A name dashboard-icons doesn't have is ErrNoAppIcon, without asking
// upstream.
func (a *AppIcons) Get(ctx context.Context, name string) (AppIcon, error) {
	// Only known names reach the disk or upstream, so name is no path.
	if !IsAppIcon(name) {
		return AppIcon{}, ErrNoAppIcon
	}
	dir := filepath.Join(a.Dir, DashboardIconsCommit)
	for _, f := range logoFormats {
		p := filepath.Join(dir, name+"."+f.ext)
		if _, err := os.Stat(p); err == nil {
			return AppIcon{Path: p, ContentType: f.contentType}, nil
		}
	}
	// One download per name, whoever asks meanwhile; it outlives a caller
	// who left, since the next one wants it too.
	v, err, _ := a.flights.Do(name, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		return a.download(ctx, dir, name)
	})
	if err != nil {
		return AppIcon{}, err
	}
	return v.(AppIcon), nil
}

func (a *AppIcons) download(ctx context.Context, dir, name string) (AppIcon, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return AppIcon{}, err
	}
	base := a.URL
	if base == "" {
		base = DashboardIconsURL
	}
	for _, f := range logoFormats {
		b, err := a.fetch(ctx, fmt.Sprintf("%s/%s/%s.%s", base, f.ext, name, f.ext))
		if errors.Is(err, ErrNoAppIcon) {
			continue
		}
		if err != nil {
			return AppIcon{}, err
		}
		p := filepath.Join(dir, name+"."+f.ext)
		tmp, err := os.CreateTemp(dir, "."+name+".*")
		if err != nil {
			return AppIcon{}, err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(b); err != nil {
			tmp.Close()
			return AppIcon{}, err
		}
		if err := tmp.Close(); err != nil {
			return AppIcon{}, err
		}
		if err := os.Rename(tmp.Name(), p); err != nil {
			return AppIcon{}, err
		}
		return AppIcon{Path: p, ContentType: f.contentType}, nil
	}
	return AppIcon{}, ErrNoAppIcon
}

// fetch GETs url: ErrNoAppIcon for a 404, the body otherwise.
func (a *AppIcons) fetch(ctx context.Context, url string) ([]byte, error) {
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound:
		return nil, ErrNoAppIcon
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GET %s: %s", url, res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxLogoBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxLogoBytes {
		return nil, fmt.Errorf("GET %s: more than %d bytes", url, maxLogoBytes)
	}
	return b, nil
}
