package nginxproxymanagerapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	nginxproxymanagerapi "github.com/itsKontra/ddash/components/nginx-proxy-manager-api"
	pb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	"github.com/itsKontra/ddash/components/nginx-proxy-manager-api/npmfake"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

const (
	email    = "ddash@home.example"
	password = "correct horse battery staple"
)

// fakeNPM is Nginx Proxy Manager serving a scenario.
type fakeNPM struct {
	*httptest.Server
	*npmfake.NPM
	// failReads makes reads of Proxy Hosts and certificates answer 500.
	failReads atomic.Bool
}

func npm(t *testing.T, scenario string) *fakeNPM {
	return npmWith(t, scenario, npmfake.Options{})
}

func npmWith(t *testing.T, scenario string, opts npmfake.Options) *fakeNPM {
	t.Helper()
	opts.Miss = func(request string, err error) { t.Errorf("fixture: %v", err) }
	if opts.Password == "" {
		opts.Password = password
	}
	f, err := npmfake.New("testdata/"+scenario, opts)
	if err != nil {
		t.Fatal(err)
	}
	n := &fakeNPM{NPM: f}
	n.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.failReads.Load() && r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/nginx/") {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"error":{"code":500,"message":"Internal Error"}}`))
			return
		}
		f.ServeHTTP(w, r)
	}))
	t.Cleanup(n.Close)
	return n
}

func start(t *testing.T, url string) (*sdktest.Harness, pb.NginxProxyManagerApiServiceClient) {
	t.Helper()
	h := sdktest.New(t, nginxproxymanagerapi.Definition())
	h.MustStart(&pb.NginxProxyManagerApiConfig{Url: url, Email: email, Password: password})
	return h, pb.NewNginxProxyManagerApiServiceClient(h.Conn)
}

// count is how many entries of log are request.
func count(log []string, request string) int {
	n := 0
	for _, e := range log {
		if e == request {
			n++
		}
	}
	return n
}

func TestListsTheProxyHostsSignedInAsDdashsUser(t *testing.T) {
	f := npm(t, "baseline")
	_, c := start(t, f.URL)

	got, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if n := len(got.GetProxyHosts()); n != 23 {
		t.Errorf("%d Proxy Hosts, want the baseline's 23", n)
	}
	// v2.10's flags read 0/1.
	want := &pb.ProxyHost{
		Id: 1,
		Fields: &pb.ProxyHostFields{
			DomainNames:   []string{"home.example", "nginx.home.example"},
			ForwardScheme: "http", ForwardHost: "198.18.1.210", ForwardPort: 81,
			BlockExploits: true, CertificateId: 3, ForceSsl: true, Http2: true,
		},
		Enabled: true,
	}
	var home *pb.ProxyHost
	for _, p := range got.GetProxyHosts() {
		if p.GetId() == 1 {
			home = p
		}
	}
	if diff := cmp.Diff(want, home, protocmp.Transform()); diff != "" {
		t.Errorf("Proxy Host 1 (-want +got):\n%s", diff)
	}
	if first := got.GetProxyHosts()[0].GetFields().GetDomainNames(); first[0] != "backup.home.example" {
		t.Errorf("first Proxy Host = %v, want Nginx Proxy Manager's order (backup.home.example first)", first)
	}
	if n := count(f.Log(), http.MethodPost+" /api/tokens"); n != 1 {
		t.Errorf("signed in %d times, want once", n)
	}
}

