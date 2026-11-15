package apps_test

import (
	"slices"
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
)

// pins are the pinned Apps' ids by their pin, from 1.
func (l *lab) pins() []string {
	l.t.Helper()
	var pinned []*appspb.App
	for _, a := range l.list().GetApps() {
		if a.GetPin() > 0 {
			pinned = append(pinned, a)
		}
	}
	slices.SortFunc(pinned, func(a, b *appspb.App) int { return int(a.GetPin() - b.GetPin()) })
	var ids []string
	for i, a := range pinned {
		if a.GetPin() != int32(i+1) {
			l.t.Errorf("%s's pin = %d, want %d", a.GetId(), a.GetPin(), i+1)
		}
		ids = append(ids, a.GetId())
	}
	return ids
}

// SetPins pins the Apps in its order and unpins the rest; a pin touches
// its App, which then stays as orphaned App settings when its sources go.
func TestTheOperatorPinsAppsInTheirOrder(t *testing.T) {
	l := newLab(t, labOptions{})
	if got := l.pins(); len(got) != 0 {
		t.Fatalf("pins = %v before any, want none", got)
	}
	l.call("SetPins", &appspb.SetPinsRequest{Ids: []string{"proxy:video.home.example", "proxy:grafana.home.example", "proxy:backup.home.example"}}, nil)
	if d := diff([]string{"proxy:video.home.example", "proxy:grafana.home.example", "proxy:backup.home.example"}, l.pins()); d != "" {
		t.Errorf("pins (-want +got):\n%s", d)
	}
	if !l.app("proxy:video.home.example").GetTouched() {
		t.Error("a pinned App isn't touched")
	}

	// Reordered and one unpinned.
	l.call("SetPins", &appspb.SetPinsRequest{Ids: []string{"proxy:backup.home.example", "proxy:video.home.example"}}, nil)
	if d := diff([]string{"proxy:backup.home.example", "proxy:video.home.example"}, l.pins()); d != "" {
		t.Errorf("pins after the reorder (-want +got):\n%s", d)
	}

	// A sync keeps them; a pinned App whose sources go is kept, still pinned.
	l.npm.DeleteProxyHost(34)
	l.unregister("video.home.example")
	l.refresh()
	if a := l.app("proxy:video.home.example"); !a.GetOrphaned() || a.GetPin() != 2 {
		t.Errorf("video = %v, want orphaned App settings, pinned second", a)
	}

	l.call("SetPins", &appspb.SetPinsRequest{}, nil)
	if got := l.pins(); len(got) != 0 {
		t.Errorf("pins = %v after unpinning all, want none", got)
	}
}

func TestBadPinsAreRefused(t *testing.T) {
	l := newLab(t, labOptions{})
	for _, tc := range []struct {
		ids  []string
		code string
	}{
		{[]string{"proxy:video.home.example", "proxy:video.home.example"}, "invalid_argument"},
		{[]string{"proxy:video.home.example", "proxy:nowhere"}, "not_found"},
	} {
		if err := l.try("SetPins", &appspb.SetPinsRequest{Ids: tc.ids}, nil); err == nil || err.code != tc.code {
			t.Errorf("SetPins %v: %v, want %s", tc.ids, err, tc.code)
		}
	}
	if got := l.pins(); len(got) != 0 {
		t.Errorf("pins = %v after refusals, want none", got)
	}
}
