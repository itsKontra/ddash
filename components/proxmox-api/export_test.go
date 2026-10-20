package proxmoxapi

import (
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/itsKontra/ddash/sdk"
)

// TrustRoots makes the Components started during t trust pool instead of
// the system's roots.
func TrustRoots(t *testing.T, pool *x509.CertPool) {
	old := rootCAs
	rootCAs = pool
	t.Cleanup(func() { rootCAs = old })
}

// SettleAtMost is how long a power Action waits for /cluster/resources to
// list its change.
const SettleAtMost = settleAtMost

// DefinitionServing is proxmox-api whose requests h answers in memory, so
// its tests can run inside a synctest bubble.
func DefinitionServing(h http.Handler) sdk.Definition { return define(handlerTransport{h}) }

type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	w := httptest.NewRecorder()
	t.h.ServeHTTP(w, r)
	res := w.Result()
	res.Request = r
	return res, nil
}
