package sdk

import (
	"database/sql"
	"log/slog"

	"google.golang.org/grpc"
)

// Env is what a Component gets at Start besides its config.
type Env struct {
	// Name is the Component's name.
	Name string
	// PublicURL is where the Operator reaches ddash, e.g.
	// "https://ddash.home.example". Notification links are built on it.
	PublicURL string
	// DB is the Component's own SQLite file, already migrated. Nil for a
	// Component without migrations.
	DB *sql.DB
	// Log is the Component's logger.
	Log *slog.Logger

	host    grpc.ClientConnInterface
	clients []any
}
