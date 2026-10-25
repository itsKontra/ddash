package core_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakegithub"
	"github.com/itsKontra/ddash/sdk"
)

const repo = "itsKontra/ddash"

// publish publishes Release rel of hello with greeter being def.
func publish(t *testing.T, gh *fakegithub.Fake, rel string, def sdk.Definition) {
	t.Helper()
	gh.Publish(rel, coretest.ReleaseDir(t, coretest.Options{
		Release:    rel,
		Components: []sdk.Definition{def},
		Plugins:    []coretest.Plugin{hello},
	}))
}

// fromGitHub runs a core of Release rel on data, downloading it from gh,
// greeter being def.
func fromGitHub(t *testing.T, gh *fakegithub.Fake, data, rel string, def sdk.Definition) *coretest.Harness {
	t.Helper()
	return coretest.New(t, coretest.Options{
		Components: []sdk.Definition{def},
		DataDir:    data,
		Source:     runtime.GitHub(runtime.GitHubOptions{Repo: repo, Release: rel, DataDir: data, Client: gh.Client()}),
	})
}

// installedFromGitHub runs v1.0.0 from gh on a fresh data dir with hello
// installed and a note kept, then shuts it down.
func installedFromGitHub(t *testing.T, gh *fakegithub.Fake) string {
	t.Helper()
	data := t.TempDir()
	h := fromGitHub(t, gh, data, "v1.0.0", notebook())
	h.SeedConfig("greeter", configured)
	h.Install("hello")
	h.WaitState("greeter", runtime.OK)
	note(t, h, "before the update")
	h.Shutdown()
	return data
}

// catalogueStates returns each Plugin's Components' states as ListCatalogue
// answers them, by "plugin/component".
func catalogueStates(t *testing.T, h *coretest.Harness) map[string]string {
	t.Helper()
	got := coreJSON(t, h, "ListCatalogue", `{}`)
	states := map[string]string{}
	plugins, _ := got["plugins"].([]any)
	for _, p := range plugins {
		p := p.(map[string]any)
		components, _ := p["components"].([]any)
		for _, c := range components {
			c := c.(map[string]any)
			state, _ := c["state"].(string)
			states[p["name"].(string)+"/"+c["name"].(string)] = state
		}
	}
	return states
}

func TestBumpingTheReleaseDownloadsItsBinariesAndBundlesIntoData(t *testing.T) {
	gh := fakegithub.New(repo)
	publish(t, gh, "v1.0.0", notebook())
	publish(t, gh, "v1.1.0", notebook(addTag))
	data := installedFromGitHub(t, gh)

	h := fromGitHub(t, gh, data, "v1.1.0", notebook(addTag))

	h.WaitState("greeter", runtime.OK)
	for _, want := range []string{"components/greeter/v1.1.0/greeter", "plugins/hello/v1.1.0/hello.plugin.js", "plugins/hello/v1.1.0/hello.plugin.css"} {
		if _, err := os.Stat(filepath.Join(data, want)); err != nil {
			t.Errorf("/data/%s: %v", want, err)
		}
	}
	res := h.Get("/plugins/hello/v1.1.0/index.js")
	if want := "/* hello.plugin.js of Release v1.1.0 */"; res.Code != http.StatusOK || res.Body.String() != want {
		t.Errorf("GET hello's bundle: %d %q, want %q", res.Code, res.Body, want)
	}
}

func TestAPluginsBundleIsServedAsSoonAsItsInstallReturns(t *testing.T) {
	gh := fakegithub.New(repo)
	publish(t, gh, "v1.0.0", notebook())
	h := fromGitHub(t, gh, t.TempDir(), "v1.0.0", notebook())

	h.Install("hello")

	if res := h.Get("/plugins/hello/v1.0.0/index.js"); res.Code != http.StatusOK {
		t.Errorf("GET hello's bundle right after Install: %d, want 200", res.Code)
	}
}

func TestInstalledPluginsKeepRunningWhileGitHubIsUnreachable(t *testing.T) {
	gh := fakegithub.New(repo)
	publish(t, gh, "v1.0.0", notebook())
	data := installedFromGitHub(t, gh)
	gh.SetOffline(true)

	h := fromGitHub(t, gh, data, "v1.0.0", notebook())

	h.WaitState("greeter", runtime.OK)
	if got, want := note(t, h, ""), []string{"before the update"}; !slices.Equal(got, want) {
		t.Errorf("notes = %v, want %v", got, want)
	}
	if res := h.Get("/plugins/hello/v1.0.0/index.js"); res.Code != http.StatusOK {
		t.Errorf("GET hello's bundle: %d", res.Code)
	}
}

func TestRollingBackToTheLastReleaseWorksWhileGitHubIsUnreachable(t *testing.T) {
	gh := fakegithub.New(repo)
	publish(t, gh, "v1.0.0", notebook())
	publish(t, gh, "v1.1.0", notebook(addTag))
	data := installedFromGitHub(t, gh)
	updated := fromGitHub(t, gh, data, "v1.1.0", notebook(addTag))
	updated.WaitState("greeter", runtime.OK)
	updated.Shutdown()
	gh.SetOffline(true)

	h := fromGitHub(t, gh, data, "v1.0.0", notebook()) // the old image tag pinned again

	h.WaitState("greeter", runtime.OK)
	if got, want := note(t, h, ""), []string{"before the update"}; !slices.Equal(got, want) {
		t.Errorf("notes = %v, want %v", got, want)
	}
	res := h.Get("/plugins/hello/v1.0.0/index.js")
	if want := "/* hello.plugin.js of Release v1.0.0 */"; res.Code != http.StatusOK || res.Body.String() != want {
		t.Errorf("GET hello's bundle: %d %q, want %q", res.Code, res.Body, want)
	}
}

