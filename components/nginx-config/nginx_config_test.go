package nginxconfig_test

import (
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	nginxconfig "github.com/itsKontra/ddash/components/nginx-config"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// nginxHost fakes nginx's host: an SSH server answering `nginx -T` from a
// scenario's captured output.
func nginxHost(t *testing.T, scenario string) *fixture.SSH {
	return fixture.SSHServer(t, "testdata/"+scenario, fixture.SSHOptions{})
}

type started struct {
	h *sdktest.Harness
	c nginxconfigpb.NginxConfigServiceClient
}

func start(t *testing.T, srv *fixture.SSH, opts ...sdktest.Option) started {
	t.Helper()
	h := sdktest.New(t, nginxconfig.Definition(), opts...)
	h.MustStart(&nginxconfigpb.NginxConfigConfig{Host: srv.Addr(), User: proto.String("root"), HostKey: srv.HostKey()})
	return started{h, nginxconfigpb.NewNginxConfigServiceClient(h.Conn)}
}

func (s started) hint(t *testing.T) string {
	t.Helper()
	resp, err := s.c.GetSetupHint(t.Context(), &nginxconfigpb.GetSetupHintRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetHint().GetBlock()
}

// authorize pastes the setup hint's line into the fake host's
// authorized_keys, as the Operator would.
func (s started) authorize(t *testing.T, srv *fixture.SSH) {
	t.Helper()
	if err := srv.Authorize(s.hint(t)); err != nil {
		t.Fatalf("the hint isn't an authorized_keys line: %v", err)
	}
}

func (s started) snapshot(t *testing.T) *nginxconfigpb.Snapshot {
	t.Helper()
	resp, err := s.c.GetSnapshot(t.Context(), &nginxconfigpb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetSnapshot()
}

func (s started) refresh(t *testing.T) *nginxconfigpb.Snapshot {
	t.Helper()
	resp, err := s.c.Refresh(t.Context(), &nginxconfigpb.RefreshRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetSnapshot()
}

func TestTheSetupHintIsAnAuthorizedKeysLineThatCanOnlyDumpTheConfig(t *testing.T) {
	srv := nginxHost(t, "baseline")
	s := start(t, srv)

	resp, err := s.c.GetSetupHint(t.Context(), &nginxconfigpb.GetSetupHintRequest{})
	if err != nil {
		t.Fatal(err)
	}

	line := regexp.MustCompile(`^restrict,from="127\.0\.0\.1",command="/usr/sbin/nginx -T" ssh-ed25519 AAAA[A-Za-z0-9+/=]+ ddash$`)
	if !line.MatchString(resp.GetHint().GetBlock()) {
		t.Errorf("block = %q, want an authorized_keys line like %s", resp.GetHint().GetBlock(), line)
	}
	if text := resp.GetHint().GetText(); !strings.Contains(text, "root") || !strings.Contains(text, srv.Addr()) {
		t.Errorf("text = %q, want it to name the user root and the host %s", text, srv.Addr())
	}
}

func TestTheKeyPairIsGeneratedOnceAndKept(t *testing.T) {
	srv := nginxHost(t, "baseline")
	db := filepath.Join(t.TempDir(), "nginx-config.db")

	first := start(t, srv, sdktest.WithDB(db))
	before := first.hint(t)
	first.h.Stop()
	after := start(t, srv, sdktest.WithDB(db)).hint(t)

	if before != after {
		t.Errorf("the key changed across a restart:\n%s\n%s", before, after)
	}
	if other := start(t, srv).hint(t); other == before {
		t.Error("two fresh Components generated the same key")
	}
}

func TestUntilTheKeyIsAuthorizedHealthIsPermissionDenied(t *testing.T) {
	srv := nginxHost(t, "baseline")
	s := start(t, srv)

	if got := s.h.Health(); got != (sdk.Health{State: sdk.Down, Message: "permission denied (publickey)"}) {
		t.Errorf("health = %v, want down: permission denied (publickey)", got)
	}

	s.authorize(t, srv)

	if got := s.h.Health(); got.State != sdk.OK {
		t.Errorf("health after the key was pasted = %v, want ok", got)
	}
}

// acme is the two ACME challenge locations Nginx Proxy Manager includes in
// its blocks.
var acme = []*nginxconfigpb.Location{
	{Path: "^~ /.well-known/acme-challenge/"},
	{Path: "= /.well-known/acme-challenge/"},
}

// assets is the static-assets location of Nginx Proxy Manager's
// assets.conf, proxying to where upstream says.
func assets(upstream string) *nginxconfigpb.Location {
	return &nginxconfigpb.Location{
		Path:      `~* ^.*\.(css|js|jpe?g|gif|png|webp|woff|eot|ttf|svg|ico|css\.map|js\.map)$`,
		ProxyPass: upstream + "$request_uri",
	}
}

// npmHost is a Proxy Host as Nginx Proxy Manager writes it to
// /data/nginx/proxy_host/<n>.conf: on 80 and 443 with its Let's Encrypt
// certificate (on [::]:443 too unless v6 is off), forwarding "/" to
// "$forward_scheme://$server:$port$request_uri" with the variables it sets,
// after the ACME locations and any of more.
func npmHost(n int, names []string, v6 bool, upstream string, more ...*nginxconfigpb.Location) *nginxconfigpb.Server {
	listens := []*nginxconfigpb.Listen{{Address: "80"}, {Address: "[::]:80"}, {Address: "443", Ssl: true}}
	if v6 {
		listens = append(listens, &nginxconfigpb.Listen{Address: "[::]:443", Ssl: true})
	}
	locations := append(slices.Clone(acme), more...)
	locations = append(locations, &nginxconfigpb.Location{Path: "/", ProxyPass: upstream + "$request_uri"})
	return &nginxconfigpb.Server{
		File: "/data/nginx/proxy_host/" + strconv.Itoa(n) + ".conf", Line: 6,
		ServerNames:    names,
		Listens:        listens,
		SslCertificate: "/etc/letsencrypt/live/npm-3/fullchain.pem",
		Locations:      locations,
	}
}

// The homelab's nginx is Nginx Proxy Manager: its own fallback, admin and
// default-host blocks, then one block per Proxy Host.
func TestReadsEveryServerBlockNginxLoads(t *testing.T) {
	srv := nginxHost(t, "baseline")
	s := start(t, srv)
	s.authorize(t, srv)

	got := s.refresh(t)

	want := []*nginxconfigpb.Server{
		{
			File: "/etc/nginx/conf.d/default.conf", Line: 2,
			ServerNames: []string{"localhost-nginx-proxy-manager"},
			Listens:     []*nginxconfigpb.Listen{{Address: "80"}, {Address: "[::]:80"}},
			Locations:   append([]*nginxconfigpb.Location{assets("http://127.0.0.1:80")}, append(slices.Clone(acme), &nginxconfigpb.Location{Path: "/"})...),
		},
		{
			File: "/etc/nginx/conf.d/default.conf", Line: 24,
			ServerNames: []string{"localhost"},
			Listens:     []*nginxconfigpb.Listen{{Address: "443", Ssl: true}, {Address: "[::]:443", Ssl: true}},
			Return:      "444",
		},
		{
			File: "/etc/nginx/conf.d/production.conf", Line: 2,
			ServerNames: []string{"nginxproxymanager"},
			Listens:     []*nginxconfigpb.Listen{{Address: "81", DefaultServer: true}, {Address: "[::]:81", DefaultServer: true}},
			Locations:   []*nginxconfigpb.Location{{Path: "/api"}, {Path: "/api/", ProxyPass: "http://127.0.0.1:3000/"}, {Path: "/"}},
		},
		{
			File: "/data/nginx/default_host/site.conf", Line: 5,
			ServerNames: []string{"default-host.localhost"},
			Listens:     []*nginxconfigpb.Listen{{Address: "80", DefaultServer: true}, {Address: "[::]:80", DefaultServer: true}},
			Locations:   append(slices.Clone(acme), &nginxconfigpb.Location{Path: "/"}),
		},
		npmHost(1, []string{"home.example", "nginx.home.example"}, true, "http://198.18.1.210:81"),
		npmHost(10, []string{"wlan0.home.example"}, false, "https://198.18.1.2:443"),
		npmHost(11, []string{"backup.home.example"}, true, "https://198.18.1.51:8007"),
		npmHost(13, []string{"fullstack.home.example"}, false, "http://198.18.1.201:3080"),
		npmHost(2, []string{"fritz.home.example"}, false, "http://203.0.113.100:80"),
		npmHost(21, []string{"n8n.home.example"}, true, "http://198.18.1.206:5678",
			&nginxconfigpb.Location{Path: "/aichat", ProxyPass: "http://198.18.1.206:5678"}),
		npmHost(25, []string{"md.home.example"}, true, "http://198.18.1.201:3077", assets("http://198.18.1.201:3077")),
		npmHost(27, []string{"ha.home.example"}, true, "http://198.18.1.101:8123"),
		npmHost(28, []string{"pv.home.example"}, true, "https://198.18.1.7:443"),
		npmHost(29, []string{"nodered.home.example"}, true, "https://198.18.1.7:1881"),
		npmHost(3, []string{"grafana.home.example"}, true, "http://198.18.1.204:3000"),
		npmHost(33, []string{"request.home.example"}, true, "http://198.18.1.212:5055"),
		npmHost(34, []string{"video.home.example"}, true, "http://198.18.1.212:8096"),
		npmHost(35, []string{"movie.home.example"}, true, "http://198.18.1.212:7878"),
		npmHost(36, []string{"series.home.example"}, true, "http://198.18.1.212:8989"),
		npmHost(37, []string{"indexer.home.example"}, true, "http://198.18.1.212:9696"),
		npmHost(38, []string{"download.home.example"}, true, "http://198.18.1.212:8282"),
		npmHost(4, []string{"home.home.example"}, false, "http://198.18.1.201:3000"),
		npmHost(5, []string{"influxdb.home.example"}, false, "http://198.18.1.203:8086"),
		npmHost(6, []string{"keycloak.home.example", "local.home.example"}, false, "https://198.18.1.201:8443"),
		npmHost(7, []string{"portainer.home.example"}, false, "http://198.18.1.201:9000"),
		npmHost(8, []string{"pve.home.example"}, false, "https://198.18.1.200:8006"),
		npmHost(9, []string{"wlan1.home.example"}, false, "https://198.18.1.3:443"),
	}
	if diff := cmp.Diff(want, got.GetServers(), protocmp.Transform()); diff != "" {
		t.Errorf("servers (-want +got):\n%s", diff)
	}
	if got.GetError() != "" || got.GetRevision() < 1 {
		t.Errorf("error %q, revision %d; want none and at least 1", got.GetError(), got.GetRevision())
	}
	if age := time.Since(got.GetReadAt().AsTime()); age < 0 || age > time.Minute {
		t.Errorf("read_at = %v, want just now", got.GetReadAt().AsTime())
	}
}

// The homelab has no upstream blocks (Nginx Proxy Manager writes none):
// upstream-block is a hand-written config with one.
func TestReadsEveryUpstreamBlockWithItsServers(t *testing.T) {
	srv := nginxHost(t, "upstream-block")
	s := start(t, srv)
	s.authorize(t, srv)

	got := s.refresh(t)

	want := []*nginxconfigpb.Upstream{{Name: "grafana", Servers: []string{"198.18.2.20:3000"}}}
	if diff := cmp.Diff(want, got.GetUpstreams(), protocmp.Transform()); diff != "" {
		t.Errorf("upstreams (-want +got):\n%s", diff)
	}
}

func TestAFailedConfigTestKeepsNginxsErrorText(t *testing.T) {
	srv := nginxHost(t, "config-test-failed")
	s := start(t, srv)
	s.authorize(t, srv)

	got := s.refresh(t)

	wantErr := "nginx: [emerg] unknown directive \"proxy_pas\" in /etc/nginx/conf.d/jellyfin.conf:11\nnginx: configuration file /etc/nginx/nginx.conf test failed"
	if got.GetError() != wantErr {
		t.Errorf("error = %q, want nginx's own text %q", got.GetError(), wantErr)
	}
	if got.GetReadAt() != nil || len(got.GetServers()) != 0 {
		t.Errorf("read_at %v, %d servers; want neither before a good read", got.GetReadAt(), len(got.GetServers()))
	}
	if health := s.h.Health(); health.State != sdk.Degraded || !strings.Contains(health.Message, "config test failed") {
		t.Errorf("health = %v, want degraded: config test failed", health)
	}
}

func TestAReadThatFailsAfterAGoodOneKeepsTheLastGoodServers(t *testing.T) {
	srv := nginxHost(t, "baseline")
	s := start(t, srv)
	s.authorize(t, srv)
	good := s.refresh(t)

	srv.Use("testdata/config-test-failed")
	bad := s.refresh(t)

	if bad.GetError() == "" || !proto.Equal(bad.GetReadAt(), good.GetReadAt()) || len(bad.GetServers()) != len(good.GetServers()) {
		t.Errorf("after a failed read: error %q, read_at %v, %d servers; want the error with the last good read (%v, %d servers)",
			bad.GetError(), bad.GetReadAt(), len(bad.GetServers()), good.GetReadAt(), len(good.GetServers()))
	}
	if bad.GetRevision() != good.GetRevision()+1 {
		t.Errorf("revision %d → %d, want it to grow by one", good.GetRevision(), bad.GetRevision())
	}

	srv.Use("testdata/baseline")
	fixed := s.refresh(t)

	if fixed.GetError() != "" || fixed.GetRevision() != bad.GetRevision()+1 {
		t.Errorf("after the fix: error %q, revision %d; want none, %d", fixed.GetError(), fixed.GetRevision(), bad.GetRevision()+1)
	}
}

func TestTheRevisionStaysWhileNothingChanges(t *testing.T) {
	srv := nginxHost(t, "baseline")
	s := start(t, srv)
	s.authorize(t, srv)

	first := s.refresh(t)
	second := s.refresh(t)

	if first.GetRevision() != second.GetRevision() {
		t.Errorf("revision %d → %d for the same config, want unchanged", first.GetRevision(), second.GetRevision())
	}
	if !second.GetReadAt().AsTime().After(first.GetReadAt().AsTime()) && !second.GetReadAt().AsTime().Equal(first.GetReadAt().AsTime()) {
		t.Errorf("read_at went back: %v → %v", first.GetReadAt().AsTime(), second.GetReadAt().AsTime())
	}
}

func TestTheSnapshotIsServedFromMemoryAndRefreshRereadsAtOnce(t *testing.T) {
	srv := nginxHost(t, "baseline")
	s := start(t, srv)
	s.authorize(t, srv)
	s.refresh(t)
	before := srv.Hits("/usr/sbin/nginx -T")

	s.snapshot(t)
	s.snapshot(t)
	s.h.Health()
	if n := srv.Hits("/usr/sbin/nginx -T") - before; n != 0 {
		t.Errorf("nginx asked %d more times for reads of a good snapshot, want none", n)
	}

	s.refresh(t)
	if n := srv.Hits("/usr/sbin/nginx -T") - before; n != 1 {
		t.Errorf("Refresh asked nginx %d times, want once", n)
	}
}

func TestAnotherHostKeyIsRefused(t *testing.T) {
	srv := nginxHost(t, "baseline")
	impostor := nginxHost(t, "baseline")
	h := sdktest.New(t, nginxconfig.Definition())
	h.MustStart(&nginxconfigpb.NginxConfigConfig{Host: srv.Addr(), HostKey: impostor.HostKey()})

	if got := h.Health(); got.State != sdk.Down || !strings.Contains(got.Message, "host key mismatch") {
		t.Errorf("health = %v, want down: host key mismatch", got)
	}
}

func TestUnreachableIsDown(t *testing.T) {
	srv := nginxHost(t, "baseline")
	s := start(t, srv)
	srv.Close()

	if got := s.h.Health(); got.State != sdk.Down || !strings.Contains(got.Message, "unreachable") {
		t.Errorf("health = %v, want down: unreachable", got)
	}
}

func TestStartReportsConfigErrorsByPath(t *testing.T) {
	h := sdktest.New(t, nginxconfig.Definition())

	err := h.Start(&nginxconfigpb.NginxConfigConfig{Host: "nginx.home.example:ssh", HostKey: "ssh-ed25519 not-base64"})

	var paths []string
	for _, fe := range sdk.FieldErrorsOf(err) {
		paths = append(paths, fe.Path)
	}
	if want := []string{"host", "host_key"}; !slices.Equal(paths, want) {
		t.Errorf("errors at %v, want %v (%v)", paths, want, err)
	}
}

func TestTheHostKeyMayBeAKnownHostsLine(t *testing.T) {
	srv := nginxHost(t, "baseline")
	h := sdktest.New(t, nginxconfig.Definition())
	h.MustStart(&nginxconfigpb.NginxConfigConfig{Host: srv.Addr(), HostKey: "nginx.home.example " + srv.HostKey()})
	s := started{h, nginxconfigpb.NewNginxConfigServiceClient(h.Conn)}
	s.authorize(t, srv)

	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health = %v, want ok", got)
	}
}
