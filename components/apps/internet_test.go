package apps_test

import (
	"net/http"
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/cloudflare-api/cloudflarefake"
	"github.com/itsKontra/ddash/core/runtime"
)

// newCloudflare is Cloudflare's baseline: the Tunnel serves
// request.home.example, video.home.example and ha.home.example.
func newCloudflare(t *testing.T) *cloudflarefake.Cloudflare {
	t.Helper()
	f, err := cloudflarefake.New("../cloudflare-api/testdata/baseline", cloudflarefake.Options{
		Miss: func(request string, err error) { t.Errorf("Cloudflare fixture: %v", err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// publicHostnames are the Apps' Public Hostnames by URL, for those that
// have any.
func publicHostnames(resp *appspb.ListAppsResponse) map[string][]*appspb.PublicHostname {
	out := map[string][]*appspb.PublicHostname{}
	for _, a := range resp.GetApps() {
		if len(a.GetPublicHostnames()) > 0 {
			out[a.GetUrl()] = a.GetPublicHostnames()
		}
	}
	return out
}

// An App whose hostname is a Public Hostname of the Cloudflare Tunnel is
// on the internet: Nginx Proxy Manager's baseline has request, video and
// ha as Proxy Hosts, which Cloudflare's baseline serves.
func TestAnAppWhoseHostnameIsAPublicHostnameIsOnTheInternet(t *testing.T) {
	l := newLab(t, labOptions{cloudflare: newCloudflare(t)})

	resp := l.list()

	if resp.GetCloudflareError() != "" {
		t.Errorf("Cloudflare error %q, want none", resp.GetCloudflareError())
	}
	want := map[string][]*appspb.PublicHostname{
		"https://request.home.example": {{Hostname: "request.home.example", Service: "http://198.18.1.212:5055"}},
		"https://video.home.example":   {{Hostname: "video.home.example", Service: "http://198.18.1.212:8096"}},
		"https://ha.home.example":      {{Hostname: "ha.home.example", Service: "http://198.18.1.101:8123"}},
	}
	if d := diff(want, publicHostnames(resp)); d != "" {
		t.Errorf("Public Hostnames by URL (-want +got):\n%s", d)
	}
}

// Without cloudflare-api set up, no App is on the internet through it,
// and nothing is wrong.
func TestNoAppIsOnTheInternetWhileCloudflareIsNotSetUp(t *testing.T) {
	l := newLab(t, labOptions{})

	resp := l.list()

	if resp.GetCloudflareError() != "" || len(publicHostnames(resp)) != 0 {
		t.Errorf("Cloudflare error %q, Public Hostnames %v; want neither", resp.GetCloudflareError(), publicHostnames(resp))
	}
	if st := l.h.Status("apps").State; st != runtime.OK {
		t.Errorf("apps is %v, want ok", st)
	}
}

// While Cloudflare can't be read, the Apps are listed without their
// Public Hostnames, with cloudflare-api's error, and apps stays healthy.
func TestAppsGoWithoutPublicHostnamesWhileCloudflareCantBeRead(t *testing.T) {
	failing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1000,"message":"Bad gateway"}],"messages":[],"result":null}`))
	})
	l := newLab(t, labOptions{cloudflare: failing})

	resp := l.list()

	if resp.GetCloudflareError() != "Cloudflare answered 502: Bad gateway (1000)" {
		t.Errorf("Cloudflare error = %q", resp.GetCloudflareError())
	}
	if len(resp.GetApps()) == 0 || len(publicHostnames(resp)) != 0 {
		t.Errorf("%d Apps with Public Hostnames %v; want the Apps, without", len(resp.GetApps()), publicHostnames(resp))
	}
	if st := l.h.Status("apps").State; st != runtime.OK {
		t.Errorf("apps is %v, want ok", st)
	}
}
