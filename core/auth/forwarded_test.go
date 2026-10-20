package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/itsKontra/ddash/core/auth"
)

// seen is what a handler behind the forwarded-header middleware sees.
type seen struct{ remoteAddr, forwardedFor, forwardedProto string }

func throughProxy(t *testing.T, trusted, peer, xff, proto string) seen {
	t.Helper()
	mw, err := auth.Forwarded(trusted)
	if err != nil {
		t.Fatal(err)
	}
	var got seen
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = seen{r.RemoteAddr, r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Forwarded-Proto")}
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = peer
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	if proto != "" {
		r.Header.Set("X-Forwarded-Proto", proto)
	}
	h.ServeHTTP(httptest.NewRecorder(), r)
	return got
}

func TestForwardedHeadersAreTrustedOnlyFromTheTrustedProxy(t *testing.T) {
	for _, tc := range []struct {
		name, trusted, peer, xff, proto string
		want                            seen
	}{
		{"from the proxy", "198.18.0.10", "198.18.0.10:41000", "203.0.113.7", "https",
			seen{"203.0.113.7:0", "203.0.113.7", "https"}},
		{"from the proxy, a client-sent chain", "198.18.0.10", "198.18.0.10:41000", "192.0.2.66, 203.0.113.7", "https",
			seen{"203.0.113.7:0", "192.0.2.66, 203.0.113.7", "https"}},
		{"from the proxy's subnet", "198.18.0.0/24", "198.18.0.10:41000", "203.0.113.7", "https",
			seen{"203.0.113.7:0", "203.0.113.7", "https"}},
		{"from anyone else", "198.18.0.10", "203.0.113.9:5000", "203.0.113.7", "https",
			seen{"203.0.113.9:5000", "", ""}},
		{"with no trusted proxy", "", "198.18.0.10:41000", "203.0.113.7", "https",
			seen{"198.18.0.10:41000", "", ""}},
		{"from the proxy, without headers", "198.18.0.10", "198.18.0.10:41000", "", "",
			seen{"198.18.0.10:41000", "", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := throughProxy(t, tc.trusted, tc.peer, tc.xff, tc.proto); got != tc.want {
				t.Errorf("handler saw %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestATrustedProxyMustBeAnAddressOrCIDR(t *testing.T) {
	if _, err := auth.Forwarded("nginx.home.example"); err == nil {
		t.Error("a hostname was accepted")
	}
}
