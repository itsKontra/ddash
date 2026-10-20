package core_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/auth/oidctest"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// newKeycloak runs a host that signs in with kc, with the hello Plugin
// installed and its greeter configured.
func newKeycloak(t *testing.T, kc *oidctest.Issuer) *coretest.Harness {
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{greeter(sample.Hooks{})},
		Plugins:    []coretest.Plugin{hello},
		Keycloak:   kc,
		Shell:      builtShell,
	})
	h.SeedConfig("greeter", configured)
	h.Install("hello")
	return h
}

func TestAnOperatorSignedInWithKeycloakActsUnderTheirOwnName(t *testing.T) {
	kc := oidctest.New(t)
	h := newKeycloak(t, kc)
	h.WaitState("greeter", runtime.OK)

	h.SignIn()
	res := h.Call("hello", "greeter", "Act", `{}`)

	if res.Code != http.StatusOK {
		t.Fatalf("Act: status %d: %s", res.Code, res.Body)
	}
	var acted struct{ Actor string }
	json.Unmarshal(res.Body.Bytes(), &acted)
	if acted.Actor != "operator:alice" {
		t.Errorf("the Component ran Act for %q, want operator:alice", acted.Actor)
	}
	e := componentEntries(h)[0]
	if e.ActorKind != "operator" || e.Username != "alice" || e.Sub != oidctest.Operator.Sub {
		t.Errorf("entry = %+v, want operator alice with her sub", e)
	}
}

func TestTheCoreAPIServesTheCurrentOperator(t *testing.T) {
	kc := oidctest.New(t)
	h := newKeycloak(t, kc)
	h.SignIn()

	got := coreJSON(t, h, "GetOperator", `{}`)

	want := map[string]any{"operator": map[string]any{"name": "alice", "sub": oidctest.Operator.Sub}}
	if d := cmp.Diff(want, got); d != "" {
		t.Errorf("GetOperator (-want +got):\n%s", d)
	}
}

func TestWithoutASessionAnAPICallIs401AndAPageLoadIsSentToSignIn(t *testing.T) {
	h := newKeycloak(t, oidctest.New(t))
	h.WaitState("greeter", runtime.OK)

	wantError(t, h.Call("hello", "greeter", "Greet", `{"name":"Ada"}`), http.StatusUnauthorized, "unauthenticated", "")
	wantError(t, h.CoreCall("ListInstalledPlugins", `{}`), http.StatusUnauthorized, "unauthenticated", "")
	// Without a session even a call missing the CSRF header is a 401, so
	// the shell reloads into sign-in instead of showing a 400.
	bare := httptest.NewRequest(http.MethodPost, "/api/core/ddash.core.CoreService/ListInstalledPlugins", strings.NewReader(`{}`))
	bare.Header.Set("Content-Type", "application/json")
	wantError(t, h.Do(bare), http.StatusUnauthorized, "unauthenticated", "")
	for _, path := range []string{"/", "/docker?host=a", "/plugins/hello/dev/index.js", "/vendor/dev/react.js"} {
		res := h.Get(path)
		if want := "/auth/login?next=" + url.QueryEscape(path); res.Code != http.StatusFound || res.Header().Get("Location") != want {
			t.Errorf("GET %s: %d to %q, want 302 to %s", path, res.Code, res.Header().Get("Location"), want)
		}
	}
	for _, path := range []string{"/healthz", "/auth/logged-out"} {
		if res := h.Get(path); res.Code != http.StatusOK {
			t.Errorf("GET %s: %d, want 200 without a session", path, res.Code)
		}
	}
	if len(h.AuditLog()) != 1 { // the harness's Install
		t.Errorf("Audit Log = %+v, want nothing from unauthenticated requests", h.AuditLog())
	}
}

// signInEntries is the Audit Log's sign-in events, oldest first.
func signInEntries(h *coretest.Harness) []appstate.Entry {
	var out []appstate.Entry
	for _, e := range h.AuditLog() {
		if e.Target == "core" && (e.Method == auth.SignInEvent || e.Method == auth.SignOutEvent) {
			out = append([]appstate.Entry{e}, out...)
		}
	}
	return out
}

func TestASignInIsAuditedWithTheOperatorsUsernameAndSub(t *testing.T) {
	h := newKeycloak(t, oidctest.New(t))

	h.SignIn()

	entries := signInEntries(h)
	if len(entries) != 1 {
		t.Fatalf("sign-in entries = %+v, want 1", entries)
	}
	e := entries[0]
	if e.ActorKind != "operator" || e.Username != "alice" || e.Sub != oidctest.Operator.Sub || e.Method != "SignIn" || e.Outcome != appstate.OK || e.Chain == "" {
		t.Errorf("entry = %+v, want alice's ok SignIn with her sub", e)
	}
}

