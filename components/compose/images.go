package compose

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// CheckInterval is how often every running container's image is checked.
const CheckInterval = 6 * time.Hour

// requestGap spaces the registry requests of one check, so a check stays
// far below any registry's rate limit. Docker Hub doesn't count manifest
// HEAD requests against its pull limit.
const requestGap = 200 * time.Millisecond

// ref is an image reference, normalised as Docker does.
type ref struct {
	// registry is the host, "docker.io" for Docker Hub.
	registry string
	// repo is the repository, "library/nginx" for an official image.
	repo string
	tag  string
	// digest is set for a reference pinned by digest.
	digest string
}

// parseRef normalises an image reference: "nginx" is
// docker.io/library/nginx:latest.
func parseRef(s string) ref {
	var r ref
	if name, digest, ok := strings.Cut(s, "@"); ok {
		s, r.digest = name, digest
	}
	first, rest, ok := strings.Cut(s, "/")
	if ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		r.registry, s = first, rest
	} else {
		r.registry = "docker.io"
	}
	if i := strings.LastIndex(s, ":"); i > strings.LastIndex(s, "/") {
		s, r.tag = s[:i], s[i+1:]
	}
	if r.tag == "" {
		r.tag = "latest"
	}
	if r.registry == "docker.io" && !strings.Contains(s, "/") {
		s = "library/" + s
	}
	r.repo = s
	return r
}

func (r ref) String() string { return r.registry + "/" + r.repo + ":" + r.tag }

// sameRepo reports whether a container's repo digest
// ("jellyfin/jellyfin@sha256:…") names r's repository.
func (r ref) sameRepo(repoDigest string) (string, bool) {
	name, digest, ok := strings.Cut(repoDigest, "@")
	if !ok {
		return "", false
	}
	other := parseRef(name)
	return digest, other.registry == r.registry && other.repo == r.repo
}

// registries asks registries for manifest digests, with anonymous or
// configured credentials and each registry's bearer tokens.
type registries struct {
	hc *http.Client
	// dockerHub is where docker.io is asked.
	dockerHub string
	// creds maps a registry host to "user:token".
	creds map[string]string

	mu     sync.Mutex
	tokens map[string]string // scope → bearer token
}

// errRateLimited is a registry's 429.
var errRateLimited = errors.New("rate limited")

// base is where registry's API is.
func (rs *registries) base(registry string) string {
	if registry == "docker.io" {
		return strings.TrimRight(rs.dockerHub, "/")
	}
	if strings.HasPrefix(registry, "localhost") || strings.HasPrefix(registry, "127.") {
		return "http://" + registry
	}
	return "https://" + registry
}

// manifestTypes are what a HEAD accepts, an index first: the digest a
// multi-arch pull records is the index's.
var manifestTypes = strings.Join([]string{
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
}, ", ")

// digest is the registry's manifest digest for r's tag.
func (rs *registries) digest(ctx context.Context, r ref) (string, int, error) {
	u := fmt.Sprintf("%s/v2/%s/manifests/%s", rs.base(r.registry), r.repo, url.PathEscape(r.tag))
	scope := "repository:" + r.repo + ":pull"
	requests := 0
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		if err != nil {
			return "", requests, err
		}
		req.Header.Set("Accept", manifestTypes)
		rs.mu.Lock()
		token := rs.tokens[r.registry+" "+scope]
		rs.mu.Unlock()
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		} else if c := rs.creds[r.registry]; c != "" {
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c)))
		}
		res, err := rs.hc.Do(req)
		requests++
		if err != nil {
			return "", requests, err
		}
		res.Body.Close()
		switch res.StatusCode {
		case http.StatusOK:
			d := res.Header.Get("Docker-Content-Digest")
			if d == "" {
				return "", requests, errors.New("the registry answered without a digest")
			}
			return d, requests, nil
		case http.StatusTooManyRequests:
			return "", requests, errRateLimited
		case http.StatusNotFound:
			return "", requests, fmt.Errorf("the registry has no %s", r)
		case http.StatusUnauthorized:
			if attempt > 0 {
				return "", requests, errors.New("the registry refused ddash: add credentials in compose's config if the image is private")
			}
			challenge := res.Header.Get("WWW-Authenticate")
			t, n, err := rs.token(ctx, r.registry, challenge, scope)
			requests += n
			if err != nil {
				return "", requests, err
			}
			rs.mu.Lock()
			rs.tokens[r.registry+" "+scope] = t
			rs.mu.Unlock()
		default:
			return "", requests, fmt.Errorf("the registry answered %s", res.Status)
		}
	}
	return "", requests, errors.New("the registry refused ddash")
}

// token fetches a bearer token as challenge (a Bearer WWW-Authenticate)
// says, with the registry's credentials when configured.
func (rs *registries) token(ctx context.Context, registry, challenge, scope string) (string, int, error) {
	params := parseChallenge(challenge)
	realm := params["realm"]
	if realm == "" {
		return "", 0, errors.New("the registry refused ddash without saying how to sign in")
	}
	q := url.Values{"scope": {scope}}
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm+"?"+q.Encode(), nil)
	if err != nil {
		return "", 0, err
	}
	if c := rs.creds[registry]; c != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c)))
	}
	res, err := rs.hc.Do(req)
	if err != nil {
		return "", 1, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusTooManyRequests {
		return "", 1, errRateLimited
	}
	if res.StatusCode != http.StatusOK {
		return "", 1, fmt.Errorf("the registry's token service answered %s", res.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", 1, err
	}
	if body.Token == "" {
		body.Token = body.AccessToken
	}
	return body.Token, 1, nil
}

// parseChallenge reads `Bearer realm="…",service="…",scope="…"`.
func parseChallenge(h string) map[string]string {
	out := map[string]string{}
	_, rest, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(strings.Fields(h)[0], "Bearer") {
		return out
	}
	for _, part := range splitParams(rest) {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			out[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return out
}

// splitParams splits on commas outside quotes.
func splitParams(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case r == ',' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(out, cur.String())
}

// parseCreds reads registry_credentials: "<registry> <user>:<token>"
// entries separated by ";" or line breaks.
func parseCreds(s string) (map[string]string, error) {
	out := map[string]string{}
	for entry := range strings.FieldsFuncSeq(s, func(r rune) bool { return r == ';' || r == '\n' }) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		f := strings.Fields(entry)
		if len(f) != 2 || !strings.Contains(f[1], ":") {
			return nil, errors.New(`each entry is "<registry> <user>:<token>", e.g. "ghcr.io me:ghp_…"`)
		}
		host := f[0]
		if host == "registry-1.docker.io" || host == "index.docker.io" {
			host = "docker.io"
		}
		out[host] = f[1]
	}
	return out, nil
}
