package proxmoxapi_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	proxmoxapi "github.com/itsKontra/ddash/components/proxmox-api"
	pb "github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
)

// pve serves the baseline over TLS with a certificate valid only for
// pve.home.example, as a node with an ACME certificate dialled by address
// does, and fails the test on a request without ddash's token. It returns
// the node's https URL.
func pve(t *testing.T) string {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "pve.home.example"},
		DNSNames:              []string{"pve.home.example"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
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
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	proxmoxapi.TrustRoots(t, roots)

	baseline := fixture.Scenario("testdata/baseline", func(request string, err error) { t.Errorf("fixture: %v", err) })
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "PVEAPIToken=ddash@pve!dashboard=4c1f9a3e-0000-4000-8000-000000000000"; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		baseline.ServeHTTP(w, r)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestDialsTheAddressAndVerifiesTheCertificateAsTheTLSServerName(t *testing.T) {
	url := pve(t)
	h, c := start(t, &pb.ProxmoxApiConfig{
		Url:           url,
		TlsServerName: "pve.home.example",
		TokenId:       "ddash@pve!dashboard",
		TokenSecret:   "4c1f9a3e-0000-4000-8000-000000000000",
	})

	if _, err := c.ListWorkloads(t.Context(), &pb.ListWorkloadsRequest{}); err != nil {
		t.Fatalf("ListWorkloads: %v", err)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health = %v, want ok", got)
	}
}

func TestWithoutATLSServerNameTheCertificateMustNameTheURLsHost(t *testing.T) {
	url := pve(t)
	h, _ := start(t, &pb.ProxmoxApiConfig{Url: url, TokenId: "ddash@pve!dashboard", TokenSecret: "4c1f9a3e-0000-4000-8000-000000000000"})

	if got := h.Health(); got.State != sdk.Down {
		t.Errorf("health = %v, want down: the certificate doesn't name 127.0.0.1", got)
	}
}
