package appstate

import (
	"context"
	"fmt"
)

// TargetsQuery is the query Targets runs.
const TargetsQuery = targetsQuery

// Entries returns the whole Audit Log, newest first. Tests only: nothing
// in the core may select it unbounded.
func (g *Gate) Entries(ctx context.Context) ([]Entry, error) {
	rows, err := g.db.QueryContext(ctx, `SELECT `+entryColumns+` FROM audit_entry ORDER BY time DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