func TestTheSessionCookieIsHostOnlySecureHTTPOnlyAndStrictAndTheLoginCookieLax(t *testing.T) {
	kc := oidctest.New(t)
	h := newKeycloak(t, kc)

	login := h.Get("/auth/login")
	loginCookie := cookieNamed(login, auth.LoginCookie)
	callback := h.Get(kc.Authorize(login.Header().Get("Location")))
	session := cookieNamed(callback, auth.SessionCookie)

	if loginCookie == nil || session == nil {
		t.Fatalf("login cookie %v, session cookie %v; want both", loginCookie, session)
	}
	if !strings.HasPrefix(session.Name, "__Host-") || !session.Secure || !session.HttpOnly ||
		session.SameSite != http.SameSiteStrictMode || session.Path != "/" || session.Domain != "" {
		t.Errorf("session cookie %s, want __Host-, Secure, HttpOnly, SameSite=Strict, Path=/", session)
	}
	if !loginCookie.Secure || !loginCookie.HttpOnly || loginCookie.SameSite != http.SameSiteLaxMode ||
		loginCookie.MaxAge <= 0 || loginCookie.MaxAge > 15*60 {
		t.Errorf("login cookie %s, want Secure, HttpOnly, SameSite=Lax and short-lived", loginCookie)
	}
	if c := cookieNamed(callback, auth.LoginCookie); c == nil || c.MaxAge >= 0 {
		t.Errorf("the callback left the login cookie: %v", c)
	}
}

func cookieNamed(res *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range res.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestAnIDTokenWithAnotherNonceGetsNoSession(t *testing.T) {
	kc := oidctest.New(t)
	h := newKeycloak(t, kc)
	kc.Nonce = "replayed-nonce"

	res := h.TrySignIn()

	if res.Code != http.StatusBadRequest || h.Cookie(auth.SessionCookie) != nil {
		t.Errorf("callback: status %d, session %v; want 400 and no session", res.Code, h.Cookie(auth.SessionCookie))
	}
	wantError(t, h.CoreCall("ListInstalledPlugins", `{}`), http.StatusUnauthorized, "unauthenticated", "")
	if entries := signInEntries(h); len(entries) != 0 {
		t.Errorf("sign-in entries = %+v, want none", entries)
	}
}

// signedIn reports whether the browser's session still reaches the API.
func signedIn(t *testing.T, h *coretest.Harness) bool {
	t.Helper()
	switch res := h.CoreCall("ListInstalledPlugins", `{}`); res.Code {
	case http.StatusOK:
		return true
	case http.StatusUnauthorized:
		return false
	default:
		t.Fatalf("ListInstalledPlugins: status %d: %s", res.Code, res.Body)
		return false
	}
}

func TestASessionEndsAfterSevenDaysWithoutARequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newKeycloak(t, oidctest.New(t))
		h.SignIn()

		time.Sleep(auth.IdleTimeout - time.Minute)
		if !signedIn(t, h) {
			t.Fatal("signed out before 7 days idle")
		}
		time.Sleep(auth.IdleTimeout - time.Minute) // the request above was activity
		if !signedIn(t, h) {
			t.Fatal("signed out 7 days after sign-in, though active since")
		}
		time.Sleep(auth.IdleTimeout + time.Minute)
		if signedIn(t, h) {
			t.Error("still signed in after 7 days idle")
		}
	})
}

func TestEveryAuthenticatedRequestCountsAsActivity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newKeycloak(t, oidctest.New(t))
		h.SignIn()

		time.Sleep(auth.IdleTimeout - time.Minute)
		if res := h.Get("/"); res.Code != http.StatusOK { // a page refresh, not an API call
			t.Fatalf("GET /: status %d", res.Code)
		}
		time.Sleep(2 * time.Minute)
		if !signedIn(t, h) {
			t.Error("the page load didn't count as activity")
		}
	})
}

func TestASessionEndsThirtyDaysAfterSignInHoweverActive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newKeycloak(t, oidctest.New(t))
		h.SignIn()

		for range 4 { // 4 × 7 days, active each day
			for range 7 {
				time.Sleep(24 * time.Hour)
				if !signedIn(t, h) {
					t.Fatal("signed out before 30 days")
				}
			}
		}
		time.Sleep(2*24*time.Hour - time.Minute)
		if !signedIn(t, h) {
			t.Fatal("signed out before 30 days")
		}
		time.Sleep(2 * time.Minute)
		if signedIn(t, h) {
			t.Error("still signed in 30 days after sign-in")
		}
	})
}

func TestASessionKeepsWorkingWhileKeycloakIsDown(t *testing.T) {
	kc := oidctest.New(t)
	h := newKeycloak(t, kc)
	h.SignIn()

	kc.SetDown(true)

	if !signedIn(t, h) {
		t.Error("signed out while Keycloak is down")
	}
}

