package dockeragentapi

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"time"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/sdk/migrate"
)

// Pairing is a paired Docker Agent as docker-agent-api keeps it: the
// host's name, the Agent's URL, the token both keep and the SHA-256 of
// the Agent's certificate.
type Pairing struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint"`
}

// KeepPairings makes the DB at dbPath hold exactly pairings, made by
// another ddash, as its paired Agents: docker-agent-api started on it
// reads those Agents as that ddash does, without pairing (make smoke reads
// a lab's Agents so). Its errors never hold a token.
func KeepPairings(ctx context.Context, dbPath string, pairings []Pairing) error {
	var errs []error
	for i, p := range pairings {
		errs = append(errs, p.check(i))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	db, err := migrate.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	if err := migrate.Apply(ctx, db, sub); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent`); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, p := range pairings {
		_, err := tx.ExecContext(ctx, `INSERT INTO agent (name, url, token, fingerprint, added, paired) VALUES (?, ?, ?, ?, ?, ?)`,
			p.Name, strings.TrimRight(p.URL, "/"), p.Token, p.Fingerprint, now, now)
		if migrate.IsTaken(err) {
			return fmt.Errorf("two Agents are named %s", p.Name)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// check says what is wrong with the i-th pairing, naming it but never
// its token.
func (p Pairing) check(i int) error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("agent %d (%s): %s", i+1, p.Name, fmt.Sprintf(format, args...)))
	}
	if !validName.MatchString(p.Name) {
		bad("name is lower-case letters, digits and -, like media")
	}
	if u, err := url.Parse(p.URL); err != nil || u.Scheme != "https" || u.Hostname() == "" {
		bad("url must be the Agent's https URL, like https://198.18.2.10:%d", agent.DockerPort)
	}
	if p.Token == "" {
		bad("token is required")
	}
	if b, err := hex.DecodeString(p.Fingerprint); err != nil || len(b) != 32 {
		bad("fingerprint must be the certificate's SHA-256 in hex, 64 characters")
	}
	return errors.Join(errs...)
}
