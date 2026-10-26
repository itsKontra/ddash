package nginxproxymanagerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// api calls Nginx Proxy Manager's API, signed in as ddash's user. There
// are no API keys: the email and password buy a token (one day by
// default), bought again once half its life is gone. A sign-in also reads
// the version and the user's permissions, which may refuse it.
type api struct {
	base, email, password string
	http                  *http.Client

	// signMu makes sign-ins take turns, so concurrent reads share one.
	signMu sync.Mutex

	mu   sync.Mutex
	sess *session
	// version is what GET /api last reported, kept across sign-ins.
	version string
}

// session is a token and what its sign-in learned.
type session struct {
	token   string
	renewAt time.Time
	// readOnly: Proxy Hosts are View for ddash's user.
	readOnly bool
}

// failure is why a call failed.
type failure struct {
	// down: Nginx Proxy Manager didn't answer, or refused ddash's sign-in
	// or permissions. The Component's Health is Down.
	down bool
	// status is the HTTP status answered; 0 when none was.
	status int
	// message is Nginx Proxy Manager's error message, when it sent one.
	message string
	text    string
}

func (f *failure) Error() string { return f.text }

func downf(format string, args ...any) *failure {
	return &failure{down: true, text: fmt.Sprintf(format, args...)}
}

// current is the session, signing in when there is none or it is due for
// renewal.
func (a *api) current(ctx context.Context) (*session, error) {
	a.signMu.Lock()
	defer a.signMu.Unlock()
	a.mu.Lock()
	s := a.sess
	a.mu.Unlock()
	if s != nil && time.Now().Before(s.renewAt) {
		return s, nil
	}
	s, err := a.signIn(ctx)
	a.mu.Lock()
	a.sess = s
	a.mu.Unlock()
	return s, err
}

// forget drops the session s, so the next call signs in again; nil drops
// whichever is current.
func (a *api) forget(s *session) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s == nil || a.sess == s {
		a.sess = nil
	}
}

// state is the version and whether Proxy Hosts are read-only, as the last
// sign-in found them.
func (a *api) state() (version string, readOnly bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.version, a.sess != nil && a.sess.readOnly
}

// minVersion is the oldest Nginx Proxy Manager ddash is coded to.
var minVersion = [2]int{2, 10}

// signIn reads the version, buys a token and reads the user's
// permissions. Each failure is down, with the fix in its text.
func (a *api) signIn(ctx context.Context) (*session, error) {
	var api struct {
		Version *struct{ Major, Minor, Revision int } `json:"version"`
	}
	if err := a.send(ctx, nil, http.MethodGet, "/api", nil, &api); err != nil {
		return nil, asDown(err)
	}
	if api.Version == nil {
		return nil, downf("Nginx Proxy Manager's GET /api reported no version: is %s Nginx Proxy Manager's admin address?", a.base)
	}
	v := api.Version
	version := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Revision)
	a.mu.Lock()
	a.version = version
	a.mu.Unlock()
	if v.Major < minVersion[0] || v.Major == minVersion[0] && v.Minor < minVersion[1] {
		return nil, downf("nginx-proxy-manager-api needs Nginx Proxy Manager 2.10 or later; it runs %s", version)
	}

	var token struct {
		Token     string `json:"token"`
		Expires   string `json:"expires"`
		Requires2 bool   `json:"requires_2fa"`
	}
	signedAt := time.Now()
	err := a.send(ctx, nil, http.MethodPost, "/api/tokens", map[string]string{"identity": a.email, "secret": a.password}, &token)
	var f *failure
	switch {
	case errors.As(err, &f) && f.status >= 400 && f.status < 500:
		return nil, downf("Nginx Proxy Manager refused ddash's sign-in as %s (%s): fix the email or password in nginx-proxy-manager-api's config", a.email, f.message)
	case err != nil:
		return nil, asDown(err)
	case token.Requires2:
		return nil, downf("Nginx Proxy Manager's user %s asks for 2FA: turn it off for this user", a.email)
	case token.Token == "":
		return nil, downf("Nginx Proxy Manager's sign-in answered no token")
	}
	s := &session{token: token.Token, renewAt: signedAt.Add(12 * time.Hour)}
	if expires, err := time.Parse(time.RFC3339, token.Expires); err == nil {
		s.renewAt = signedAt.Add(expires.Sub(signedAt) / 2)
	}

	var me struct {
		Roles       []string `json:"roles"`
		Permissions struct {
			Visibility   string `json:"visibility"`
			ProxyHosts   string `json:"proxy_hosts"`
			Certificates string `json:"certificates"`
		} `json:"permissions"`
	}
	if err := a.send(ctx, s, http.MethodGet, "/api/users/me?expand=permissions", nil, &me); err != nil {
		return nil, asDown(err)
	}
	if slices.Contains(me.Roles, "admin") {
		return s, nil
	}
	p := me.Permissions
	switch {
	case p.Visibility == "user":
		return nil, downf("ddash's user sees only the Proxy Hosts it created: set Item Visibility to All Items")
	case p.ProxyHosts != "manage" && p.ProxyHosts != "view":
		return nil, downf("ddash's user can't see Proxy Hosts: set them to Manage")
	case p.Certificates != "manage" && p.Certificates != "view":
		return nil, downf("ddash's user can't see SSL Certificates: set them to View")
	}
	s.readOnly = p.ProxyHosts == "view"
	return s, nil
}

// asDown makes a failure at sign-in down.
func asDown(err error) error {
	var f *failure
	if errors.As(err, &f) {
		f.down = true
	}
	return err
}

// call sends a request signed in, decoding a JSON answer into out (nil to
// ignore it). A token Nginx Proxy Manager no longer takes (expired, or
// permissions changed under it) is dropped and the call sent once more
// with a fresh sign-in.
func (a *api) call(ctx context.Context, method, path string, body, out any) error {
	for attempt := 0; ; attempt++ {
		s, err := a.current(ctx)
		if err != nil {
			return err
		}
		err = a.send(ctx, s, method, path, body, out)
		var f *failure
		if attempt == 0 && errors.As(err, &f) && tokenRefused(f) {
			a.forget(s)
			continue
		}
		return err
	}
}

// tokenRefused is whether a failure says the token, not the request, was
// refused: an expired token (400), a revoked one (401), or a permission
// no longer held (403).
func tokenRefused(f *failure) bool {
	return f.status == http.StatusUnauthorized || f.status == http.StatusForbidden ||
		f.status == http.StatusBadRequest && strings.Contains(strings.ToLower(f.message), "token")
}

// send sends one request, with s's token unless s is nil.
func (a *api) send(ctx context.Context, s *session, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s != nil {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	short := method + " " + strings.SplitN(path, "?", 2)[0]
	resp, err := a.http.Do(req)
	if err != nil {
		return downf("Nginx Proxy Manager doesn't answer at %s: %v", a.base, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return downf("Nginx Proxy Manager: reading %s: %v", short, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := message(raw)
		return &failure{status: resp.StatusCode, message: msg, text: fmt.Sprintf("Nginx Proxy Manager answered %s with %d: %s", short, resp.StatusCode, msg)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &failure{status: resp.StatusCode, text: fmt.Sprintf("Nginx Proxy Manager answered %s with no JSON it knows: %v", short, err)}
	}
	return nil
}

// message is the message of Nginx Proxy Manager's error envelope
// {"error": {"code": 400, "message": "…"}}, else the body's first line.
func message(raw []byte) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Error.Message != "" {
		return env.Error.Message
	}
	s := strings.TrimSpace(string(raw))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
