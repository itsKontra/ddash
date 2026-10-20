package core_test

import (
	"encoding/json"
	"net/http"
	"testing"

	dockerapi "github.com/itsKontra/ddash/components/docker-api"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
)

// docker is the docker Plugin as the Release's catalogue lists it.
var docker = coretest.Plugin{Name: "docker", Requires: []string{"docker-api"}}

func TestInstallingDockerServesDockerApiContainersToTheUI(t *testing.T) {
	media := fixture.Server(t, "../components/docker-api/testdata/baseline")
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{dockerapi.Definition()},
		Plugins:    []coretest.Plugin{docker},
	})
	h.SeedConfig("docker-api", &dockerapipb.DockerApiConfig{Hosts: []*dockerapipb.Host{
		{Name: "media", Url: "tcp://" + media.Listener.Addr().String()},
	}})

	h.Install("docker")
	h.WaitState("docker-api", runtime.OK)
	res := h.Call("docker", "docker-api", "ListContainers", `{}`)

	if res.Code != http.StatusOK {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	var got struct {
		Hosts []struct {
			Host       string `json:"host"`
			Containers []struct {
				Name  string `json:"name"`
				State string `json:"state"`
			} `json:"containers"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %s: %v", res.Body, err)
	}
	if len(got.Hosts) != 1 || got.Hosts[0].Host != "media" || len(got.Hosts[0].Containers) != 3 {
		t.Fatalf("got %s, want media's 3 containers", res.Body)
	}
	if c := got.Hosts[0].Containers[0]; c.Name != "jellyfin" || c.State != "running" {
		t.Errorf("first container = %+v, want jellyfin running", c)
	}
}
