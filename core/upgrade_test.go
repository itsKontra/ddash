package core_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/migrate"
)

// v003Migrations are the core DB migrations v0.0.3 shipped: those before
// pending_upgrade.
const v003Migrations = 6

// v003 is a v0.0.3 ddash's /data as that Release left it: the core DB at
// its migrations, the Checks Plugin installed (pulling notify, which sent
// to ntfy-api and telegram-api), ntfy set up to ntfyServer with notify's
// filter for ntfy's image updates switched off, and Telegram saved
// without a bot (not set up). The config rows are v0.0.3's wire shapes,
// written field by field: notify's message and the old channel configs no
// longer exist as types.
func v003(t *testing.T, ntfyServer string) string {
	t.Helper()
	data := t.TempDir()
	db, err := migrate.Open(filepath.Join(data, "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fsys := fstest.MapFS{}
	entries, err := os.ReadDir("coredb/migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries[:v003Migrations] {
		b, err := os.ReadFile(filepath.Join("coredb/migrations", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		fsys["migrations/"+e.Name()] = &fstest.MapFile{Data: b}
	}
	sub, _ := fstest.MapFS(fsys).Sub("migrations")
	if err := migrate.Apply(t.Context(), db, sub); err != nil {
		t.Fatal(err)
	}
	str := func(b []byte, n protowire.Number, s string) []byte {
		return protowire.AppendString(protowire.AppendTag(b, n, protowire.BytesType), s)
	}
	boolean := func(b []byte, n protowire.Number, v bool) []byte {
		return protowire.AppendVarint(protowire.AppendTag(b, n, protowire.VarintType), protowire.EncodeBool(v))
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	manifest, _ := json.Marshal(map[string]any{
		"name": "checks", "title": "Checks", "description": "TCP, ICMP and DNS Checks against any address, with their state and latency.",
		"icon": "activity", "author": "itsKontra", "requires": []string{"checks", "notify"},
	})
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO installed_plugin (name, release, installed_at, manifest) VALUES ('checks', 'v0.0.3', ?, ?)`, []any{now, string(manifest)}},
		// ntfy-api: server = 1, topic = 2.
		{`INSERT INTO config (owner, value, updated_at) VALUES ('ntfy-api', ?, ?)`, []any{str(str(nil, 1, ntfyServer), 2, "ddash-alerts"), now}},
		// telegram-api: server = 1 only; the token (2) and chat ID (3) empty.
		{`INSERT INTO config (owner, value, updated_at) VALUES ('telegram-api', ?, ?)`, []any{str(nil, 1, "https://api.telegram.org"), now}},
		// notify: ntfy_image_updates = 2 off, telegram_checks = 4 off.
		{`INSERT INTO config (owner, value, updated_at) VALUES ('notify', ?, ?)`, []any{boolean(boolean(nil, 2, false), 4, false), now}},
	} {
		if _, err := db.ExecContext(t.Context(), q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []string{"checks", "notify", "ntfy-api", "telegram-api"} {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO component_release (name, release) VALUES (?, 'v0.0.3')`, c); err != nil {
			t.Fatal(err)
		}
	}
	return data
}

// The Release after v0.0.3: Checks no longer pulls a channel in, and each
// channel is a Plugin of its own.
func afterV003(data string) coretest.Options {
	return coretest.Options{
		DataDir:    data,
		Components: []sdk.Definition{checks.Definition(), sender()},
		Plugins:    []coretest.Plugin{{Name: "checks", Requires: []string{"checks"}}},
	}
}

func installedPlugins(h *coretest.Harness) []string {
	var names []string
	for _, p := range h.Host.Runtime().Installed() {
		names = append(names, p.Name)
	}
	return names
}

// Updating from v0.0.3 with ntfy set up keeps its Notifications arriving,
// filtered as before, without the Operator doing anything: ntfy's Plugin
// is installed for it. Telegram, never set up, isn't.
func TestAnUpdateFromV003InstallsTheChannelThatWasSetUpWithItsFilter(t *testing.T) {
	ntfy := newNtfy(t)
	data := v003(t, ntfy.URL)

	l := newNotifyLab(t, afterV003(data))

	if diff := cmp.Diff([]string{"checks", "ntfy", "sending"}, installedPlugins(l.Harness)); diff != "" {
		t.Errorf("installed Plugins (-want +got):\n%s", diff)
	}
	l.WaitState("ntfy-api", runtime.OK)
	raw, _, err := l.Host.Config().Raw(t.Context(), "ntfy-api")
	if err != nil {
		t.Fatal(err)
	}
	var cfg ntfyapipb.NtfyApiConfig
	if err := proto.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.GetTopic() != "ddash-alerts" || cfg.GetServer() != ntfy.URL || cfg.ImageUpdates == nil || cfg.GetImageUpdates() || cfg.Checks != nil || cfg.ActionOutcomes != nil {
		t.Errorf("ntfy-api's config = %v, want its topic with image updates off and the rest on", &cfg)
	}
	// notify is gone with its data: no orphan, no config.
	if saved, err := l.Host.Config().Saved(t.Context(), "notify"); err != nil || saved {
		t.Errorf("notify's config saved = %v, %v, want dropped", saved, err)
	}
	if orphans := l.Host.Runtime().Orphans(); slices.Contains(orphans, "notify") {
		t.Errorf("orphans %v, want no notify", orphans)
	}

	l.ntfy = ntfy
	if diff := cmp.Diff(map[string]string{"ntfy-api": "sent"}, l.send(t, "CATEGORY_CHECKS", "NAS down")); diff != "" {
		t.Errorf("a Check's Notification (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]string{"ntfy-api": "filtered"}, l.send(t, "CATEGORY_IMAGE_UPDATES", "1 image update")); diff != "" {
		t.Errorf("an image update's Notification (-want +got):\n%s", diff)
	}
	if got := ntfy.published(); !slices.Equal(got, []string{"NAS down"}) {
		t.Errorf("ntfy got %v, want the Check's Notification only", got)
	}
}

// The upgrade runs once: a channel Plugin the Operator uninstalls after it
// stays uninstalled across restarts.
func TestTheUpgradeFromV003RunsOnce(t *testing.T) {
	ntfy := newNtfy(t)
	data := v003(t, ntfy.URL)
	l := newNotifyLab(t, afterV003(data))
	if diff := cmp.Diff([]string{"checks", "ntfy", "sending"}, installedPlugins(l.Harness)); diff != "" {
		t.Fatalf("installed Plugins after the update (-want +got):\n%s", diff)
	}
	if _, err := l.Uninstall("ntfy", false); err != nil {
		t.Fatal(err)
	}
	l.Shutdown()

	l = newNotifyLab(t, afterV003(data))

	if diff := cmp.Diff([]string{"checks", "sending"}, installedPlugins(l.Harness)); diff != "" {
		t.Errorf("installed Plugins after a restart (-want +got):\n%s", diff)
	}
}

// A fresh ddash has no channel to carry over and installs none, even once
// a channel's config is saved: only config from before the update counts.
func TestAFreshDdashInstallsNoChannel(t *testing.T) {
	data := t.TempDir()
	l := newNotifyLab(t, afterV003(data))
	l.SeedConfig("ntfy-api", &ntfyapipb.NtfyApiConfig{Topic: "ddash-alerts"})
	l.Shutdown()

	l = newNotifyLab(t, afterV003(data))

	if diff := cmp.Diff([]string{"sending"}, installedPlugins(l.Harness)); diff != "" {
		t.Errorf("installed Plugins (-want +got):\n%s", diff)
	}
}
