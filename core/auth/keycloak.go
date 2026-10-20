package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// OIDC is how ddash reaches its Keycloak realm, as a confidential client.
type OIDC struct {
	// Issuer is the realm's public URL, e.g.
	// https://auth.home.example/realms/home.
	Issuer       string
	ClientID     string
	ClientSecret string
	// HTTPClient makes the back-channel requests to Keycloak. Nil uses
	// http.DefaultClient.
	HTTPClient *http.Client
}

// OperatorRole is the role on ddash's Keycloak client that the ID token
// must carry for a sign-in to succeed.
const OperatorRole = "operator"

// The audited sign-in events, as the Audit Log's method.
const (
	SignInEvent  = "SignIn"
	SignOutEvent = "SignOut"
)

// Audit records a sign-in event for actor in the Audit Log: SignInEvent or
// SignOutEvent, with err nil on success. A sign-in fails when it can't.
type Audit func(ctx context.Context, actor Actor, event string, err error) error

// Paths of sign-in, all public.
const (
	LoginPath     = "/auth/login"
	CallbackPath  = "/auth/callback"
	LogoutPath    = "/auth/logout"
	LoggedOutPath = "/auth/logged-out"
)

// Cookie names. The __Host- prefix makes the browser insist on Secure,
// Path=/ and no Domain.
const (
	SessionCookie = "__Host-ddash-session"
	LoginCookie   = "__Host-ddash-login"
)

// loginTTL bounds the trip through Keycloak's login page.
const loginTTL = 10 * 60 // seconds

// KeycloakOptions configure sign-in with Keycloak.
type KeycloakOptions struct {
	OIDC OIDC
	// PublicURL is where the Operator reaches ddash; redirect URIs are
	// built from it, never from the request.
	PublicURL string
	// DB is the core DB, holding the session table.
	DB    *sql.DB
	Audit Audit
	// Logger receives sign-in failures. Nil discards them.
	Logger *slog.Logger
}

// Keycloak signs the Operator in with Keycloak as an OIDC backend for the
// frontend: auth code + PKCE and a nonce, the operator role required, and
// a server-side session behind a cookie. Keycloak's tokens are discarded
// after the callback, so a session outlives Keycloak being down.
type Keycloak struct {
	opts     KeycloakOptions
	sessions sessions
	logger   *slog.Logger

	mu       sync.Mutex
	provider *oidc.Provider // discovered on first use, so ddash starts without Keycloak
}

// NewKeycloak returns sign-in with the realm opts.OIDC. It doesn't contact
// Keycloak: discovery waits for the first sign-in.
func NewKeycloak(opts KeycloakOptions) (*Keycloak, error) {
	switch {
	case opts.OIDC.Issuer == "", opts.OIDC.ClientID == "", opts.OIDC.ClientSecret == "":
		return nil, errors.New("sign-in needs the OIDC issuer, client ID and client secret")
	case !strings.HasPrefix(opts.PublicURL, "https://"):
		return nil, fmt.Errorf("sign-in needs an https public URL, not %q", opts.PublicURL)
	}
	opts.PublicURL = strings.TrimSuffix(opts.PublicURL, "/")
	if opts.OIDC.HTTPClient == nil {
		opts.OIDC.HTTPClient = http.DefaultClient
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Keycloak{opts: opts, sessions: sessions{opts.DB}, logger: logger}, nil
}

// Handler serves the public sign-in paths under /auth/.
func (k *Keycloak) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+LoginPath, k.login)
	mux.HandleFunc("GET "+CallbackPath, k.callback)
	mux.HandleFunc("GET "+LogoutPath, k.logout)
	mux.HandleFunc("POST "+LogoutPath, k.logout)
	mux.HandleFunc("GET "+LoggedOutPath, loggedOut)
	return mux
}

// Guard lets a request through only with a live session, as its Operator.
// Each request it lets through counts as activity. Without a session, a
// page load is sent to sign in and anything else gets 401.
func (k *Keycloak) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(SessionCookie); err == nil {
			op, ok, err := k.sessions.touch(r.Context(), c.Value)
			if err != nil {
				k.logger.Error("session lookup failed", "err", err)
				writeJSONError(w, http.StatusServiceUnavailable, "unavailable", "sessions unavailable")
				return
			}
			if ok {
				next.ServeHTTP(w, r.WithContext(WithActor(r.Context(), op)))
				return
			}
		}
		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !strings.HasPrefix(r.URL.Path, "/api/") {
			http.Redirect(w, r, LoginPath+"?"+url.Values{"next": {r.URL.RequestURI()}}.Encode(), http.StatusFound)
			return
		}
		Refuse(next).ServeHTTP(w, r)
	})
}