func TestReadsTheFlagsLaterVersionsSendAsTrueAndFalse(t *testing.T) {
	f := npm(t, "later-version")
	_, c := start(t, f.URL)

	got, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	want := &pb.ListProxyHostsResponse{ProxyHosts: []*pb.ProxyHost{
		{Id: 13, Fields: &pb.ProxyHostFields{DomainNames: []string{"fullstack.home.example"}, ForwardScheme: "http", ForwardHost: "198.18.1.201", ForwardPort: 3080}},
		{Id: 3, Fields: &pb.ProxyHostFields{
			DomainNames: []string{"grafana.home.example"}, ForwardScheme: "http", ForwardHost: "198.18.1.204", ForwardPort: 3000,
			BlockExploits: true, Websockets: true, CertificateId: 3, ForceSsl: true, Http2: true, Hsts: true,
		}, Enabled: true},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListProxyHosts (-want +got):\n%s", diff)
	}
}

func TestAProxyHostNginxRejectedIsOfflineWithNginxsErrorAndTheHealthStaysOK(t *testing.T) {
	f := npm(t, "offline-proxy-host")
	h, c := start(t, f.URL)

	got, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	var wiki *pb.ProxyHost
	for _, p := range got.GetProxyHosts() {
		if p.GetId() == 39 {
			wiki = p
		} else if p.GetOffline() || p.GetNginxError() != "" {
			t.Errorf("Proxy Host %d is offline, want only 39", p.GetId())
		}
	}
	wantErr := "nginx: [emerg] unknown directive \"proxy_bufferz\" in /data/nginx/proxy_host/39.conf:58\nnginx: configuration file /etc/nginx/nginx.conf test failed\n"
	if !wiki.GetOffline() || wiki.GetNginxError() != wantErr || !wiki.GetEnabled() {
		t.Errorf("Proxy Host 39 = %v, want enabled, Offline with nginx's error", wiki)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("Health = %v, want ok: an Offline Proxy Host doesn't change it", got)
	}
}

func TestListsTheCertificatesWithTheirExpiry(t *testing.T) {
	f := npm(t, "baseline")
	_, c := start(t, f.URL)

	got, err := c.ListCertificates(t.Context(), &pb.ListCertificatesRequest{})
	if err != nil {
		t.Fatal(err)
	}

	want := &pb.ListCertificatesResponse{Certificates: []*pb.Certificate{
		{Id: 3, Provider: "letsencrypt", NiceName: "*.home.example", DomainNames: []string{"*.home.example", "home.example"}, ExpiresOn: timestamppb.New(time.Date(2026, 12, 29, 2, 0, 13, 0, time.UTC))},
		{Id: 1, Provider: "other", NiceName: "NAS (uploaded)", DomainNames: []string{"nas.home.example"}, ExpiresOn: timestamppb.New(time.Date(2025, 5, 2, 19, 20, 0, 0, time.UTC))},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListCertificates (-want +got):\n%s", diff)
	}
}

func TestStatusIsUpWithTheVersionTheReadTimeAndTheReverseProxysAddress(t *testing.T) {
	f := npm(t, "baseline")
	// A name, resolved: localhost.
	_, c := start(t, strings.Replace(f.URL, "127.0.0.1", "localhost", 1))
	before := time.Now()

	got, err := c.GetStatus(t.Context(), &pb.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if got.GetStatus() != pb.Status_STATUS_UP || got.GetError() != "" || got.GetVersion() != "2.10.4" || got.GetProxyHostsReadOnly() || got.GetAddress() != "127.0.0.1" {
		t.Errorf("GetStatus = %v, want Up, 2.10.4, writable, at 127.0.0.1", got)
	}
	if read := got.GetReadAt().AsTime(); read.Before(before) || !read.Equal(got.GetLastGoodReadAt().AsTime()) {
		t.Errorf("read at %v, last good read at %v; want both now", read, got.GetLastGoodReadAt().AsTime())
	}
}

func TestAReadFailingAfterAGoodOneIsWarnWithTheLastGoodRead(t *testing.T) {
	f := npm(t, "baseline")
	h, c := start(t, f.URL)
	good, err := c.GetStatus(t.Context(), &pb.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	f.failReads.Store(true)

	_, err = c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{Fresh: true})

	want := "Nginx Proxy Manager answered GET /api/nginx/proxy-hosts with 500: Internal Error"
	if status.Code(err) != codes.Unavailable || status.Convert(err).Message() != want {
		t.Errorf("ListProxyHosts error = %v, want Unavailable: %s", err, want)
	}
	got, err := c.GetStatus(t.Context(), &pb.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetStatus() != pb.Status_STATUS_WARN || got.GetError() != want || !got.GetLastGoodReadAt().AsTime().Equal(good.GetLastGoodReadAt().AsTime()) || !got.GetReadAt().AsTime().After(good.GetReadAt().AsTime()) {
		t.Errorf("GetStatus = %v, want Warn with the error and the earlier good read", got)
	}
	if got := h.Health(); got.State != sdk.Degraded || got.Message != want {
		t.Errorf("Health = %v, want degraded: %s", got, want)
	}

	f.failReads.Store(false)
	if _, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{Fresh: true}); err != nil {
		t.Fatal(err)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("Health = %v, want ok after a good read", got)
	}
}

func TestStatusIsDownWhenNginxProxyManagerDoesntAnswer(t *testing.T) {
	f := npm(t, "baseline")
	h, c := start(t, f.URL)
	if _, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{}); err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{Fresh: true})

	if status.Code(err) != codes.Unavailable || !strings.HasPrefix(status.Convert(err).Message(), "Nginx Proxy Manager doesn't answer at "+f.URL) {
		t.Errorf("ListProxyHosts error = %v, want Unavailable: doesn't answer", err)
	}
	if got := h.Health(); got.State != sdk.Down {
		t.Errorf("Health = %v, want down even after a good read", got)
	}
	if got, _ := c.GetStatus(t.Context(), &pb.GetStatusRequest{}); got.GetStatus() != pb.Status_STATUS_DOWN || got.GetLastGoodReadAt() == nil {
		t.Errorf("GetStatus = %v, want Down, keeping the last good read's time", got)
	}
}

func TestFreshBypassesAndRefillsTheCache(t *testing.T) {
	for _, tc := range []struct {
		request string
		list    func(c pb.NginxProxyManagerApiServiceClient, fresh bool) error
	}{
		{"GET /api/nginx/proxy-hosts", func(c pb.NginxProxyManagerApiServiceClient, fresh bool) error {
			_, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{Fresh: fresh})
			return err
		}},
		{"GET /api/nginx/certificates", func(c pb.NginxProxyManagerApiServiceClient, fresh bool) error {
			_, err := c.ListCertificates(t.Context(), &pb.ListCertificatesRequest{Fresh: fresh})
			return err
		}},
	} {
		t.Run(tc.request, func(t *testing.T) {
			f := npm(t, "baseline")
			_, c := start(t, f.URL)

			for i, fresh := range []bool{false, false, true, false} {
				if err := tc.list(c, fresh); err != nil {
					t.Fatalf("read %d: %v", i, err)
				}
			}

			// The second read is cached, the fresh third isn't, and the
			// fourth gets what the third read.
			if n := count(f.Log(), tc.request); n != 2 {
				t.Errorf("%d reads, want 2: %v", n, f.Log())
			}
		})
	}
}

func TestRefreshDropsEveryCacheAndSignsInAgain(t *testing.T) {
	f := npm(t, "baseline")
	_, c := start(t, f.URL)
	if _, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListCertificates(t.Context(), &pb.ListCertificatesRequest{}); err != nil {
		t.Fatal(err)
	}
	// Proxy Hosts went to View since the sign-in: Refresh notices.
	f.EditUser(func(u *npmfake.User) { u.ProxyHosts = "view" })

	if _, err := c.Refresh(t.Context(), &pb.RefreshRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListCertificates(t.Context(), &pb.ListCertificatesRequest{}); err != nil {
		t.Fatal(err)
	}

	log := f.Log()
	if count(log, "GET /api/nginx/proxy-hosts") != 2 || count(log, "GET /api/nginx/certificates") != 2 || count(log, signIn) != 2 {
		t.Errorf("after Refresh: %v, want both lists read again after a second sign-in", log)
	}
	if st, _ := c.GetStatus(t.Context(), &pb.GetStatusRequest{}); !st.GetProxyHostsReadOnly() {
		t.Errorf("GetStatus = %v, want Proxy Hosts read-only after the new sign-in", st)
	}
}