func TestOnANewReleaseWhileGitHubIsUnreachableInstalledPluginsShowFromTheCoreDB(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gh := fakegithub.New(repo)
		publish(t, gh, "v1.0.0", notebook())
		publish(t, gh, "v1.1.0", notebook(addTag))
		data := installedFromGitHub(t, gh)
		gh.SetOffline(true)

		h := fromGitHub(t, gh, data, "v1.1.0", notebook(addTag))
		synctest.Wait()

		got := coreJSON(t, h, "ListCatalogue", `{}`)
		plugins, _ := got["plugins"].([]any)
		if got["release"] != "v1.1.0" || len(plugins) != 1 || plugins[0].(map[string]any)["installed"] != true {
			t.Errorf("ListCatalogue = %v, want hello installed on v1.1.0", got)
		}
		if diff := cmp.Diff(map[string]string{"hello/greeter": "update pending"}, catalogueStates(t, h)); diff != "" {
			t.Errorf("the Catalogue (-want +got):\n%s", diff)
		}
		if got := coreJSON(t, h, "ListInstalledPlugins", `{}`); !strings.Contains(fmt.Sprint(got), "name:hello") {
			t.Errorf("ListInstalledPlugins = %v, want hello", got)
		}

		gh.SetOffline(false)
		time.Sleep(time.Minute)
		synctest.Wait()

		if got := h.Status("greeter"); got.State != runtime.OK {
			t.Errorf("a minute after GitHub is back greeter is %v, want ok", got)
		}
	})
}

func TestAPluginWhoseBundlesFailToDownloadShowsUpdatePending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gh := fakegithub.New(repo)
		publish(t, gh, "v1.0.0", notebook())
		publish(t, gh, "v1.1.0", notebook(addTag))
		data := installedFromGitHub(t, gh)
		gh.FailAsset("hello.plugin.js", true)

		h := fromGitHub(t, gh, data, "v1.1.0", notebook(addTag))
		synctest.Wait()

		hello := coreJSON(t, h, "ListCatalogue", `{}`)["plugins"].([]any)[0].(map[string]any)
		if got, _ := hello["updatePending"].(string); !strings.Contains(got, "hello.plugin.js of Release v1.1.0") {
			t.Errorf("hello's updatePending = %q, want its bundle's download named", got)
		}

		gh.FailAsset("hello.plugin.js", false)
		time.Sleep(time.Minute)
		synctest.Wait()

		hello = coreJSON(t, h, "ListCatalogue", `{}`)["plugins"].([]any)[0].(map[string]any)
		if got := hello["updatePending"]; got != nil {
			t.Errorf("a minute later hello's updatePending = %v, want none", got)
		}
	})
}

func TestADailyCheckShowsANewerReleaseAvailableInTheCatalogue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gh := fakegithub.New(repo)
		publish(t, gh, "v1.0.0", notebook())
		publish(t, gh, "v0.9.0", notebook()) // latest, yet older
		h := fromGitHub(t, gh, t.TempDir(), "v1.0.0", notebook())
		synctest.Wait()
		if got := coreJSON(t, h, "ListCatalogue", `{}`)["availableRelease"]; got != nil {
			t.Errorf("availableRelease = %v, want none newer", got)
		}

		publish(t, gh, "v1.1.0", notebook())
		time.Sleep(24 * time.Hour)
		synctest.Wait()

		if got := coreJSON(t, h, "ListCatalogue", `{}`)["availableRelease"]; got != "v1.1.0" {
			t.Errorf("a day later availableRelease = %v, want v1.1.0", got)
		}
	})
}

func TestAFailedDownloadLeavesTheComponentUpdatePendingRetriedWithBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gh := fakegithub.New(repo)
		publish(t, gh, "v1.0.0", notebook())
		publish(t, gh, "v1.1.0", notebook(addTag))
		data := installedFromGitHub(t, gh)
		gh.FailAsset("greeter", true)

		h := fromGitHub(t, gh, data, "v1.1.0", notebook(addTag))
		synctest.Wait()

		if got := h.Status("greeter"); got.State != runtime.UpdatePending || !strings.Contains(got.Message, "greeter of Release v1.1.0") {
			t.Errorf("greeter is %v, want update pending naming its download", got)
		}
		if diff := cmp.Diff(map[string]string{"hello/greeter": "update pending"}, catalogueStates(t, h)); diff != "" {
			t.Errorf("the Catalogue (-want +got):\n%s", diff)
		}
		wantError(t, h.Call("hello", "greeter", "Act", `{}`), http.StatusServiceUnavailable, "unavailable", "")

		gh.FailAsset("greeter", false)
		time.Sleep(time.Minute)
		synctest.Wait()

		if got := h.Status("greeter"); got.State != runtime.OK {
			t.Errorf("a minute after the download works again greeter is %v, want ok", got)
		}
		if got, want := note(t, h, ""), []string{"before the update"}; !slices.Equal(got, want) {
			t.Errorf("notes = %v, want %v", got, want)
		}
	})
}
