package runtime

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
// directory of its files (`make dev` links each Plugin's dist/ so).
func openApp(app, file string) (fs.File, error) {
	if !fs.ValidPath(file) {
		return nil, &fs.PathError{Op: "open", Path: file, Err: fs.ErrInvalid}
	}
	if info, err := os.Stat(app); err == nil && info.IsDir() {
		return os.DirFS(app).Open(file)
	}
	z, err := zip.OpenReader(app)
	if err != nil {
		return nil, err
	}
	f, err := z.Open(file)
	if err != nil {
		z.Close()
		return nil, err
	}
	return appFile{File: f, zip: z}, nil
}

// appFile is a file in an open .ddapp: closing it closes the .ddapp too.
type appFile struct {
	fs.File
	zip *zip.ReadCloser
}

func (f appFile) Close() error {
	return errors.Join(f.File.Close(), f.zip.Close())
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
