package runtime

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A Release ships each Plugin and each Component as one zip file:
//
//	<plugin>.ddapp   the Plugin's built files (index.js, index.css, …) at its root
//	<component>.ct   the Component's static binary, named <component>, at its root
//
// Not .app: GitHub refuses release assets with that extension.
const (
	AppExt       = ".ddapp"
	ComponentExt = ".ct"
)

// AppFile is plugin's file in a Release.
func AppFile(plugin string) string { return plugin + AppExt }

// ComponentFile is component's file in a Release.
func ComponentFile(component string) string { return component + ComponentExt }

// HostAgentArchs are the architectures the Host Agent is built for.
var HostAgentArchs = []string{"amd64", "arm64"}

// HostAgentFile is the Host Agent's binary for arch in a Release.
func HostAgentFile(arch string) string { return "ddash-host-agent-linux-" + arch }

// packTime is every packed file's modification time, so packing the same
// files gives the same bytes, and the same checksum.
var packTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// Pack zips every regular file in fsys, by its path, keeping its
// permission bits: the format of a .ddapp and a .ct.
func Pack(w io.Writer, fsys fs.FS) error {
	zw := zip.NewWriter(w)
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := fs.Stat(fsys, name) // follows a symlink, as `make dev`'s are
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("pack %s: not a regular file", name)
		}
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: packTime}
		h.SetMode(info.Mode().Perm())
		f, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		src, err := fsys.Open(name)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(f, src)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

// PackFile is Pack into the file dst.
func PackFile(dst string, fsys fs.FS) error {
	var b bytes.Buffer
	if err := Pack(&b, fsys); err != nil {
		return err
	}
	return writeFile(dst, b.Bytes(), 0o644)
}

// openApp opens file in the Plugin's app at app: a .ddapp zip, or a
// directory of its files (`make dev` links each Plugin's dist/ so), which
// is read afresh each time. A .ddapp is kept open between calls (openApps).
func openApp(app, file string) (fs.File, error) {
	if !fs.ValidPath(file) {
		return nil, &fs.PathError{Op: "open", Path: file, Err: fs.ErrInvalid}
	}
	info, err := os.Stat(app)
	if err != nil {
		openApps.drop(app)
		return nil, err
	}
	if info.IsDir() {
		return os.DirFS(app).Open(file)
	}
	a, err := openApps.get(app, info)
	if err != nil {
		return nil, err
	}
	f, err := a.zip.Open(file)
	if err != nil {
		a.release()
		return nil, err
	}
	return &appFile{File: f, app: a}, nil
}

// openApps keeps every .ddapp opened open, by its path: one per Plugin of
// the Release, each an open file and its zip directory, so serving a
// bundle's file reads no directory again. A .ddapp replaced or changed on
// disk (a Release dir repacked in place) is opened anew; one gone is
// dropped.
var openApps = appCache{apps: map[string]*openedApp{}}

type appCache struct {
	mu   sync.Mutex
	apps map[string]*openedApp
}

// openedApp is an open .ddapp. It closes once no file in it is open and
// openApps no longer holds it.
type openedApp struct {
	info os.FileInfo
	file *os.File
	zip  *zip.Reader
	refs atomic.Int32 // its open files, plus one while openApps holds it
}

// get returns the .ddapp at app, whose stat is info, with a reference the
// caller releases.
func (c *appCache) get(app string, info os.FileInfo) (*openedApp, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a := c.apps[app]; a != nil {
		if sameFile(a.info, info) {
			a.refs.Add(1)
			return a, nil
		}
		delete(c.apps, app)
		a.release()
	}
	a, err := openZip(app)
	if err != nil {
		return nil, err
	}
	a.refs.Store(2)
	c.apps[app] = a
	return a, nil
}

// drop forgets the .ddapp at app, if open.
func (c *appCache) drop(app string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a := c.apps[app]; a != nil {
		delete(c.apps, app)
		a.release()
	}
}

// sameFile reports whether a and b are the same file, unchanged.
func sameFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func openZip(name string) (*openedApp, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		var z *zip.Reader
		if z, err = zip.NewReader(f, info.Size()); err == nil {
			return &openedApp{info: info, file: f, zip: z}, nil
		}
	}
	f.Close()
	return nil, fmt.Errorf("%s: %w", filepath.Base(name), err)
}

func (a *openedApp) release() {
	if a.refs.Add(-1) == 0 {
		a.file.Close()
	}
}

// appFile is a file in an open .ddapp.
type appFile struct {
	fs.File
	app   *openedApp
	close sync.Once
}

func (f *appFile) Close() error {
	f.close.Do(f.app.release)
	return f.File.Close()
}

// splitAppPath splits a Release's fs.FS path "<plugin>/<file>".
func splitAppPath(name string) (plugin, file string, ok bool) {
	plugin, file, ok = strings.Cut(name, "/")
	return plugin, file, ok && fs.ValidPath(name) && plugin != "" && file != ""
}

// unpackComponent writes the binary named name in the .ct at ct to dest,
// executable, unless dest already holds it, and returns its checksum, which
// the subprocess launcher pins.
func unpackComponent(ct, name, dest string) ([]byte, error) {
	z, err := zip.OpenReader(ct)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(ct), err)
	}
	defer z.Close()
	var entry *zip.File
	for _, f := range z.File {
		if f.Name == name {
			entry = f
		}
	}
	if entry == nil {
		return nil, fmt.Errorf("%s holds no binary %s", filepath.Base(ct), name)
	}
	if sum, ok := unpacked(dest, entry); ok {
		return sum, nil
	}
	src, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()
	b, err := io.ReadAll(src) // checks the entry's CRC-32
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(ct), err)
	}
	if err := writeFile(dest, b, 0o755); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	return sum[:], nil
}

// unpacked returns dest's checksum when dest already holds entry.
func unpacked(dest string, entry *zip.File) ([]byte, bool) {
	f, err := os.Open(dest)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	sha, crc := sha256.New(), crc32.NewIEEE()
	n, err := io.Copy(io.MultiWriter(sha, crc), f)
	if err != nil || uint64(n) != entry.UncompressedSize64 || crc.Sum32() != entry.CRC32 {
		return nil, false
	}
	return sha.Sum(nil), true
}
