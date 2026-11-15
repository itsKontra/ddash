package compose

import (
	"strings"
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
