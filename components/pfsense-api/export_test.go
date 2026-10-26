package pfsenseapi

import (
	"net/http"
	"net/http/httptest"

	"github.com/itsKontra/ddash/sdk"
)

// DefinitionServing is pfsense-api whose requests h answers in memory, so
// its tests can run inside a synctest bubble. A request whose context ends
// first fails, as on the network.
func DefinitionServing(h http.Handler) sdk.Definition { return define(handlerTransport{h}) }

type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		t.h.ServeHTTP(w, r)
	}()
	select {
	case <-done:
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	res := w.Result()
	res.Request = r
	return res, nil
}
