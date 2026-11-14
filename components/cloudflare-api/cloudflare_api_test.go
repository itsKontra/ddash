package cloudflareapi_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	cloudflareapi "github.com/itsKontra/ddash/components/cloudflare-api"
	pb "github.com/itsKontra/ddash/components/cloudflare-api/cloudflareapipb"
	"github.com/itsKontra/ddash/components/cloudflare-api/cloudflarefake"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// lab is cloudflare-api started against a cloudflarefake serving a
// scenario.
type lab struct {
	h    *sdktest.Harness
	c    pb.CloudflareApiServiceClient
	fake *cloudflarefake.Cloudflare
}

func ptr[T any](v T) *T { return &v }

func config(url string) *pb.CloudflareApiConfig {
	return &pb.CloudflareApiConfig{AccountId: cloudflarefake.AccountID, TunnelId: cloudflarefake.TunnelID, ApiToken: cloudflarefake.DefaultToken, ApiUrl: ptr(url)}
}

func start(t *testing.T, scenario string, edit ...func(*pb.CloudflareApiConfig)) *lab {
	t.Helper()
	fake, err := cloudflarefake.New("testdata/"+scenario, cloudflarefake.Options{Miss: func(r string, err error) { t.Errorf("unanswered %s: %v", r, err) }})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	cfg := config(srv.URL)
	for _, e := range edit {
		e(cfg)
	}
	h := sdktest.New(t, cloudflareapi.Definition())
	h.MustStart(cfg)
	return &lab{h: h, c: pb.NewCloudflareApiServiceClient(h.Conn), fake: fake}
}

func TestAnEmptyConfigStartsNotSetUp(t *testing.T) {
	h := sdktest.New(t, cloudflareapi.Definition())
	h.MustStart(&pb.CloudflareApiConfig{})
	c := pb.NewCloudflareApiServiceClient(h.Conn)

	got, err := c.GetTunnel(t.Context(), &pb.GetTunnelRequest{})
	if err != nil || got.GetSetUp() || got.GetTunnel() != nil {
		t.Errorf("GetTunnel = %v, %v; want set_up false and nothing else", got, err)
	}
	_, err = c.GetConfiguration(t.Context(), &pb.GetConfigurationRequest{})
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != "not set up" {
		t.Errorf("GetConfiguration = %v, want FAILED_PRECONDITION not set up", err)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health = %v, want ok", got)
	}
}

func TestAPartlyFilledConfigIsRefusedPerField(t *testing.T) {
	for _, tc := range []struct {
		cfg  *pb.CloudflareApiConfig
		want []string
	}{
		{&pb.CloudflareApiConfig{AccountId: cloudflarefake.AccountID}, []string{"tunnel_id", "api_token"}},
		{&pb.CloudflareApiConfig{AccountId: "acme", TunnelId: cloudflarefake.TunnelID, ApiToken: "t"}, []string{"account_id"}},
		{&pb.CloudflareApiConfig{AccountId: cloudflarefake.AccountID, TunnelId: "home", ApiToken: "t"}, []string{"tunnel_id"}},
	} {
		h := sdktest.New(t, cloudflareapi.Definition())
		var paths []string
		for _, e := range sdk.FieldErrorsOf(h.Start(tc.cfg)) {
			paths = append(paths, e.Path)
		}
		if !slices.Equal(paths, tc.want) {
			t.Errorf("Start(%v) field errors on %v, want %v", tc.cfg, paths, tc.want)
		}
	}
}

func TestGetTunnelReadsItsStateAndConnectorsWithTheToken(t *testing.T) {
	l := start(t, "baseline")

	got, err := l.c.GetTunnel(t.Context(), &pb.GetTunnelRequest{})
	if err != nil {
		t.Fatal(err)
	}

	tun := got.GetTunnel()
	if !got.GetSetUp() || tun.GetName() != "home" || tun.GetStatus() != pb.TunnelStatus_TUNNEL_STATUS_HEALTHY || !tun.GetRemotelyManaged() {
		t.Errorf("tunnel = %v, want home, healthy, remotely managed", tun)
	}
	if tun.GetCnameTarget() != cloudflarefake.Target {
		t.Errorf("CNAME target = %q, want %q", tun.GetCnameTarget(), cloudflarefake.Target)
	}
	var connectors []string
	for _, k := range tun.GetConnectors() {
		var colos []string
		for _, x := range k.GetConnections() {
			colos = append(colos, x.GetColoName()+" from "+x.GetOriginIp())
		}
		connectors = append(connectors, k.GetVersion()+" "+k.GetArch()+": "+strings.Join(colos, ", "))
	}
	want := []string{
		"2025.10.0 linux_amd64: fra06 from 203.0.113.7, ams08 from 203.0.113.7",
		"2025.9.1 linux_arm64: fra10 from 203.0.113.7, ams01 from 203.0.113.7",
	}
	if diff := cmp.Diff(want, connectors); diff != "" {
		t.Errorf("connectors (-want +got):\n%s", diff)
	}
	if got := l.h.Health(); got.State != sdk.OK {
		t.Errorf("health = %v, want ok", got)
	}
}

