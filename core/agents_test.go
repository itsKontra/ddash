package core_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
)

// counted counts the files asked of a release source: each one is a
// checksum over the whole file, or a download.
type counted struct {
	runtime.ReleaseSource
	files atomic.Int32
}

func (c *counted) File(ctx context.Context, name string) (runtime.Binary, error) {
	c.files.Add(1)
	return c.ReleaseSource.File(ctx, name)
}

func agentHost(t *testing.T) (*coretest.Harness, *counted) {
	opts := coretest.Options{Release: "v1.2.3", ReleaseFiles: map[string]string{
		runtime.HostAgentFile("amd64"): "the amd64 Host Agent",
		runtime.HostAgentFile("arm64"): "the arm64 Host Agent",
	}}
	src := &counted{ReleaseSource: runtime.Dir{Path: coretest.ReleaseDir(t, opts), Unpack: t.TempDir()}}
	opts.Source = src
	return coretest.New(t, opts), src
}

func TestTheHostAgentIsDownloadedWithItsChecksum(t *testing.T) {
	h, _ := agentHost(t)

	res := h.Get("/agent/host/amd64")

	sum := sha256.Sum256([]byte("the amd64 Host Agent"))
	if res.Code != http.StatusOK || res.Body.String() != "the amd64 Host Agent" || res.Header().Get("X-Checksum-Sha256") != hex.EncodeToString(sum[:]) {
		t.Errorf("status %d, body %q, checksum %q", res.Code, res.Body, res.Header().Get("X-Checksum-Sha256"))
	}
	if sums := h.Get("/agent/host/SHA256SUMS").Body.String(); !strings.Contains(sums, hex.EncodeToString(sum[:])+"  "+runtime.HostAgentFile("amd64")) {
		t.Errorf("SHA256SUMS = %q", sums)
	}
	if script := h.Get("/agent/host/install.sh").Body.String(); !strings.Contains(script, hex.EncodeToString(sum[:])) {
		t.Error("install.sh doesn't pin the amd64 checksum")
	}
	if res := h.Get("/agent/host/riscv64"); res.Code != http.StatusNotFound {
		t.Errorf("an unknown architecture: status %d", res.Code)
	}
}

// These answer anyone, signed in or not: asking again and again must not
// make the core check the binaries each time.
func TestTheHostAgentsFilesAreCheckedOnceNotPerRequest(t *testing.T) {
	h, src := agentHost(t)

	for range 5 {
		h.Get("/agent/host/install.sh")
		h.Get("/agent/host/SHA256SUMS")
		h.Get("/agent/host/amd64")
	}

	if got := src.files.Load(); got != 2 {
		t.Errorf("the release source was asked for %d files over 15 requests, want 2 (one per architecture)", got)
	}
}
