package checks_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// testCA issues certificates for 127.0.0.1 that checks trusts when started
// with DefinitionTrusting(ca.pool).
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ddash test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

// serve runs an HTTPS server answering 200 with a certificate for
// 127.0.0.1 valid until notAfter, and returns its URL.
func (ca *testCA) serve(t *testing.T, notAfter time.Time) string {
	t.Helper()
	cert := ca.issue("127.0.0.1", notAfter)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.URL
}

// issue is a certificate for host (a name or an IP address) valid until
// notAfter.
func (ca *testCA) issue(host string, notAfter time.Time) tls.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-24 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		panic(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// answering runs a plain HTTP server answering status.
func answering(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

// firstAppBeat sets one App with url and waits, in real time, for its
// Check's first beat.
func firstAppBeat(t *testing.T, c checkspb.ChecksServiceClient, url string) *checkspb.Check {
	t.Helper()
	setApps(t, c, app("proxy:test.home.example", "Test", url))
	return firstBeat(t, c, appCheck(t, c, "proxy:test.home.example").GetId())
}

func TestAnHTTPCheckIsUpOnAnyStatusBelow500(t *testing.T) {
	for _, status := range []int{200, 204, 401, 404, 499} {
		_, c := start(t)
		got := firstAppBeat(t, c, answering(t, status))
		if got.GetState() != checkspb.State_STATE_UP || got.GetLatencyMs() <= 0 {
			t.Errorf("HTTP %d: %v (%q, latency %v), want up with a latency", status, got.GetState(), got.GetReason(), got.GetLatencyMs())
		}
	}
}

func TestAnHTTPCheckIsDownOn5xxWithTheStatusAsReason(t *testing.T) {
	for _, status := range []int{500, 502, 503} {
		_, c := start(t)
		got := firstAppBeat(t, c, answering(t, status))
		if want := "HTTP " + itoa(int64(status)); got.GetFailures() != 1 || got.GetReason() != want {
			t.Errorf("HTTP %d: %d failures (%q), want a failure: %s", status, got.GetFailures(), got.GetReason(), want)
		}
	}
}

func TestAnHTTPCheckDoesNotFollowRedirects(t *testing.T) {
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	t.Cleanup(redirect.Close)
	_, c := start(t)

	got := firstAppBeat(t, c, redirect.URL)
	if got.GetState() != checkspb.State_STATE_UP {
		t.Errorf("a 302 to a failing page: %v (%q), want up", got.GetState(), got.GetReason())
	}
	if n := followed.Load(); n != 0 {
		t.Errorf("the redirect was followed %d times", n)
	}
}

func TestAnHTTPCheckFailsOnAConnectionError(t *testing.T) {
	_, c := start(t)
	got := firstAppBeat(t, c, "http://"+closed(t))
	if got.GetFailures() != 1 || got.GetReason() != "connection refused" {
		t.Errorf("%d failures (%q), want a failure: connection refused", got.GetFailures(), got.GetReason())
	}
}

func TestAnHTTPSCheckFailsOnAnInvalidCertificate(t *testing.T) {
	ca := newCA(t)
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(untrusted.Close)
	for url, want := range map[string]string{
		untrusted.URL:                           "TLS: certificate signed by an unknown authority",
		ca.serve(t, time.Now().Add(-time.Hour)): "TLS: certificate expired",
	} {
		h := sdktest.New(t, checks.DefinitionTrusting(ca.pool))
		h.MustStart(&checkspb.ChecksConfig{})
		got := firstAppBeat(t, checkspb.NewChecksServiceClient(h.Conn), url)
		if got.GetFailures() != 1 || got.GetReason() != want {
			t.Errorf("%d failures (%q), want a failure: %s", got.GetFailures(), got.GetReason(), want)
		}
	}
}

func TestAnHTTPSCheckRecordsTheCertificatesExpiryAndWarnsWithin14Days(t *testing.T) {
	ca := newCA(t)
	for _, tc := range []struct {
		left      time.Duration
		expiring  bool
		situation string
	}{
		{10 * 24 * time.Hour, true, "expires in 10 days"},
		{30 * 24 * time.Hour, false, "expires in 30 days"},
	} {
		expires := time.Now().Add(tc.left).Truncate(time.Second)
		h := sdktest.New(t, checks.DefinitionTrusting(ca.pool))
		h.MustStart(&checkspb.ChecksConfig{})
		got := firstAppBeat(t, checkspb.NewChecksServiceClient(h.Conn), ca.serve(t, expires))

		if got.GetState() != checkspb.State_STATE_UP {
			t.Errorf("%s: %v (%q), want up", tc.situation, got.GetState(), got.GetReason())
		}
		if !got.GetCertExpires().AsTime().Equal(expires) {
			t.Errorf("%s: cert_expires %v, want %v", tc.situation, got.GetCertExpires().AsTime(), expires)
		}
		if got.GetCertExpiring() != tc.expiring {
			t.Errorf("%s: cert_expiring %v, want %v", tc.situation, got.GetCertExpiring(), tc.expiring)
		}
	}
}

func TestAnHTTPCheckHasNoCertificate(t *testing.T) {
	_, c := start(t)
	if got := firstAppBeat(t, c, answering(t, 200)); got.GetCertExpires() != nil || got.GetCertExpiring() {
		t.Errorf("cert_expires %v, expiring %v; want neither over plain HTTP", got.GetCertExpires(), got.GetCertExpiring())
	}
}
