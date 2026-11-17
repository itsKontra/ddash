package checks_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/migrate"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// lockDB holds the write lock of the Component's DB, as a slow commit
// would, until the returned func releases it. A write meanwhile waits for
// SQLite's busy timeout (5 s).
func lockDB(t *testing.T, h *sdktest.Harness) (release func()) {
	t.Helper()
	db, err := migrate.Open(h.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(t.Context(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	released := false
	release = func() {
		if released {
			return
		}
		released = true
		if _, err := conn.ExecContext(context.Background(), `ROLLBACK`); err != nil {
			t.Error(err)
		}
		conn.Close()
		db.Close()
	}
	t.Cleanup(release)
	return release
}

// In real time: SQLite waits for its lock outside the synctest bubble.
func TestGettersAnswerWhileABeatIsBeingRecorded(t *testing.T) {
	dialed, probed := make(chan struct{}, 1), make(chan struct{})
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		select {
		case dialed <- struct{}{}:
		default:
		}
		select {
		case <-probed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		client, server := net.Pipe()
		server.Close()
		return client, nil
	}
	h := sdktest.New(t, checks.DefinitionDialing(dial), sdktest.WithClient[notificationpb.NotificationServiceClient](&fakeNotify{}))
	h.MustStart(&checkspb.ChecksConfig{})
	c := checkspb.NewChecksServiceClient(h.Conn)
	id := create(t, c, tcp("nas:22")).GetId()
	<-dialed // its first beat is probing

	release := lockDB(t, h)
	close(probed)
	time.Sleep(200 * time.Millisecond) // the beat now waits for the DB to record itself

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	resp, err := c.ListChecks(ctx, &checkspb.ListChecksRequest{})
	if err != nil {
		t.Fatalf("ListChecks while a beat is being recorded: %v, want an answer at once", err)
	}
	if ch := resp.GetChecks()[0]; ch.GetLastBeat() != nil {
		t.Errorf("ListChecks shows a beat at %v before it is recorded", ch.GetLastBeat().AsTime())
	}
	if _, err := c.GetHistory(ctx, &checkspb.GetHistoryRequest{Id: id}); err != nil {
		t.Errorf("GetHistory while a beat is being recorded: %v, want an answer at once", err)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("Health while a beat is being recorded: %v, want OK", got)
	}

	release()
	if got := firstBeat(t, c, id).GetState(); got != checkspb.State_STATE_UP {
		t.Errorf("once recorded, the Check is %v, want up", got)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("Health once the beat is recorded: %v, want OK", got)
	}
}
