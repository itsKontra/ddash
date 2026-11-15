// Package registryapi is the registry-api Component: it reads image
// digests from container registries.
package registryapi

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/itsKontra/ddash/components/registry-api/registryapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// requestTimeout bounds one request to a registry.
const requestTimeout = 15 * time.Second

// defaultHub is where docker.io is asked unless the config names a mirror.
const defaultHub = "https://registry-1.docker.io"

// Definition is registry-api, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*pb.RegistryApiConfig]{
		Manifest: manifest,
		Service:  &pb.RegistryApiService_ServiceDesc,
		New:      func() sdk.Component[*pb.RegistryApiConfig] { return &component{} },
	})
}

type component struct {
	pb.UnimplementedRegistryApiServiceServer
	reg *registries
}

func (c *component) Start(_ context.Context, _ *sdk.Env, cfg *pb.RegistryApiConfig) error {
	creds, err := parseCreds(cfg.GetRegistryCredentials())
	if err != nil {
		var errs sdk.FieldErrors
		errs.Add("registry_credentials", "%s", err.Error())
		return errs.Err()
	}
	hub := cfg.GetDockerHubRegistry()
	if hub == "" {
		hub = defaultHub
	}
	c.reg = &registries{hc: &http.Client{Timeout: requestTimeout}, dockerHub: hub, creds: creds, tokens: map[string]string{}}
	return nil
}

// Health is ok: registry-api keeps nothing, and a registry that fails one
// image says nothing about the next one's.
func (c *component) Health(context.Context) sdk.Health { return sdk.Health{State: sdk.OK} }

func (c *component) GetDigest(ctx context.Context, req *pb.GetDigestRequest) (*pb.GetDigestResponse, error) {
	if req.GetRegistry() == "" || req.GetRepository() == "" || req.GetTag() == "" {
		return nil, status.Error(codes.InvalidArgument, "an image is a registry, a repository and a tag")
	}
	digest, requests, err := c.reg.digest(ctx, req)
	out := &pb.GetDigestResponse{Digest: digest, Requests: int32(requests), Outcome: pb.Outcome_OUTCOME_FOUND}
	switch {
	case err == nil:
	case errors.Is(err, errRateLimited):
		out.Outcome, out.Message = pb.Outcome_OUTCOME_RATE_LIMITED, "rate limited by "+req.GetRegistry()
	case errors.Is(err, errNoSuchImage):
		out.Outcome, out.Message = pb.Outcome_OUTCOME_NO_SUCH_IMAGE, err.Error()
	case errors.Is(err, errRefused):
		out.Outcome, out.Message = pb.Outcome_OUTCOME_REFUSED, err.Error()
	default:
		out.Outcome, out.Message = pb.Outcome_OUTCOME_FAILED, err.Error()
	}
	return out, nil
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

// What a registry answered in place of a digest.
var (
	// errRateLimited is a registry's 429.
	errRateLimited = errors.New("rate limited")
	// errNoSuchImage is a registry's 404.
	errNoSuchImage = errors.New("the registry has no")
	// errRefused is a registry that still answers 401 once ddash signed in
	// as it asked.
	errRefused = errors.New("the registry refused ddash")
)

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
func (rs *registries) digest(ctx context.Context, r *pb.GetDigestRequest) (string, int, error) {
	u := fmt.Sprintf("%s/v2/%s/manifests/%s", rs.base(r.GetRegistry()), r.GetRepository(), url.PathEscape(r.GetTag()))
	scope := "repository:" + r.GetRepository() + ":pull"
	requests := 0
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		if err != nil {
			return "", requests, err
		}
		req.Header.Set("Accept", manifestTypes)
		rs.mu.Lock()
		token := rs.tokens[r.GetRegistry()+" "+scope]
		rs.mu.Unlock()
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		} else if c := rs.creds[r.GetRegistry()]; c != "" {
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
			return "", requests, fmt.Errorf("%w %s/%s:%s", errNoSuchImage, r.GetRegistry(), r.GetRepository(), r.GetTag())
		case http.StatusUnauthorized:
			if attempt > 0 {
				return "", requests, fmt.Errorf("%w: add credentials in registry-api's config if the image is private", errRefused)
			}
			challenge := res.Header.Get("WWW-Authenticate")
			t, n, err := rs.token(ctx, r.GetRegistry(), challenge, scope)
			requests += n
			if err != nil {
				return "", requests, err
			}
			rs.mu.Lock()
			rs.tokens[r.GetRegistry()+" "+scope] = t
			rs.mu.Unlock()
		default:
			return "", requests, fmt.Errorf("the registry answered %s", res.Status)
		}
	}
	return "", requests, errRefused
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
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return "", 1, fmt.Errorf("%w: add credentials in registry-api's config if the image is private, or correct them", errRefused)
	}
	if res.StatusCode != http.StatusOK {
		return "", 1, fmt.Errorf("the registry's token service answered %s", res.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
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