// SweepSessions deletes expired sessions at once and then every
// SweepInterval, until ctx ends. It is one of the core's housekeeping loops.
func (k *Keycloak) SweepSessions(ctx context.Context) { k.sessions.sweep(ctx) }

// loginState is what the login cookie carries through Keycloak.
type loginState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"x,omitempty"`
}

func (k *Keycloak) login(w http.ResponseWriter, r *http.Request) {
	p, err := k.discover(r.Context())
	if err != nil {
		k.logger.Warn("Keycloak unreachable", "err", err)
		page(w, http.StatusServiceUnavailable, "Keycloak is unreachable", "ddash can't reach Keycloak to sign you in. Try again in a moment.")
		return
	}
	st := loginState{State: rand.Text(), Nonce: rand.Text(), Verifier: oauth2.GenerateVerifier(), Next: localPath(r.URL.Query().Get("next"))}
	b, _ := json.Marshal(st)
	http.SetCookie(w, &http.Cookie{
		Name: LoginCookie, Value: base64.RawURLEncoding.EncodeToString(b), Path: "/",
		MaxAge: loginTTL, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, k.oauth2(p).AuthCodeURL(st.State, oauth2.S256ChallengeOption(st.Verifier), oidc.Nonce(st.Nonce)), http.StatusFound)
}

func (k *Keycloak) callback(w http.ResponseWriter, r *http.Request) {
	st, ok := readLoginState(r)
	clearCookie(w, LoginCookie, http.SameSiteLaxMode)
	q := r.URL.Query()
	switch {
	case !ok:
		page(w, http.StatusBadRequest, "Sign-in expired", "This sign-in took too long or was started elsewhere. Sign in again.")
		return
	case subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(st.State)) != 1:
		page(w, http.StatusBadRequest, "Sign-in failed", "The sign-in response doesn't match this browser's sign-in. Sign in again.")
		return
	case q.Get("error") != "":
		k.logger.Warn("Keycloak refused the sign-in", "error", q.Get("error"), "description", q.Get("error_description"))
		page(w, http.StatusBadRequest, "Sign-in failed", "Keycloak didn't sign you in. Sign in again.")
		return
	}
	op, err := k.verify(r.Context(), q.Get("code"), st)
	if err != nil {
		k.logger.Warn("sign-in failed", "err", err, "client", r.RemoteAddr)
		page(w, http.StatusBadRequest, "Sign-in failed", "ddash couldn't verify your sign-in with Keycloak. Sign in again.")
		return
	}
	if !op.operator {
		refused := status.Error(codes.PermissionDenied, "no "+OperatorRole+" role on the "+k.opts.OIDC.ClientID+" client")
		if err := k.opts.Audit(r.Context(), op.Actor, SignInEvent, refused); err != nil {
			k.logger.Error("audit log unavailable", "err", err)
		}
		k.logger.Warn("sign-in refused: no operator role", "username", op.Name, "sub", op.Sub, "client", r.RemoteAddr)
		page(w, http.StatusForbidden, "Not authorised", "Your account isn't allowed to use ddash.")
		return
	}
	if err := k.opts.Audit(r.Context(), op.Actor, SignInEvent, nil); err != nil {
		k.logger.Error("sign-in refused: audit log unavailable", "err", err)
		page(w, http.StatusServiceUnavailable, "Sign-in failed", "ddash can't record your sign-in in its Audit Log, so it won't sign you in.")
		return
	}
	token, err := k.sessions.create(r.Context(), op.Actor)
	if err != nil {
		k.logger.Error("can't store the session", "err", err)
		page(w, http.StatusServiceUnavailable, "Sign-in failed", "ddash couldn't store your session. Sign in again.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", MaxAge: int(MaxSessionAge.Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	// Keycloak's redirect here is a cross-site navigation, and a browser
	// doesn't send a Strict cookie on a redirect that follows it. A page
	// that moves on by itself starts a same-site navigation that does.
	next := st.Next
	if next == "" {
		next = "/"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = continueTmpl.Execute(w, next)
}

// signedIn is who an ID token says signed in.
type signedIn struct {
	Actor
	operator bool
}

// verify exchanges code for tokens, verifies the ID token and its nonce,
// and reads who signed in. The tokens go no further.
func (k *Keycloak) verify(ctx context.Context, code string, st loginState) (signedIn, error) {
	p, err := k.discover(ctx)
	if err != nil {
		return signedIn{}, err
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, k.opts.OIDC.HTTPClient)
	tok, err := k.oauth2(p).Exchange(ctx, code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return signedIn{}, fmt.Errorf("code exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return signedIn{}, errors.New("no ID token in the token response")
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: k.opts.OIDC.ClientID}).Verify(oidc.ClientContext(ctx, k.opts.OIDC.HTTPClient), raw)
	if err != nil {
		return signedIn{}, err
	}
	// go-oidc leaves the nonce to the caller.
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(st.Nonce)) != 1 {
		return signedIn{}, errors.New("ID token nonce doesn't match this sign-in")
	}
	var claims struct {
		PreferredUsername string `json:"preferred_username"`
		ResourceAccess    map[string]struct {
			Roles []string `json:"roles"`
		} `json:"resource_access"`
	}
	if err := idt.Claims(&claims); err != nil {
		return signedIn{}, err
	}
	name := claims.PreferredUsername
	if name == "" {
		name = idt.Subject
	}
	return signedIn{
		Actor:    Actor{Kind: Operator, Name: name, Sub: idt.Subject},
		operator: slices.Contains(claims.ResourceAccess[k.opts.OIDC.ClientID].Roles, OperatorRole),
	}, nil
}

func (k *Keycloak) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		op, ok, err := k.sessions.end(r.Context(), c.Value)
		if err != nil {
			k.logger.Error("can't end the session", "err", err)
			page(w, http.StatusServiceUnavailable, "Sign-out failed", "ddash couldn't end your session. Try again.")
			return
		}
		if ok {
			if err := k.opts.Audit(r.Context(), op, SignOutEvent, nil); err != nil {
				k.logger.Error("audit log unavailable", "err", err)
			}
		}
	}
	clearCookie(w, SessionCookie, http.SameSiteStrictMode)
	loggedOutURL := k.opts.PublicURL + LoggedOutPath
	p, err := k.discover(r.Context())
	var end struct {
		URL string `json:"end_session_endpoint"`
	}
	if err == nil {
		err = p.Claims(&end)
	}
	if err != nil || end.URL == "" {
		k.logger.Warn("signed out of ddash only: Keycloak's end-session endpoint is unknown", "err", err)
		http.Redirect(w, r, LoggedOutPath, http.StatusSeeOther)
		return
	}
	// Without the ID token, which ddash didn't keep, Keycloak asks the
	// Operator to confirm the logout.
	q := url.Values{"client_id": {k.opts.OIDC.ClientID}, "post_logout_redirect_uri": {loggedOutURL}}
	http.Redirect(w, r, end.URL+"?"+q.Encode(), http.StatusSeeOther)
}

// discover returns the realm's provider, discovering it on first use.
func (k *Keycloak) discover(ctx context.Context) (*oidc.Provider, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.provider != nil {
		return k.provider, nil
	}
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, k.opts.OIDC.HTTPClient), k.opts.OIDC.Issuer)
	if err != nil {
		return nil, err
	}
	k.provider = p
	return p, nil
}

func (k *Keycloak) oauth2(p *oidc.Provider) *oauth2.Config {
	ep := p.Endpoint()
	ep.AuthStyle = oauth2.AuthStyleInHeader
	return &oauth2.Config{
		ClientID:     k.opts.OIDC.ClientID,
		ClientSecret: k.opts.OIDC.ClientSecret,
		Endpoint:     ep,
		RedirectURL:  k.opts.PublicURL + CallbackPath,
		Scopes:       []string{oidc.ScopeOpenID, "profile"},
	}
}

func readLoginState(r *http.Request) (loginState, bool) {
	c, err := r.Cookie(LoginCookie)
	if err != nil {
		return loginState{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return loginState{}, false
	}
	var st loginState
	if json.Unmarshal(b, &st) != nil || st.State == "" || st.Nonce == "" || st.Verifier == "" {
		return loginState{}, false
	}
	return st, true
}

// localPath returns p if it is a path on this host, and "" otherwise, so a
// sign-in never ends on another site.
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n") {
		return ""
	}
	return p
}

func clearCookie(w http.ResponseWriter, name string, sameSite http.SameSite) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: sameSite})
}

func writeJSONError(w http.ResponseWriter, code int, kind, message string) {
	b, _ := json.Marshal(map[string]string{"code": kind, "message": message})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(b)
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · ddash</title>
<style>body{font-family:system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1rem;line-height:1.5}
@media (prefers-color-scheme:dark){body{background:#111;color:#ddd}a{color:#8bf}}</style></head>
<body><h1>{{.Title}}</h1><p>{{.Text}}</p><p><a href="` + LoginPath + `">Sign in</a></p></body></html>
`))

var continueTmpl = template.Must(template.New("continue").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta http-equiv="refresh" content="0;url={{.}}">
<title>Signed in · ddash</title></head>
<body><p>Signed in. <a href="{{.}}">Continue to ddash</a></p></body></html>
`))

// page writes a plain HTML page with a link to sign in.
func page(w http.ResponseWriter, code int, title, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = pageTmpl.Execute(w, struct{ Title, Text string }{title, text})
}

func loggedOut(w http.ResponseWriter, _ *http.Request) {
	page(w, http.StatusOK, "Signed out", "You are signed out of ddash.")
}
