package nginxconfig_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	nginxconfig "github.com/itsKontra/ddash/components/nginx-config"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
)

func TestCaptureRunsNginxDashTWithTheComponentsOwnKey(t *testing.T) {
	srv := nginxHost(t, "config-test-failed")
	s := start(t, srv)
	s.authorize(t, srv)
	s.h.Stop()
	cfg := &nginxconfigpb.NginxConfigConfig{Host: srv.Addr(), User: proto.String("root"), HostKey: srv.HostKey()}

	out, err := nginxconfig.Capture(t.Context(), s.h.DBPath, cfg)

	if err != nil {
		t.Fatal(err)
	}
	wantErr, _ := os.ReadFile("testdata/config-test-failed/nginx_-T.stderr")
	if out.Stdout != "" || out.Stderr != string(wantErr) || out.Exit != 1 {
		t.Errorf("Capture = %q, %q, exit %d; want the host's answer", out.Stdout, out.Stderr, out.Exit)
	}
}

func TestCaptureReturnsTheWholeDump(t *testing.T) {
	srv := nginxHost(t, "baseline")
	s := start(t, srv)
	s.authorize(t, srv)
	s.h.Stop()
	cfg := &nginxconfigpb.NginxConfigConfig{Host: srv.Addr(), User: proto.String("root"), HostKey: srv.HostKey()}

	out, err := nginxconfig.Capture(t.Context(), s.h.DBPath, cfg)

	want, _ := os.ReadFile("testdata/baseline/nginx_-T.stdout")
	if err != nil || out.Stdout != string(want) || out.Exit != 0 {
		t.Errorf("Capture = %d bytes, exit %d, %v; want baseline's dump", len(out.Stdout), out.Exit, err)
	}
}

func TestCaptureNeedsTheKeyAStartMade(t *testing.T) {
	srv := nginxHost(t, "baseline")
	cfg := &nginxconfigpb.NginxConfigConfig{Host: srv.Addr(), User: proto.String("root"), HostKey: srv.HostKey()}

	_, err := nginxconfig.Capture(t.Context(), filepath.Join(t.TempDir(), "nginx-config.db"), cfg)

	if err == nil || !strings.Contains(err.Error(), "no key") {
		t.Errorf("err = %v, want no key yet", err)
	}
}
