package dockerapi_test

import (
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"

	dockerapi "github.com/itsKontra/ddash/components/docker-api"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// socketProxy fakes one host's socket-proxy serving a scenario, and returns
// its config URL.
func socketProxy(t *testing.T, scenario string) (string, *fixture.Fake) {
	srv := fixture.Server(t, "testdata/"+scenario)
	return "tcp://" + srv.Listener.Addr().String(), srv
}

// unreachable returns a socket-proxy URL nothing listens on.
func unreachable(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return "tcp://" + addr
}

func start(t *testing.T, hosts ...*dockerapipb.Host) (*sdktest.Harness, dockerapipb.DockerApiServiceClient) {
	h := sdktest.New(t, dockerapi.Definition())
	h.MustStart(&dockerapipb.DockerApiConfig{Hosts: hosts})
	return h, dockerapipb.NewDockerApiServiceClient(h.Conn)
}

func list(t *testing.T, c dockerapipb.DockerApiServiceClient) *dockerapipb.ListContainersResponse {
	t.Helper()
	resp, err := c.ListContainers(t.Context(), &dockerapipb.ListContainersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestNoHostsIsAValidConfig(t *testing.T) {
	h, c := start(t)

	if got := list(t, c); len(got.GetHosts()) != 0 {
		t.Errorf("hosts = %v, want none", got.GetHosts())
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health = %v, want ok", got)
	}
}

func TestListsContainersPerHostWithStateAndPublishedPorts(t *testing.T) {
	media, _ := socketProxy(t, "baseline")
	dockerEnv, _ := socketProxy(t, "docker-env")
	_, c := start(t, &dockerapipb.Host{Name: "media", Url: media}, &dockerapipb.Host{Name: "docker-env", Url: dockerEnv})

	got := list(t, c)

	want := &dockerapipb.ListContainersResponse{Hosts: []*dockerapipb.HostContainers{
		{Host: "media", Address: "127.0.0.1", Containers: []*dockerapipb.Container{
			{
				Id:     "8c5d2f1e4b7a9c3d6e8f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d",
				Name:   "jellyfin",
				Image:  "lscr.io/linuxserver/jellyfin:latest",
				State:  "running",
				Status: "Up 3 hours (healthy)",
				Ports: []*dockerapipb.Port{
					{Ip: "198.18.2.10", PublicPort: 8096, PrivatePort: 8096, Protocol: "tcp"},
					{Ip: "::", PublicPort: 8096, PrivatePort: 8096, Protocol: "tcp"},
					{Ip: "198.18.2.10", PublicPort: 7359, PrivatePort: 7359, Protocol: "udp"},
				},
			},
			{
				Id:     "2e4f6a8b0c1d3e5f7a9b1c3d5e7f9a1b3c5d7e9f1a3b5c7d9e1f3a5b7c9d1e3f",
				Name:   "sonarr",
				Image:  "lscr.io/linuxserver/sonarr:latest",
				State:  "exited",
				Status: "Exited (0) 2 days ago",
			},
			{
				Id:     "9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c3b2a1f0e9d8c7b6a5f4e3d2c1b0a9f8e",
				Name:   "qbittorrent",
				Image:  "lscr.io/linuxserver/qbittorrent:latest",
				State:  "restarting",
				Status: "Restarting (1) 5 seconds ago",
				Ports:  []*dockerapipb.Port{{Ip: "198.18.2.10", PublicPort: 8081, PrivatePort: 8080, Protocol: "tcp"}},
			},
		}},
		{Host: "docker-env", Address: "127.0.0.1", Containers: []*dockerapipb.Container{
			{
				Id:     "4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b",
				Name:   "grafana",
				Image:  "grafana/grafana:12.2.0",
				State:  "running",
				Status: "Up 12 days",
				Ports:  []*dockerapipb.Port{{Ip: "198.18.2.20", PublicPort: 3000, PrivatePort: 3000, Protocol: "tcp"}},
			},
		}},
	}}
	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("ListContainers (-want +got):\n%s", diff)
	}
}

func TestAnUnreachableHostIsListedWithItsErrorAndNamedInHealth(t *testing.T) {
	media, _ := socketProxy(t, "baseline")
	h, c := start(t, &dockerapipb.Host{Name: "media", Url: media}, &dockerapipb.Host{Name: "docker-env", Url: unreachable(t)})

	got := list(t, c)

	if n := len(got.GetHosts()[0].GetContainers()); n != 3 || got.GetHosts()[0].GetError() != "" {
		t.Errorf("media: %d containers, error %q; want 3 and none", n, got.GetHosts()[0].GetError())
	}
	if dead := got.GetHosts()[1]; dead.GetHost() != "docker-env" || dead.GetError() == "" || len(dead.GetContainers()) != 0 {
		t.Errorf("docker-env = %v, want listed with an error and no containers", dead)
	}
	if health := h.Health(); health.State != sdk.Degraded || !strings.HasPrefix(health.Message, "docker-env unreachable") {
		t.Errorf("health = %v, want degraded: docker-env unreachable", health)
	}
}

func TestEveryHostUnreachableIsDown(t *testing.T) {
	h, _ := start(t, &dockerapipb.Host{Name: "media", Url: unreachable(t)})

	if health := h.Health(); health.State != sdk.Down || !strings.HasPrefix(health.Message, "media unreachable") {
		t.Errorf("health = %v, want down: media unreachable", health)
	}
}

func TestAProxyRefusingContainersIsAnError(t *testing.T) {
	media, _ := socketProxy(t, "proxy-denied")
	h, c := start(t, &dockerapipb.Host{Name: "media", Url: media})

	if got := list(t, c).GetHosts()[0].GetError(); !strings.Contains(got, "Forbidden") {
		t.Errorf("error = %q, want the proxy's Forbidden", got)
	}
	if health := h.Health(); health.State != sdk.Down || !strings.HasPrefix(health.Message, "media: ") {
		t.Errorf("health = %v, want down naming media", health)
	}
}

func TestContainersAreCachedBetweenRefreshes(t *testing.T) {
	media, srv := socketProxy(t, "baseline")
	h, c := start(t, &dockerapipb.Host{Name: "media", Url: media})

	list(t, c)
	list(t, c)
	h.Health()

	if n := srv.Hits("GET /v1.44/containers/json"); n != 1 {
		t.Errorf("socket-proxy asked %d times, want once within the TTL", n)
	}
}

func TestStartReportsConfigErrorsByPath(t *testing.T) {
	h := sdktest.New(t, dockerapi.Definition())

	err := h.Start(&dockerapipb.DockerApiConfig{Hosts: []*dockerapipb.Host{
		{Name: "media", Url: "tcp://198.18.2.10:2375"},
		{Name: "", Url: "198.18.2.20:2375"},
		{Name: "media", Url: "https://203.0.113.5"},
	}})

	got := sdk.FieldErrorsOf(err)
	var paths []string
	for _, fe := range got {
		paths = append(paths, fe.Path)
	}
	want := []string{"hosts[1].name", "hosts[1].url", "hosts[2].name", "hosts[2].url"}
	if !slices.Equal(paths, want) {
		t.Errorf("errors at %v, want %v (%v)", paths, want, got)
	}
}
