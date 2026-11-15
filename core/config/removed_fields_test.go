package config_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/itsKontra/ddash/core/config"
	"github.com/itsKontra/ddash/core/coredb"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
)

// A Release that removes a config field (a secret that moved to another
// Component) must not leave its value in the core DB.
func TestLoadingAConfigDropsWhatItsMessageNoLongerHasFromTheDB(t *testing.T) {
	db, err := coredb.Open(t.Context(), filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := config.New(db)
	removed := func(value string) []byte {
		return protowire.AppendString(protowire.AppendTag(nil, 900, protowire.BytesType), value)
	}
	// As an older Release stored it: field 900 on the config and on a Peer.
	peer := &samplepb.Peer{Name: "nas"}
	peer.ProtoReflect().SetUnknown(removed("old-peer-password"))
	old := &samplepb.SampleConfig{Target: "world", Token: "kept", Peers: []*samplepb.Peer{peer}}
	old.ProtoReflect().SetUnknown(removed("old-api-key"))
	if err := store.Put(t.Context(), "greeter", old); err != nil {
		t.Fatal(err)
	}
	raw := func() []byte {
		var b []byte
		if err := db.QueryRowContext(t.Context(), `SELECT value FROM config WHERE owner = 'greeter'`).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	if !bytes.Contains(raw(), []byte("old-api-key")) {
		t.Fatal("the test's stored config doesn't hold the removed field")
	}

	msg, err := store.Load(t.Context(), "greeter", (&samplepb.SampleConfig{}).ProtoReflect().Descriptor())

	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"old-api-key", "old-peer-password"} {
		if bytes.Contains(raw(), []byte(gone)) {
			t.Errorf("the DB still holds %q after the load", gone)
		}
	}
	got := msg.ProtoReflect()
	fields := got.Descriptor().Fields()
	if got.Get(fields.ByName("target")).String() != "world" || got.Get(fields.ByName("token")).String() != "kept" || got.Get(fields.ByName("peers")).List().Len() != 1 {
		t.Errorf("loaded %v, want the fields the message still has unchanged", msg)
	}
	if !bytes.Contains(raw(), []byte("kept")) || !bytes.Contains(raw(), []byte("nas")) {
		t.Error("the DB lost a field the message still has")
	}
}
