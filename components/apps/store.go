package apps

import (
	"context"
	"database/sql"
	"maps"
	"slices"
	"strings"

	"github.com/itsKontra/ddash/components/apps/appspb"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
)

// kinds names each Workload kind in the DB.
var kinds = map[workloadpb.Kind]string{
	workloadpb.Kind_KIND_LXC:      "lxc",
	workloadpb.Kind_KIND_VM:       "vm",
	workloadpb.Kind_KIND_DOCKER:   "docker",
	workloadpb.Kind_KIND_EXTERNAL: "external",
}

// record is an App's record: what apps keeps of an App between syncs.
type record struct {
	id  string
	url string
	// aliases are its further hostnames as last derived.
	aliases []string
	// proxyHostID links it to its Proxy Host; 0 for none.
	proxyHostID int64
	// hostOverride links it to its own Host Override; zero for none.
	hostOverride hostName
	settings     *appspb.AppSettings
	ignores      map[appspb.WarningKind]bool
	// linkOnly: added by hand, never removed by a sync.
	linkOnly bool
	// touched: a setting or an Ignore was ever set.
	touched bool
	// migrated: an overlay or manual App not yet through its first sync.
	migrated bool
}

func (r *record) clone() *record {
	c := *r
	c.aliases = slices.Clone(r.aliases)
	c.settings = cloneSettings(r.settings)
	c.ignores = maps.Clone(r.ignores)
	return &c
}

func cloneSettings(s *appspb.AppSettings) *appspb.AppSettings {
	if s == nil {
		return &appspb.AppSettings{}
	}
	return &appspb.AppSettings{Name: s.GetName(), Icon: s.GetIcon(), Group: s.GetGroup(), Hidden: s.GetHidden(), Workload: s.GetWorkload()}
}

// store is apps' DB: the App records and their Ignores.
type store struct {
	db *sql.DB
}

// querier is a DB or a transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// records returns every App record, by id.
func (s store) records(ctx context.Context) ([]*record, error) {
	return readRecords(ctx, s.db)
}

func readRecords(ctx context.Context, q querier) ([]*record, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, url, aliases, proxy_host_id, host_override_host, host_override_domain, name, icon, group_name, hidden,
		       workload_kind, workload_host, workload_id, link_only, touched, migrated
		FROM app ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*record
	byID := map[string]*record{}
	for rows.Next() {
		r := &record{settings: &appspb.AppSettings{}, ignores: map[appspb.WarningKind]bool{}}
		var aliases, kind, host, id string
		if err := rows.Scan(&r.id, &r.url, &aliases, &r.proxyHostID, &r.hostOverride.host, &r.hostOverride.domain, &r.settings.Name, &r.settings.Icon, &r.settings.Group, &r.settings.Hidden,
			&kind, &host, &id, &r.linkOnly, &r.touched, &r.migrated); err != nil {
			return nil, err
		}
		if aliases != "" {
			r.aliases = strings.Split(aliases, "\n")
		}
		for k, name := range kinds {
			if name == kind {
				r.settings.Workload = &workloadpb.WorkloadRef{Kind: k, Host: host, Id: id}
			}
		}
		out = append(out, r)
		byID[r.id] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	ignores, err := q.QueryContext(ctx, `SELECT app_id, kind FROM app_ignore`)
	if err != nil {
		return nil, err
	}
	defer ignores.Close()
	for ignores.Next() {
		var id, kind string
		if err := ignores.Scan(&id, &kind); err != nil {
			return nil, err
		}
		if r, ok := byID[id]; ok {
			r.ignores[appspb.WarningKind(appspb.WarningKind_value[kind])] = true
		}
	}
	return out, ignores.Err()
}

// save writes the records in put (inserted or replaced, Ignores
// included) and deletes those in drop, in one transaction.
func (s store) save(ctx context.Context, put []*record, drop []string) error {
	if len(put) == 0 && len(drop) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	for _, id := range drop {
		if _, err := tx.ExecContext(ctx, `DELETE FROM app WHERE id = ?`, id); err != nil {
			return err
		}
	}
	for _, r := range put {
		w := r.settings.GetWorkload()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO app (id, url, aliases, proxy_host_id, host_override_host, host_override_domain, name, icon, group_name, hidden,
			                 workload_kind, workload_host, workload_id, link_only, touched, migrated)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET
				url = excluded.url, aliases = excluded.aliases, proxy_host_id = excluded.proxy_host_id,
				host_override_host = excluded.host_override_host, host_override_domain = excluded.host_override_domain,
				name = excluded.name, icon = excluded.icon, group_name = excluded.group_name, hidden = excluded.hidden,
				workload_kind = excluded.workload_kind, workload_host = excluded.workload_host, workload_id = excluded.workload_id,
				link_only = excluded.link_only, touched = excluded.touched, migrated = excluded.migrated`,
			r.id, r.url, strings.Join(r.aliases, "\n"), r.proxyHostID, r.hostOverride.host, r.hostOverride.domain,
			r.settings.GetName(), r.settings.GetIcon(), r.settings.GetGroup(), r.settings.GetHidden(),
			kinds[w.GetKind()], w.GetHost(), w.GetId(), r.linkOnly, r.touched, r.migrated); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_ignore WHERE app_id = ?`, r.id); err != nil {
			return err
		}
		for k, on := range r.ignores {
			if !on {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_ignore (app_id, kind) VALUES (?, ?)`, r.id, k.String()); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
