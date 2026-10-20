package core_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	dockerapi "github.com/itsKontra/ddash/components/docker-api"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/core"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
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

func TestHealthzIsPublicAndTheAPIIsNot(t *testing.T) {
	host, err := core.New(t.Context(), core.Options{
		DataDir:  t.TempDir(),
		Release:  emptyRelease(t),
		Launcher: runtime.InProcess(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Shutdown(context.Background()) })

	healthz := httptest.NewRecorder()
	host.Handler().ServeHTTP(healthz, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	api := httptest.NewRecorder()
	host.Handler().ServeHTTP(api, coretest.ConnectRequest("docker-api", "ListContainers", `{}`))

	if healthz.Code != http.StatusOK {
		t.Errorf("/healthz: status %d: %s", healthz.Code, healthz.Body)
	}
	wantError(t, api, http.StatusUnauthorized, "unauthenticated", "")
}

func TestShutdownStopsComponentsInParallelWaitsTenSecondsAndClosesTheDB(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var lifetimes []context.Context
		// stubborn Components ignore their ctx for a minute.
		stubborn := sample.Hooks{Start: func(ctx context.Context, _ *sdk.Env, _ *samplepb.SampleConfig) error {
			mu.Lock()
			lifetimes = append(lifetimes, ctx)
			mu.Unlock()
			sdk.Every(ctx, time.Hour, func(context.Context) { time.Sleep(time.Minute) })
			return nil
		}}
		h := coretest.New(t, coretest.Options{
			Components: []sdk.Definition{sample.Define("a", nil, stubborn), sample.Define("b", nil, stubborn)},
			Plugins:    []coretest.Plugin{{Name: "both", Requires: []string{"a", "b"}}},
		})
		h.SeedConfig("a", configured)
		h.SeedConfig("b", configured)
		h.Install("both")
		synctest.Wait()

		start := time.Now()
		h.Shutdown()

		if took := time.Since(start); took != runtime.StopTimeout {
			t.Errorf("shutdown took %v, want %v", took, runtime.StopTimeout)
		}
		for i, ctx := range lifetimes {
			if ctx.Err() == nil {
				t.Errorf("component %d's ctx isn't cancelled", i)
			}
		}
		for _, name := range []string{"a", "b"} {
			if got := h.Status(name).State; got != runtime.Stopped {
				t.Errorf("%s is %s, want stopped", name, got)
			}
		}
		healthz := h.Do(httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if healthz.Code != http.StatusServiceUnavailable {
			t.Errorf("/healthz after shutdown: status %d, want 503: the core DB is closed", healthz.Code)
		}
		if got := healthz.Body.String(); got != "core DB unavailable\n" {
			t.Errorf("/healthz after shutdown says %q, want only \"core DB unavailable\": it's public, so no DB error text", got)
		}

		time.Sleep(time.Minute) // let the stubborn loops end inside the bubble
	})
}

// emptyRelease is a Release with nothing in it.
func emptyRelease(t *testing.T) runtime.Dir {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "catalogue.json"), []byte(`{"release":"dev"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return runtime.Dir(dir)
}
