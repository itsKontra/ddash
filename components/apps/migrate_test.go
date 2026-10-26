package apps_test

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/sdk/migrate"
)

// olderData lays out a data dir as the Release before App records left
// it: apps with overlays and manual Apps, and checks with each one's App
// Check, up for two beats.
//
//   - overlay grafana (name Graphs, group Monitoring): Proxy Host 3;
//   - overlay video (group Media) and manual Jellyfin at video's URL;
//   - manual Markdown at md's URL, md having no overlay;
//   - overlay paperless, whose Proxy Host is gone;
//   - manual pfSense, no Proxy Host's.
//
// The App Checks have ids 1 to 6 in that order (md's is 4, the manual
// Markdown's 5), plus 7 for the untouched influxdb.
func olderData(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	olderApps(t, dir,
		`INSERT INTO overlay (server_name, name, group_name) VALUES ('grafana.home.example', 'Graphs', 'Monitoring')`,
		`INSERT INTO overlay (server_name, group_name, workload_kind, workload_host, workload_id) VALUES ('video.home.example', 'Media', 'lxc', 'pve', '212')`,
		`INSERT INTO overlay (server_name, name) VALUES ('paperless.home.example', 'Documents')`,
		`INSERT INTO manual_app (id, name, url, icon, group_name) VALUES ('manual:11bb', 'Jellyfin', 'https://video.home.example', 'jellyfin', 'Movies')`,
		`INSERT INTO manual_app (id, name, url) VALUES ('manual:22cc', 'Markdown', 'https://md.home.example')`,
		`INSERT INTO manual_app (id, name, url, icon, group_name) VALUES ('manual:00aa', 'pfSense', 'https://198.18.0.1', 'pfsense', 'Network')`,
	)

	at := time.Now().Add(-2 * time.Minute).Truncate(time.Minute).UnixMilli()
	next := at + time.Minute.Milliseconds()
	var stmts []string
	for i, id := range []string{"proxy:grafana.home.example", "proxy:video.home.example", "manual:11bb", "proxy:md.home.example", "manual:22cc", "manual:00aa", "proxy:influxdb.home.example"} {
		stmts = append(stmts,
			fmt.Sprintf(`INSERT INTO checks (id, kind, name, type, target, state, since, last_beat, app_id)
				VALUES (%d, 'app', '%s', 'http', 'https://old.example', 'up', %d, %d, '%s')`, i+1, id, at, next, id),
			fmt.Sprintf(`INSERT INTO check_stat (check_id, period, bucket, up, latency_avg, latency_min, latency_max)
				VALUES (%d, 'minute', %d, 1, 10, 10, 10), (%d, 'minute', %d, 1, 12, 12, 12)`, i+1, at, i+1, next))
	}
	seedDB(t, filepath.Join(dir, "db", "checks.db"), os.DirFS("../checks/migrations"), stmts...)
	return dir
}

// olderApps lays out apps' DB in dir as the Release before App records
// left it, with stmts run on it.
func olderApps(t *testing.T, dir string, stmts ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{}
	for _, name := range []string{"0001_apps.sql", "0002_workload_override.sql"} {
		body, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		old[name] = &fstest.MapFile{Data: body}
	}
	seedDB(t, filepath.Join(dir, "db", "apps.db"), old, stmts...)
}

