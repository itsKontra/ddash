package core_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	notifycomponent "github.com/itsKontra/ddash/components/notify"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	telegramapi "github.com/itsKontra/ddash/components/telegram-api"
	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
)

// coreJSON makes a core API call that must succeed and decodes its answer.
func coreJSON(t *testing.T, h *coretest.Harness, method, body string) map[string]any {
	t.Helper()
	res := h.CoreCall(method, body)
	if res.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", method, res.Code, res.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestInstallThroughTheCoreAPIStartsThePluginsComponents(t *testing.T) {
	h := coretest.New(t, coretest.Options{Components: []sdk.Definition{greeter(sample.Hooks{})}, Plugins: []coretest.Plugin{hello}})
	h.SeedConfig("greeter", configured)

	coreJSON(t, h, "Install", `{"plugin":"hello"}`)

	h.WaitState("greeter", runtime.OK)
	entries := coreEntries(h)
	if len(entries) != 1 || entries[0].Method != "Install" || entries[0].Outcome != appstate.OK || entries[0].Username != "test" {
		t.Errorf("core entries = %+v, want operator test's ok Install", entries)
	}
}

func TestUninstallThroughTheCoreAPIAnswersTheOrphansItLeaves(t *testing.T) {
	h := newShared(t)

	got := coreJSON(t, h, "Uninstall", `{"plugin":"relayer"}`)

	if diff := cmp.Diff(map[string]any{"orphans": []any{"relay"}}, got); diff != "" {
		t.Errorf("Uninstall (-want +got):\n%s", diff)
	}
	if _, running := h.Host.Runtime().Status()["relay"]; running {
		t.Errorf("relay still runs: %v", h.Status("relay"))
	}
	entries := coreEntries(h)
	if last := entries[len(entries)-1]; last.Method != "Uninstall" || !last.Disruptive || last.Outcome != appstate.OK || last.Username != "test" {
		t.Errorf("last core entry = %+v, want operator test's disruptive ok Uninstall", last)
	}
}

func TestUninstallThroughTheCoreAPIWithDeleteDataLeavesNoOrphans(t *testing.T) {
	h := newShared(t)

	got := coreJSON(t, h, "Uninstall", `{"plugin":"relayer","deleteData":true}`)

	if len(got) != 0 {
		t.Errorf("Uninstall = %v, want no orphans", got)
	}
	if files := dbFiles(t, h); slices.Contains(files, "relay.db") {
		t.Errorf("DB files %v, want relay.db deleted", files)
	}
}

func TestTheCatalogueListsEveryPluginWithTheComponentsItPullsIn(t *testing.T) {
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{greeter(sample.Hooks{}), sample.Define("relay", []string{"greeter"}, sample.Hooks{})},
		Plugins: []coretest.Plugin{
			{Name: "relayer", Title: "Relayer", Description: "Relays greetings.", Icon: "waypoints", Author: "itsKontra", Requires: []string{"relay"}},
			hello,
		},
	})
	h.SeedConfig("greeter", configured)
	h.Install("hello")
	h.WaitState("greeter", runtime.OK)

	got := coreJSON(t, h, "ListCatalogue", `{}`)

	want := map[string]any{
		"release": sdk.Release,
		"plugins": []any{
			map[string]any{
				"name": "hello", "title": "hello", "installed": true,
				"components": []any{map[string]any{"name": "greeter", "state": "ok", "configurable": true}},
			},
			map[string]any{
				"name": "relayer", "title": "Relayer", "description": "Relays greetings.", "icon": "waypoints", "author": "itsKontra",
				// greeter already runs for hello; relay doesn't run.
				"components": []any{map[string]any{"name": "greeter", "state": "ok", "configurable": true}, map[string]any{"name": "relay"}},
			},
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ListCatalogue (-want +got):\n%s", diff)
	}
}

