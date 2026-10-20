package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

// A session ends after IdleTimeout without an authenticated request, and
// MaxSessionAge after sign-in however active it is.
const (
	IdleTimeout   = 7 * 24 * time.Hour
	MaxSessionAge = 30 * 24 * time.Hour
)

// SweepInterval is how often expired sessions are deleted.
const SweepInterval = time.Hour

// sessions are the Operator sessions in the core DB's session table.
type sessions struct{ db *sql.DB }

// tokenID is how the table keys a session: a stolen DB copy holds no
// usable cookie.
func tokenID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// create starts a session for the Operator and returns its cookie token.
func (s sessions) create(ctx context.Context, op Actor) (string, error) {
	token := rand.Text()
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO session (id, username, sub, created, last_seen) VALUES (?, ?, ?, ?, ?)`,
		tokenID(token), op.Name, op.Sub, now, now)
	return token, err
}

// touch counts a request on token's session as activity and returns its
// Operator, or false when there is no such live session.
func (s sessions) touch(ctx context.Context, token string) (Actor, bool, error) {
	now := time.Now()
	op := Actor{Kind: Operator}
	err := s.db.QueryRowContext(ctx, `
		UPDATE session SET last_seen = ?
		WHERE id = ? AND last_seen > ? AND created > ?
		RETURNING username, sub`,
		now.UnixMilli(), tokenID(token), now.Add(-IdleTimeout).UnixMilli(), now.Add(-MaxSessionAge).UnixMilli(),
	).Scan(&op.Name, &op.Sub)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, false, nil
	}
	return op, err == nil, err
}

// end deletes token's session and returns its Operator, or false when there
// was none.
func (s sessions) end(ctx context.Context, token string) (Actor, bool, error) {
	op := Actor{Kind: Operator}
	err := s.db.QueryRowContext(ctx, `DELETE FROM session WHERE id = ? RETURNING username, sub`, tokenID(token)).
		Scan(&op.Name, &op.Sub)
	if errors.Is(err, sql.ErrNoRows) {
		return Actor{}, false, nil
	}
	return op, err == nil, err
}

// sweep deletes the expired sessions at once and then every SweepInterval,
// until ctx ends.
func (s sessions) sweep(ctx context.Context) {
	ticker := time.NewTicker(SweepInterval)
	defer ticker.Stop()
	for {
		now := time.Now()
		_, _ = s.db.ExecContext(ctx, `DELETE FROM session WHERE last_seen <= ? OR created <= ?`, // retried next time
			now.Add(-IdleTimeout).UnixMilli(), now.Add(-MaxSessionAge).UnixMilli())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