func TestDdashStartsWhileKeycloakIsDownAndSignInWaitsForIt(t *testing.T) {
	kc := oidctest.New(t)
	kc.SetDown(true)
	h := newKeycloak(t, kc)

	if res := h.Get("/healthz"); res.Code != http.StatusOK {
		t.Errorf("healthz: status %d, want 200", res.Code)
	}
	if res := h.Get("/auth/login"); res.Code != http.StatusServiceUnavailable {
		t.Errorf("/auth/login with Keycloak down: status %d, want 503", res.Code)
	}
	kc.SetDown(false)
	h.SignIn()
}

func TestSignInReturnsToThePageThatAskedForIt(t *testing.T) {
	kc := oidctest.New(t)
	h := newKeycloak(t, kc)

	for path, want := range map[string]string{
		"/docker?host=a":        "/docker?host=a",
		"//evil.example/phish":  "/",
		"https://evil.example/": "/",
	} {
		h.ClearCookies()
		login := h.Get("/auth/login?next=" + url.QueryEscape(path))
		res := h.Get(kc.Authorize(login.Header().Get("Location")))
		if !strings.Contains(res.Body.String(), `url=`+want+`"`) {
			t.Errorf("next=%s: the callback page is %s, want it to continue to %s", path, res.Body, want)
		}
	}
}

func TestAStaleOrForeignCallbackGetsNoSession(t *testing.T) {
	kc := oidctest.New(t)
	h := newKeycloak(t, kc)
	login := h.Get("/auth/login")
	back := kc.Authorize(login.Header().Get("Location"))

	h.ClearCookies() // the login cookie expired, or this isn't the browser that started
	if res := h.Get(back); res.Code != http.StatusBadRequest || h.Cookie(auth.SessionCookie) != nil {
		t.Errorf("callback without the login cookie: %d, want 400 and no session", res.Code)
	}
	h.Get("/auth/login") // a newer sign-in in this browser
	if res := h.Get(back); res.Code != http.StatusBadRequest || h.Cookie(auth.SessionCookie) != nil {
		t.Errorf("callback for another sign-in's state: %d, want 400 and no session", res.Code)
	}
}

func TestLogoutEndsTheSessionThenLogsOutOfKeycloak(t *testing.T) {
	kc := oidctest.New(t)
	h := newKeycloak(t, kc)
	h.SignIn()
	stolen := h.Cookie(auth.SessionCookie)

	res := h.Get("/auth/logout")

	if res.Code != http.StatusSeeOther {
		t.Fatalf("logout: status %d, want 303 to Keycloak", res.Code)
	}
	q := kc.EndSession(res.Header().Get("Location"))
	if q.Get("client_id") != oidctest.ClientID || q.Get("post_logout_redirect_uri") != coretest.PublicURL+"/auth/logged-out" {
		t.Errorf("end-session query %v, want ddash's client and the logged-out page", q)
	}
	if signedIn(t, h) {
		t.Error("still signed in after logout")
	}
	r := coretestRequest("ListInstalledPlugins")
	r.AddCookie(stolen)
	if res := h.Do(r); res.Code != http.StatusUnauthorized {
		t.Errorf("the old session cookie: status %d, want 401", res.Code)
	}
	entries := signInEntries(h)
	if len(entries) != 2 || entries[1].Method != "SignOut" || entries[1].Username != "alice" || entries[1].Sub != oidctest.Operator.Sub {
		t.Errorf("sign-in entries = %+v, want alice's SignIn then SignOut", entries)
	}
	if res := h.Get("/auth/logged-out"); res.Code != http.StatusOK {
		t.Errorf("logged-out page: status %d, want 200", res.Code)
	}
}

func coretestRequest(method string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/core/ddash.core.CoreService/"+method, strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Connect-Protocol-Version", "1")
	return r
}

func TestAnAccountWithoutTheOperatorRoleIsNotAuthorisedAndAudited(t *testing.T) {
	kc := oidctest.New(t)
	h := newKeycloak(t, kc)
	kc.User = oidctest.User{Username: "mallory", Sub: "9d1e-mallory", Roles: []string{"viewer"}}

	res := h.TrySignIn()

	if res.Code != http.StatusForbidden || !strings.Contains(res.Body.String(), "Not authorised") {
		t.Errorf("callback: status %d, want 403 and a not authorised page: %s", res.Code, res.Body)
	}
	if h.Cookie(auth.SessionCookie) != nil {
		t.Error("mallory got a session")
	}
	wantError(t, h.CoreCall("ListInstalledPlugins", `{}`), http.StatusUnauthorized, "unauthenticated", "")
	entries := signInEntries(h)
	if len(entries) != 1 {
		t.Fatalf("sign-in entries = %+v, want 1", entries)
	}
	e := entries[0]
	if e.Username != "mallory" || e.Sub != "9d1e-mallory" || e.Method != "SignIn" || e.Outcome != "PermissionDenied" {
		t.Errorf("entry = %+v, want mallory's refused SignIn with her sub", e)
	}
}