func TestADownTunnelHasNoConnectors(t *testing.T) {
	l := start(t, "tunnel-down")

	got, err := l.c.GetTunnel(t.Context(), &pb.GetTunnelRequest{})
	if err != nil || got.GetTunnel().GetStatus() != pb.TunnelStatus_TUNNEL_STATUS_DOWN || len(got.GetTunnel().GetConnectors()) != 0 {
		t.Errorf("GetTunnel = %v, %v; want down without connectors", got, err)
	}
	// Reaching Cloudflare works: the Component is fine.
	if h := l.h.Health(); h.State != sdk.OK {
		t.Errorf("health = %v, want ok", h)
	}
}

func TestARefusedTokenMakesItDown(t *testing.T) {
	l := start(t, "baseline", func(c *pb.CloudflareApiConfig) { c.ApiToken = "revoked" })

	_, err := l.c.GetTunnel(t.Context(), &pb.GetTunnelRequest{})

	want := "Cloudflare refused the API token: Authentication error (10000)"
	if status.Code(err) != codes.PermissionDenied || status.Convert(err).Message() != want {
		t.Errorf("GetTunnel = %v, want PERMISSION_DENIED %s", err, want)
	}
	if h := l.h.Health(); h.State != sdk.Down || h.Message != want {
		t.Errorf("health = %v, want down: %s", h, want)
	}
}

func TestAWrongTunnelIDCantBeRouted(t *testing.T) {
	l := start(t, "baseline", func(c *pb.CloudflareApiConfig) { c.TunnelId = "00000000-0000-4000-8000-000000000000" })

	_, err := l.c.GetTunnel(t.Context(), &pb.GetTunnelRequest{})

	if status.Code(err) != codes.NotFound || !strings.Contains(status.Convert(err).Message(), "Could not route") {
		t.Errorf("GetTunnel = %v, want NOT_FOUND Could not route", err)
	}
}

func TestGetConfigurationListsTheRulesWithoutTheCatchAll(t *testing.T) {
	l := start(t, "baseline")

	got, err := l.c.GetConfiguration(t.Context(), &pb.GetConfigurationRequest{})
	if err != nil {
		t.Fatal(err)
	}

	want := &pb.GetConfigurationResponse{
		Version: 7,
		Rules: []*pb.IngressRule{
			{Hostname: "request.home.example", Service: "http://198.18.1.212:5055"},
			{Hostname: "video.home.example", Service: "http://198.18.1.212:8096", OriginRequest: `{"connectTimeout":30,"keepAliveTimeout":90}`},
			{Hostname: "ha.home.example", Service: "http://198.18.1.101:8123"},
		},
		CatchAll: "http_status:404",
	}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("configuration (-want +got):\n%s", diff)
	}
}

func TestZonesAndTheirDNSRecordsAreListed(t *testing.T) {
	l := start(t, "baseline")

	zones, err := l.c.ListZones(t.Context(), &pb.ListZonesRequest{})
	if err != nil || len(zones.GetZones()) != 1 || zones.GetZones()[0].GetName() != "home.example" || zones.GetZones()[0].GetId() != cloudflarefake.ZoneID {
		t.Fatalf("ListZones = %v, %v; want home.example", zones, err)
	}
	recs, err := l.c.ListDnsRecords(t.Context(), &pb.ListDnsRecordsRequest{ZoneId: cloudflarefake.ZoneID})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range recs.GetRecords() {
		f := r.GetFields()
		got = append(got, f.GetType()+" "+f.GetName()+" "+f.GetContent())
	}
	want := []string{
		"CNAME ha.home.example " + cloudflarefake.Target,
		"A home.example 203.0.113.7",
		"CNAME request.home.example " + cloudflarefake.Target,
		"CNAME video.home.example " + cloudflarefake.Target,
		"A vpn.home.example 203.0.113.7",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("records (-want +got):\n%s", diff)
	}
	if !slices.Contains(l.fake.Log(), "GET /zones?account.id="+cloudflarefake.AccountID+"&page=1&per_page=50") {
		t.Errorf("requests %v, want the account's zones read", l.fake.Log())
	}
}

func TestVerifyAccessReadsTheTunnelAndTheZones(t *testing.T) {
	l := start(t, "baseline")

	got, err := l.c.VerifyAccess(t.Context(), &pb.VerifyAccessRequest{})

	if err != nil || got.GetTunnelName() != "home" || !got.GetRemotelyManaged() || got.GetZones() != 1 {
		t.Errorf("VerifyAccess = %v, %v; want home, remotely managed, 1 zone", got, err)
	}
}

func TestReadsAreCachedUntilFresh(t *testing.T) {
	l := start(t, "baseline")
	count := func() int {
		n := 0
		for _, r := range l.fake.Log() {
			if r == http.MethodGet+" /accounts/"+cloudflarefake.AccountID+"/cfd_tunnel/"+cloudflarefake.TunnelID+"/configurations" {
				n++
			}
		}
		return n
	}
	for range 2 {
		if _, err := l.c.GetConfiguration(t.Context(), &pb.GetConfigurationRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	if count() != 1 {
		t.Errorf("configuration read %d times, want once through the cache", count())
	}
	if _, err := l.c.GetConfiguration(t.Context(), &pb.GetConfigurationRequest{Fresh: true}); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Errorf("configuration read %d times, want a fresh read", count())
	}
}
