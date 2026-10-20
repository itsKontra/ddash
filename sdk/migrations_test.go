package sdk_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/internal/echo/echopb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// notes is an echo that keeps notes in its own table.
type notes struct {
	echo
	env *sdk.Env
}

func (n *notes) Start(ctx context.Context, env *sdk.Env, cfg *echopb.EchoConfig) error {
	n.env = env
	return n.echo.Start(ctx, env, cfg)
}

func (n *notes) AddNote(ctx context.Context, req *echopb.AddNoteRequest) (*echopb.AddNoteResponse, error) {
	if _, err := n.env.DB.ExecContext(ctx, "INSERT INTO note (text) VALUES (?)", req.GetText()); err != nil {
		return nil, err
	}
	return &echopb.AddNoteResponse{}, nil
}

func (n *notes) CountNotes(ctx context.Context, _ *echopb.CountNotesRequest) (*echopb.CountNotesResponse, error) {
	var count int64
	err := n.env.DB.QueryRowContext(ctx, "SELECT count(*) FROM note").Scan(&count)
	return &echopb.CountNotesResponse{Count: count}, err
}

var noteMigrations = fstest.MapFS{
	"migrations/0001_notes.sql": {Data: []byte("CREATE TABLE note (id INTEGER PRIMARY KEY, text TEXT NOT NULL);")},
}

func defineNotes(n *notes) sdk.Definition {
	return sdk.Define(sdk.Spec[*echopb.EchoConfig]{
		Manifest:   []byte(echoManifest),
		Service:    &echopb.EchoService_ServiceDesc,
		Migrations: noteMigrations,
		New:        func() sdk.Component[*echopb.EchoConfig] { return n },
	})
}

func TestMigrationsAreAppliedBeforeStart(t *testing.T) {
	var tableAtStart bool
	n := &notes{}
	n.start = func(_ context.Context, env *sdk.Env, _ *echopb.EchoConfig) error {
		tableAtStart = env.DB.QueryRow("SELECT 1 FROM sqlite_master WHERE name = 'note'").Scan(new(int)) == nil
		return nil
	}
	h := sdktest.New(t, defineNotes(n))

	h.MustStart(&echopb.EchoConfig{})

	if !tableAtStart {
		t.Fatal("the note table didn't exist when Start ran")
	}
}

func TestTheDBOutlivesAProcessRestart(t *testing.T) {
	first := sdktest.New(t, defineNotes(&notes{}))
	first.MustStart(&echopb.EchoConfig{})
	if _, err := echopb.NewEchoServiceClient(first.Conn).AddNote(t.Context(), &echopb.AddNoteRequest{Text: "kept"}); err != nil {
		t.Fatal(err)
	}
	first.Stop()

	second := sdktest.New(t, defineNotes(&notes{}), sdktest.WithDB(first.DBPath))
	second.MustStart(&echopb.EchoConfig{})

	resp, err := echopb.NewEchoServiceClient(second.Conn).CountNotes(t.Context(), &echopb.CountNotesRequest{})
	if err != nil || resp.GetCount() != 1 {
		t.Fatalf("CountNotes = %d, %v; want 1", resp.GetCount(), err)
	}
}

func TestADBFromANewerReleaseIsRefused(t *testing.T) {
	newer := sdktest.New(t, sdk.Define(sdk.Spec[*echopb.EchoConfig]{
		Manifest: []byte(echoManifest),
		Service:  &echopb.EchoService_ServiceDesc,
		Migrations: fstest.MapFS{
			"migrations/0001_notes.sql":  noteMigrations["migrations/0001_notes.sql"],
			"migrations/0002_author.sql": {Data: []byte("ALTER TABLE note ADD COLUMN author TEXT;")},
		},
		New: func() sdk.Component[*echopb.EchoConfig] { return &notes{} },
	}))
	newer.MustStart(&echopb.EchoConfig{})
	newer.Stop()
	started := false
	n := &notes{}
	n.start = func(context.Context, *sdk.Env, *echopb.EchoConfig) error { started = true; return nil }

	older := sdktest.New(t, defineNotes(n), sdktest.WithDB(newer.DBPath))
	err := older.Start(&echopb.EchoConfig{})

	if err == nil || started {
		t.Fatalf("Start err = %v, Component started = %v; want refused before Start", err, started)
	}
	if got := older.Health(); got.State != sdk.Down || !strings.Contains(got.Message, "ahead") {
		t.Errorf("health = %v, want down because the schema is ahead", got)
	}
}

func TestAComponentWithoutMigrationsGetsNoDB(t *testing.T) {
	var gotDB bool
	h := sdktest.New(t, define(&echo{start: func(_ context.Context, env *sdk.Env, _ *echopb.EchoConfig) error {
		gotDB = env.DB != nil
		return nil
	}}))

	h.MustStart(&echopb.EchoConfig{})

	if gotDB {
		t.Error("a Component without migrations got a DB")
	}
}

func TestAFailedMigrationIsDown(t *testing.T) {
	h := sdktest.New(t, sdk.Define(sdk.Spec[*echopb.EchoConfig]{
		Manifest:   []byte(echoManifest),
		Service:    &echopb.EchoService_ServiceDesc,
		Migrations: fstest.MapFS{"migrations/0001_broken.sql": {Data: []byte("NOT SQL")}},
		New:        func() sdk.Component[*echopb.EchoConfig] { return &notes{} },
	}))

	err := h.Start(&echopb.EchoConfig{})

	if err == nil || status.Code(err) == codes.InvalidArgument {
		t.Fatalf("Start err = %v, want a startup error", err)
	}
	if got := h.Health(); got.State != sdk.Down {
		t.Errorf("health = %v, want down", got)
	}
}