func seedDB(t *testing.T, path string, migrations fs.FS, stmts ...string) {
	t.Helper()
	db, err := migrate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate.Apply(t.Context(), db, migrations); err != nil {
		t.Fatal(err)
	}
	for _, q := range stmts {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// The upgrade keeps every overlay's and manual App's id, so their Checks
// and uptime carry over. The first sync links the overlays to their Proxy
// Hosts by server_name. A manual App at a Proxy Host's hostname merges
// into that Proxy Host's App, which keeps the manual App's name, icon and
// group; the manual App's Check goes. Other manual Apps become Link only.
func TestAnUpgradeKeepsEveryOverlayAndManualApp(t *testing.T) {
	l := newLab(t, labOptions{dataDir: olderData(t)})

	type app struct {
		ID, Name, URL, Icon, Group string
		Reach                      appspb.Reach
		Orphaned                   bool
	}
	got := map[string]app{}
	for _, a := range l.list().GetApps() {
		got[a.GetId()] = app{a.GetId(), a.GetName(), a.GetUrl(), a.GetIcon(), a.GetGroup(), a.GetReach(), a.GetOrphaned()}
	}
	proxied, linkOnly := appspb.Reach_REACH_PROXIED, appspb.Reach_REACH_LINK_ONLY
	for _, want := range []app{
		{"proxy:grafana.home.example", "Graphs", "https://grafana.home.example", "", "Monitoring", proxied, false},
		{"proxy:video.home.example", "Jellyfin", "https://video.home.example", "jellyfin", "Movies", proxied, false},
		{"proxy:md.home.example", "Markdown", "https://md.home.example", "", "", proxied, false},
		{"proxy:paperless.home.example", "Documents", "https://paperless.home.example", "", "", appspb.Reach_REACH_UNSPECIFIED, true},
		{"manual:00aa", "pfSense", "https://198.18.0.1", "pfsense", "Network", linkOnly, false},
		{"proxy:influxdb.home.example", "Influxdb", "https://influxdb.home.example", "", "", proxied, false},
	} {
		if d := diff(want, got[want.ID]); d != "" {
			t.Errorf("%s (-want +got):\n%s", want.ID, d)
		}
	}
	for _, gone := range []string{"manual:11bb", "manual:22cc"} {
		if _, ok := got[gone]; ok {
			t.Errorf("the merged manual App %s is still listed", gone)
		}
	}
	if len(got) != 30 {
		t.Errorf("%d Apps, want the 23 Proxy Hosts', the 5 Not-proxied Host Overrides', paperless's orphan and pfSense", len(got))
	}
	if w := l.app("proxy:video.home.example").GetSettings().GetWorkload(); w.GetId() != "212" {
		t.Errorf("video's Workload override = %v, want the overlay's", w)
	}

	checks := l.appChecks()
	for id, check := range map[string]int64{
		"proxy:grafana.home.example": 1, "proxy:video.home.example": 2, "proxy:md.home.example": 4,
		"manual:00aa": 6, "proxy:influxdb.home.example": 7,
	} {
		if got := checks[id].GetId(); got != check {
			t.Errorf("%s's Check is %d, want %d", id, got, check)
			continue
		}
		if up := uptime(t, l, check).GetUp(); up < 2 {
			t.Errorf("%s's Check has %d up beats in 24 h, want its 2 migrated ones", id, up)
		}
	}
	for _, gone := range []string{"manual:11bb", "manual:22cc", "proxy:paperless.home.example"} {
		if c := checks[gone]; c != nil {
			t.Errorf("%s still has a Check: %v", gone, c)
		}
	}
	if c := checks["proxy:grafana.home.example"]; c.GetSpec().GetTarget() != "https://grafana.home.example" {
		t.Errorf("Grafana's Check probes %s, want its URL", c.GetSpec().GetTarget())
	}

	// Linked now: a rename in Nginx Proxy Manager keeps Grafana.
	l.npm.EditProxyHost(3, func(row map[string]any) { row["domain_names"] = []string{"graphs.home.example"} })
	l.refresh()
	if got := l.app("proxy:grafana.home.example"); got.GetLinks().GetProxyHostId() != 3 || got.GetOrphaned() {
		t.Errorf("Grafana after a rename = %v, want it linked to Proxy Host 3", got)
	}
}

// A migrated overlay's Proxy Host without a certificate gets an http URL.
func TestAMigratedOverlaysSchemeFollowsTheCertificate(t *testing.T) {
	dir := t.TempDir()
	olderApps(t, dir, `INSERT INTO overlay (server_name, group_name) VALUES ('grafana.home.example', 'Monitoring')`)
	npm, failNPM := newNPM(t)
	npm.EditProxyHost(3, noCert)

	l := newLab(t, labOptions{dataDir: dir, npm: npm, failNPM: failNPM})

	if got := l.app("proxy:grafana.home.example"); got.GetUrl() != "http://grafana.home.example" || got.GetGroup() != "Monitoring" {
		t.Errorf("Grafana = %v, want http, its group kept", got)
	}
}

// uptime is a Check's uptime over 24 h.
func uptime(t *testing.T, l *lab, id int64) *checkspb.Uptime {
	t.Helper()
	res := l.h.Call("apps", "checks", "GetHistory", fmt.Sprintf(`{"id": "%d"}`, id))
	if res.Code != http.StatusOK {
		t.Fatalf("GetHistory: %d %s", res.Code, res.Body)
	}
	var h checkspb.GetHistoryResponse
	if err := protojson.Unmarshal(res.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	return h.GetUptime()[0]
}
