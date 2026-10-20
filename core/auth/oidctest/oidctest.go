// Package oidctest is Keycloak as a test OIDC issuer: discovery, JWKS, the
// authorization, token and end-session endpoints of one realm, served in
// memory so it works inside a testing/synctest bubble.
//
//	kc := oidctest.New(t)
//	kc.User = oidctest.User{Username: "alice", Sub: "f3c1", Roles: []string{"operator"}}
//	h := coretest.New(t, coretest.Options{Keycloak: kc})
//	h.SignIn()
//
// It checks what Keycloak checks for ddash's client: the client secret,
// the exact redirect URI and the PKCE S256 challenge.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"

	"github.com/itsKontra/ddash/core/auth"
)

// The realm and client the issuer serves.
const (
	IssuerURL    = "https://keycloak.home.example/realms/home"
	ClientID     = "ddash"
	ClientSecret = "test-client-secret"
)

// User is who signs in at the issuer's login page.
type User struct {
	Username string
	Sub      string
	// Roles are the user's roles on the ddash client.
	Roles []string
}

// Operator is a user holding the operator role.
var Operator = User{Username: "alice", Sub: "3f0c5a52-alice", Roles: []string{"operator"}}

// Issuer is a fake Keycloak realm.
type Issuer struct {
	// User signs in at the next authorization. Default Operator.
	User User
	// Nonce, when set, replaces the nonce of the ID tokens issued.
	Nonce string

	t       testing.TB
	key     *rsa.PrivateKey
	handler http.Handler

	mu    sync.Mutex
	codes map[string]grant
	down  bool
}

type grant struct {
	user          User
	nonce         string
	challenge     string
	redirectURI   string
	authenticated time.Time
}

// New starts an issuer for the test.
func New(t testing.TB) *Issuer {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	kc := &Issuer{User: Operator, t: t, key: key, codes: map[string]grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /realms/home/.well-known/openid-configuration", kc.discovery)
	mux.HandleFunc("GET /realms/home/protocol/openid-connect/certs", kc.certs)
	mux.HandleFunc("POST /realms/home/protocol/openid-connect/token", kc.token)
	kc.handler = mux
	return kc
}

// Config is ddash's sign-in config for this realm, reaching it in memory.
func (kc *Issuer) Config() auth.OIDC {
	return auth.OIDC{
		Issuer:       IssuerURL,
		ClientID:     ClientID,
		ClientSecret: ClientSecret,
		HTTPClient:   &http.Client{Transport: kc},
	}
}

// SetDown makes every back-channel request fail, as if Keycloak were down.
func (kc *Issuer) SetDown(down bool) {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	kc.down = down
}

// RoundTrip serves ddash's back-channel requests in memory.
func (kc *Issuer) RoundTrip(r *http.Request) (*http.Response, error) {
	kc.mu.Lock()
	down := kc.down
	kc.mu.Unlock()
	if down {
		return nil, fmt.Errorf("dial %s: connection refused", r.URL.Host)
	}
	if r.URL.Scheme+"://"+r.URL.Host != "https://keycloak.home.example" {
		return nil, fmt.Errorf("oidctest: request to %s, not the issuer", r.URL)
	}
	w := httptest.NewRecorder()
	kc.handler.ServeHTTP(w, r)
	return w.Result(), nil
}

// Authorize is the browser's trip through Keycloak's login page: given the
// authorization URL ddash redirected to, it signs User in and returns the
// URL Keycloak redirects back to.
func (kc *Issuer) Authorize(authURL string) string {
	kc.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		kc.t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != IssuerURL+"/protocol/openid-connect/auth" {
		kc.t.Fatalf("authorize at %s, not the issuer's authorization endpoint", got)
	}
	q := u.Query()
	for k, want := range map[string]string{"response_type": "code", "client_id": ClientID, "code_challenge_method": "S256"} {
		if q.Get(k) != want {
			kc.t.Fatalf("authorize: %s=%q, want %q", k, q.Get(k), want)
		}
	}
	if !slices.Contains(strings.Fields(q.Get("scope")), "openid") {
		kc.t.Fatalf("authorize: scope %q lacks openid", q.Get("scope"))
	}
	for _, k := range []string{"state", "nonce", "code_challenge", "redirect_uri"} {
		if q.Get(k) == "" {
			kc.t.Fatalf("authorize: no %s", k)
		}
	}
	code := rand.Text()
	kc.mu.Lock()
	kc.codes[code] = grant{
		user: kc.User, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"),
		redirectURI: q.Get("redirect_uri"), authenticated: time.Now(),
	}
	kc.mu.Unlock()
	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		kc.t.Fatal(err)
	}
	bq := back.Query()
	bq.Set("code", code)
	bq.Set("state", q.Get("state"))
	bq.Set("iss", IssuerURL)
	back.RawQuery = bq.Encode()
	return back.String()
}

