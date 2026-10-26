package nginxproxymanagerapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	"github.com/itsKontra/ddash/components/nginx-proxy-manager-api/npmfake"
	"github.com/itsKontra/ddash/sdk"
)

const signIn = http.MethodPost + " /api/tokens"

// reads are the requests for Proxy Hosts and certificates in log.
func reads(log []string) []string {
	var out []string
	for _, e := range log {
		if strings.Contains(e, " /api/nginx/") {
			out = append(out, e)
		}
	}
	return out
}

func TestSignsInAgainBeforeTheTokenExpires(t *testing.T) {
	f := npmWith(t, "baseline", npmfake.Options{TokenLifetime: 2 * time.Second})
	_, c := start(t, f.URL)
	if _, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{}); err != nil {
		t.Fatal(err)
	}

	// Past half the token's life, and before its end.
	time.Sleep(1200 * time.Millisecond)
	_, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{Fresh: true})

	if err != nil {
		t.Fatal(err)
	}
	if n := count(f.Log(), signIn); n != 2 {
		t.Errorf("signed in %d times, want again before the token expired: %v", n, f.Log())
	}
}

func TestATokenNginxProxyManagerCallsExpiredIsReplacedAndTheReadSentAgain(t *testing.T) {
	f := npm(t, "baseline")
	_, c := start(t, f.URL)
	if _, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{}); err != nil {
		t.Fatal(err)
	}
	f.ExpireTokens()

	_, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{Fresh: true})

	if err != nil {
		t.Fatalf("ListProxyHosts after the token expired: %v", err)
	}
	if n := count(f.Log(), signIn); n != 2 {
		t.Errorf("signed in %d times, want once more after the expired token", n)
	}
}

func TestARefusedSignInIsDownAndRetriedAtTheNextRead(t *testing.T) {
	f := npm(t, "baseline")
	f.EditUser(func(u *npmfake.User) { u.Password = "an old password" })
	h, c := start(t, f.URL)

	_, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{})

	want := "Nginx Proxy Manager refused ddash's sign-in as ddash@home.example (Invalid email or password): fix the email or password in nginx-proxy-manager-api's config"
	if status.Code(err) != codes.Unavailable || status.Convert(err).Message() != want {
		t.Errorf("ListProxyHosts error = %v, want Unavailable: %s", err, want)
	}
	if got := h.Health(); got.State != sdk.Down || got.Message != want {
		t.Errorf("Health = %v, want down: %s", got, want)
	}
	if r := reads(f.Log()); len(r) != 0 {
		t.Errorf("read %v without a sign-in", r)
	}

	// Nginx Proxy Manager has no lockout: the next read signs in again.
	f.EditUser(func(u *npmfake.User) { u.Password = password })
	if _, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{}); err != nil {
		t.Fatalf("ListProxyHosts once the password matches: %v", err)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("Health = %v, want ok once signed in", got)
	}
}

func TestEachSignInProblemIsDownNamingItsFixAndNothingIsRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*npmfake.NPM)
		want string
	}{
		{"2FA", func(f *npmfake.NPM) { f.EditUser(func(u *npmfake.User) { u.TwoFactor = true }) },
			"Nginx Proxy Manager's user ddash@home.example asks for 2FA: turn it off for this user"},
		{"own items only", func(f *npmfake.NPM) { f.EditUser(func(u *npmfake.User) { u.Visibility = "user" }) },
			"ddash's user sees only the Proxy Hosts it created: set Item Visibility to All Items"},
		{"certificates hidden", func(f *npmfake.NPM) { f.EditUser(func(u *npmfake.User) { u.Certificates = "hidden" }) },
			"ddash's user can't see SSL Certificates: set them to View"},
		{"Proxy Hosts hidden", func(f *npmfake.NPM) { f.EditUser(func(u *npmfake.User) { u.ProxyHosts = "hidden" }) },
			"ddash's user can't see Proxy Hosts: set them to Manage"},
		{"below 2.10", func(f *npmfake.NPM) { f.SetVersion(2, 9, 22) },
			"nginx-proxy-manager-api needs Nginx Proxy Manager 2.10 or later; it runs 2.9.22"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := npm(t, "baseline")
			tc.edit(f.NPM)
			h, c := start(t, f.URL)

			_, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{})

			if status.Code(err) != codes.Unavailable || status.Convert(err).Message() != tc.want {
				t.Errorf("ListProxyHosts error = %v, want Unavailable: %s", err, tc.want)
			}
			if _, err := c.ListCertificates(t.Context(), &pb.ListCertificatesRequest{}); status.Convert(err).Message() != tc.want {
				t.Errorf("ListCertificates error = %v, want %s", err, tc.want)
			}
			if got := h.Health(); got.State != sdk.Down || got.Message != tc.want {
				t.Errorf("Health = %v, want down: %s", got, tc.want)
			}
			st, err := c.GetStatus(t.Context(), &pb.GetStatusRequest{})
			if err != nil || st.GetStatus() != pb.Status_STATUS_DOWN || st.GetError() != tc.want {
				t.Errorf("GetStatus = %v (%v), want Down: %s", st, err, tc.want)
			}
			if r := reads(f.Log()); len(r) != 0 {
				t.Errorf("read %v despite the sign-in problem", r)
			}
		})
	}
}

func TestAnAdminAndCertificatesOnViewOrManageAreAcceptedSilently(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*npmfake.User)
	}{
		{"certificates on view", func(u *npmfake.User) { u.Certificates = "view" }},
		{"certificates on manage", func(u *npmfake.User) { u.Certificates = "manage" }},
		{"admin", func(u *npmfake.User) {
			u.Admin, u.Visibility, u.ProxyHosts, u.Certificates = true, "user", "hidden", "hidden"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := npm(t, "baseline")
			f.EditUser(tc.edit)
			h, c := start(t, f.URL)

			st, err := c.GetStatus(t.Context(), &pb.GetStatusRequest{})

			if err != nil || st.GetStatus() != pb.Status_STATUS_UP || st.GetError() != "" || st.GetProxyHostsReadOnly() {
				t.Errorf("GetStatus = %v (%v), want Up and writable", st, err)
			}
			if got := h.Health(); got.State != sdk.OK || got.Message != "" {
				t.Errorf("Health = %v, want ok with nothing to say", got)
			}
			if _, err := c.ListCertificates(t.Context(), &pb.ListCertificatesRequest{}); err != nil {
				t.Errorf("ListCertificates: %v", err)
			}
		})
	}
}
