package registryapi_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/agent/agentfake"
	registryapi "github.com/itsKontra/ddash/components/registry-api"
	pb "github.com/itsKontra/ddash/components/registry-api/registryapipb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// start runs registry-api reading a fake Docker Hub that has
// jellyfin/jellyfin:10.9 and the official nginx:1.27.
func start(t *testing.T) (pb.RegistryApiServiceClient, *agentfake.Registry) {
	t.Helper()
	docker, err := agentfake.NewDocker(t.TempDir(), agentfake.DockerSeed{Images: []agentfake.Image{
		{Ref: "jellyfin/jellyfin:10.9", ID: "sha256:j1", Latest: "sha256:j2"},
		{Ref: "nginx:1.27", ID: "sha256:n1"},
	}}, agentfake.Options{StateDir: t.TempDir(), Release: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	registry := agentfake.NewRegistry(docker.Engine)
	srv := httptest.NewServer(registry)
	t.Cleanup(srv.Close)
	h := sdktest.New(t, registryapi.Definition())
	h.MustStart(&pb.RegistryApiConfig{DockerHubRegistry: &srv.URL})
	return pb.NewRegistryApiServiceClient(h.Conn), registry
}

func digest(t *testing.T, c pb.RegistryApiServiceClient, repository, tag string) *pb.GetDigestResponse {
	t.Helper()
	res, err := c.GetDigest(context.Background(), &pb.GetDigestRequest{Registry: "docker.io", Repository: repository, Tag: tag})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestATagsDigestIsReadAfterSigningInAsTheRegistryAsks(t *testing.T) {
	c, registry := start(t)

	got := digest(t, c, "jellyfin/jellyfin", "10.9")

	if got.GetOutcome() != pb.Outcome_OUTCOME_FOUND || !strings.HasPrefix(got.GetDigest(), "sha256:") || got.GetMessage() != "" {
		t.Fatalf("GetDigest = %v", got)
	}
	// Refused without a token, the token, then the manifest.
	if got.GetRequests() != 3 || registry.Requests() != 3 {
		t.Errorf("counted %d requests, the registry got %d; want 3", got.GetRequests(), registry.Requests())
	}

	// The token is kept: the next read is one request.
	if again := digest(t, c, "jellyfin/jellyfin", "10.9"); again.GetRequests() != 1 || again.GetDigest() != got.GetDigest() {
		t.Errorf("the second read = %v, want one request", again)
	}
	if official := digest(t, c, "library/nginx", "1.27"); official.GetOutcome() != pb.Outcome_OUTCOME_FOUND || official.GetDigest() == "" {
		t.Errorf("an official image = %v", official)
	}
}

func TestWhatARegistryAnswersInPlaceOfADigestIsAnOutcomeNotAFailedCall(t *testing.T) {
	c, registry := start(t)

	if got := digest(t, c, "jellyfin/jellyfin", "no-such-tag"); got.GetOutcome() != pb.Outcome_OUTCOME_NO_SUCH_IMAGE || !strings.Contains(got.GetMessage(), "docker.io/jellyfin/jellyfin:no-such-tag") {
		t.Errorf("a tag the registry lacks = %v", got)
	}

	registry.Private("me/private")
	if got := digest(t, c, "me/private", "latest"); got.GetOutcome() != pb.Outcome_OUTCOME_REFUSED || got.GetDigest() != "" || !strings.Contains(got.GetMessage(), "registry-api's config") {
		t.Errorf("a private image without credentials = %v, want refused, saying where credentials go", got)
	}

	registry.RateLimit(true)
	if got := digest(t, c, "jellyfin/jellyfin", "10.9"); got.GetOutcome() != pb.Outcome_OUTCOME_RATE_LIMITED || got.GetMessage() != "rate limited by docker.io" {
		t.Errorf("a registry limiting ddash = %v", got)
	}
}

func TestAnImageNeedsARegistryARepositoryAndATag(t *testing.T) {
	c, _ := start(t)

	_, err := c.GetDigest(context.Background(), &pb.GetDigestRequest{Registry: "docker.io", Repository: "library/nginx"})

	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("no tag: %v, want InvalidArgument", err)
	}
}

func TestCredentialsThatDontReadAsRegistryUserTokenAreRefused(t *testing.T) {
	err := sdktest.New(t, registryapi.Definition()).Start(&pb.RegistryApiConfig{RegistryCredentials: "ghcr.io"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("an entry without user:token: %v, want InvalidArgument", err)
	}

	err = sdktest.New(t, registryapi.Definition()).Start(&pb.RegistryApiConfig{RegistryCredentials: "ghcr.io me:ghp_x; registry.home.example ddash:secret"})
	if err != nil {
		t.Errorf("two well-formed entries: %v", err)
	}
}