// A Plugin pulls both channels in through notify; leaving them empty turns
// them off without the Catalogue showing the Plugin unconfigured or down.
func TestChannelsNotSetUpLeaveAPluginThatPullsThemInOK(t *testing.T) {
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{notifycomponent.Definition(), ntfyapi.Definition(), telegramapi.Definition()},
		Plugins:    []coretest.Plugin{{Name: "alerts", Requires: []string{"notify"}}},
	})
	h.Install("alerts")
	for _, c := range []string{"ntfy-api", "telegram-api", "notify"} {
		h.WaitState(c, runtime.OK)
	}

	cat := coreJSON(t, h, "ListCatalogue", `{}`)

	plugins, _ := cat["plugins"].([]any)
	if len(plugins) != 1 {
		t.Fatalf("plugins %v, want alerts", plugins)
	}
	got := map[string]any{}
	for _, c := range plugins[0].(map[string]any)["components"].([]any) {
		c := c.(map[string]any)
		got[c["name"].(string)] = c
	}
	want := map[string]any{
		"notify":       map[string]any{"name": "notify", "state": "ok", "configurable": true},
		"ntfy-api":     map[string]any{"name": "ntfy-api", "state": "ok", "message": "not set up", "configurable": true},
		"telegram-api": map[string]any{"name": "telegram-api", "state": "ok", "message": "not set up", "configurable": true},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("alerts' Components (-want +got):\n%s", diff)
	}

	channels := h.Call("alerts", "notify", "ListChannels", `{}`)
	var listed struct{ Channels []map[string]any }
	_ = json.Unmarshal(channels.Body.Bytes(), &listed)
	for _, ch := range listed.Channels {
		if ch["setUp"] != nil || ch["problem"] != nil {
			t.Errorf("channel %v, want not set up, no problem", ch)
		}
	}
	if len(listed.Channels) != 2 {
		t.Errorf("ListChannels = %d %s, want both channels", channels.Code, channels.Body)
	}
	sent := h.Call("alerts", "notify", "Send", `{"notification":{"title":"NAS down"},"category":"CATEGORY_CHECKS"}`)
	if sent.Code != http.StatusOK || strings.Count(sent.Body.String(), "DELIVERY_STATE_NOT_SET_UP") != 2 {
		t.Errorf("Send = %d %s, want ok, not set up on both channels", sent.Code, sent.Body)
	}
}

func TestDeletingAnOrphanDeletesItsDBFileAndConfig(t *testing.T) {
	h := newShared(t)
	coreJSON(t, h, "Uninstall", `{"plugin":"relayer"}`)
	if got := coreJSON(t, h, "ListCatalogue", `{}`)["orphans"]; !cmp.Equal(got, []any{"relay"}) {
		t.Fatalf("orphans after uninstalling relayer = %v, want [relay]", got)
	}

	coreJSON(t, h, "DeleteOrphan", `{"component":"relay"}`)

	if got := coreJSON(t, h, "ListCatalogue", `{}`)["orphans"]; got != nil {
		t.Errorf("orphans = %v, want none", got)
	}
	if files := dbFiles(t, h); slices.Contains(files, "relay.db") || !slices.Contains(files, "greeter.db") {
		t.Errorf("DB files %v, want relay.db deleted and greeter.db kept", files)
	}
	entries := coreEntries(h)
	if last := entries[len(entries)-1]; last.Method != "DeleteOrphan" || !last.Disruptive || last.Outcome != appstate.OK {
		t.Errorf("last core entry = %+v, want a disruptive ok DeleteOrphan", last)
	}

	h.Install("relayer")

	h.WaitState("relay", runtime.Unconfigured) // its config is gone too
}

func TestOnlyAnOrphanCanBeDeleted(t *testing.T) {
	h := newShared(t)

	for _, tc := range []struct {
		component string
		code      int
		message   string
	}{
		{"greeter", http.StatusBadRequest, "greeter isn't an orphan: an installed Plugin requires it"},
		{"nobody", http.StatusNotFound, "nobody has no DB file"},
		{"../core", http.StatusNotFound, "../core has no DB file"},
	} {
		res := h.CoreCall("DeleteOrphan", `{"component":"`+tc.component+`"}`)

		if res.Code != tc.code || !strings.Contains(res.Body.String(), tc.message) {
			t.Errorf("deleting orphan %s: %d %s, want %d %q", tc.component, res.Code, res.Body, tc.code, tc.message)
		}
	}
	if files := dbFiles(t, h); !slices.Contains(files, "greeter.db") {
		t.Errorf("DB files %v, want greeter.db kept", files)
	}
}

func TestTheCoreAPIAnswersARefusedActionWithItsCode(t *testing.T) {
	h := coretest.New(t, coretest.Options{Components: []sdk.Definition{greeter(sample.Hooks{})}, Plugins: []coretest.Plugin{hello}})

	res := h.CoreCall("Uninstall", `{"plugin":"hello"}`)

	body := res.Body.String()
	if res.Code != http.StatusNotFound || !strings.Contains(body, `"code":"not_found"`) || !strings.Contains(body, "hello isn't installed") {
		t.Errorf("uninstalling a Plugin that isn't installed: %d %s, want not_found", res.Code, body)
	}
}
