package hostagentapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// ServeDownloads makes ddash's downloads (SHA256SUMS) come from h during t.
func ServeDownloads(t *testing.T, h http.Handler) {
	old := downloads
	downloads = &http.Client{Transport: handlerTransport{h}}
	t.Cleanup(func() { downloads = old })
}

type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	t.h.ServeHTTP(w, r)
	res := w.Result()
	res.Request = r
	return res, nil
}