// EndSession is the browser's arrival at Keycloak's end-session endpoint:
// given the URL ddash redirected to, it returns the logout request's query.
func (kc *Issuer) EndSession(logoutURL string) url.Values {
	kc.t.Helper()
	u, err := url.Parse(logoutURL)
	if err != nil {
		kc.t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != IssuerURL+"/protocol/openid-connect/logout" {
		kc.t.Fatalf("logout at %s, not the issuer's end-session endpoint", got)
	}
	return u.Query()
}

func (kc *Issuer) discovery(w http.ResponseWriter, _ *http.Request) {
	base := IssuerURL + "/protocol/openid-connect"
	writeJSON(w, map[string]any{
		"issuer":                                IssuerURL,
		"authorization_endpoint":                base + "/auth",
		"token_endpoint":                        base + "/token",
		"jwks_uri":                              base + "/certs",
		"end_session_endpoint":                  base + "/logout",
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"plain", "S256"},
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
	})
}

func (kc *Issuer) certs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: &kc.key.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig",
	}}})
}

func (kc *Issuer) token(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	}
	if id != ClientID || secret != ClientSecret {
		oauthError(w, http.StatusUnauthorized, "unauthorized_client")
		return
	}
	if r.PostFormValue("grant_type") != "authorization_code" {
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}
	kc.mu.Lock()
	g, ok := kc.codes[r.PostFormValue("code")]
	delete(kc.codes, r.PostFormValue("code"))
	nonce := kc.Nonce
	kc.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
	switch {
	case !ok:
		oauthError(w, http.StatusBadRequest, "invalid_grant")
		return
	case g.redirectURI != r.PostFormValue("redirect_uri"):
		oauthError(w, http.StatusBadRequest, "invalid_grant")
		return
	case base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge:
		oauthError(w, http.StatusBadRequest, "invalid_grant")
		return
	}
	if nonce == "" {
		nonce = g.nonce
	}
	now := time.Now()
	claims := map[string]any{
		"iss": IssuerURL, "aud": ClientID, "azp": ClientID, "sub": g.user.Sub,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "auth_time": g.authenticated.Unix(),
		"nonce": nonce, "preferred_username": g.user.Username, "typ": "ID",
	}
	if len(g.user.Roles) > 0 {
		claims["resource_access"] = map[string]any{ClientID: map[string]any{"roles": g.user.Roles}}
	}
	writeJSON(w, map[string]any{
		"access_token": rand.Text(), "token_type": "Bearer", "expires_in": 300,
		"refresh_token": rand.Text(), "id_token": kc.sign(claims), "scope": "openid profile",
	})
}

func (kc *Issuer) sign(claims map[string]any) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: kc.key, KeyID: "test"}},
		(&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		kc.t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		kc.t.Fatal(err)
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		kc.t.Fatal(err)
	}
	s, err := jws.CompactSerialize()
	if err != nil {
		kc.t.Fatal(err)
	}
	return s
}

func oauthError(w http.ResponseWriter, code int, kind string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": kind})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
