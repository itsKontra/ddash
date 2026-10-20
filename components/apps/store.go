package apps

import (
	"context"
	"database/sql"

	"github.com/itsKontra/ddash/components/apps/appspb"
)

// store is apps' DB: overlays and manual Apps.
type store struct {
	db *sql.DB
}

// overlays returns every overlay by server_name.
func (s store) overlays(ctx context.Context) (map[string]*appspb.Overlay, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT server_name, name, icon, group_name, hidden FROM overlay`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*appspb.Overlay{}
	for rows.Next() {
		o := &appspb.Overlay{}
		if err := rows.Scan(&o.ServerName, &o.Name, &o.Icon, &o.Group, &o.Hidden); err != nil {
			return nil, err
		}
		out[o.GetServerName()] = o
	}
	return out, rows.Err()
}

func (s store) hasOverlay(ctx context.Context, serverName string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM overlay WHERE server_name = ?`, serverName).Scan(&n)
	return n > 0, err
}

func (s store) putOverlay(ctx context.Context, o *appspb.Overlay) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO overlay (server_name, name, icon, group_name, hidden) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (server_name) DO UPDATE SET
			name = excluded.name, icon = excluded.icon, group_name = excluded.group_name, hidden = excluded.hidden`,
		o.GetServerName(), o.GetName(), o.GetIcon(), o.GetGroup(), o.GetHidden())
	return err
}

func (s store) moveOverlay(ctx context.Context, from, to string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE overlay SET server_name = ? WHERE server_name = ?`, to, from)
	return err
}

func (s store) deleteOverlay(ctx context.Context, serverName string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM overlay WHERE server_name = ?`, serverName)
	return err
}

func (s store) insertManual(ctx context.Context, a *appspb.App) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO manual_app (id, name, url, icon, group_name) VALUES (?, ?, ?, ?, ?)`,
		a.GetId(), a.GetName(), a.GetUrl(), a.GetIcon(), a.GetGroup())
	return err
}

// updateManual reports whether the manual App existed.
func (s store) updateManual(ctx context.Context, a *appspb.App) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE manual_app SET name = ?, url = ?, icon = ?, group_name = ? WHERE id = ?`,
		a.GetName(), a.GetUrl(), a.GetIcon(), a.GetGroup(), a.GetId())
	return affected(res, err)
}

// deleteManual reports whether the manual App existed.
func (s store) deleteManual(ctx context.Context, id string) (bool, error) {
	return affected(s.db.ExecContext(ctx, `DELETE FROM manual_app WHERE id = ?`, id))
}

func affected(res sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s store) manualApps(ctx context.Context) ([]*appspb.App, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, url, icon, group_name FROM manual_app`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*appspb.App
	for rows.Next() {
		a := &appspb.App{Manual: true}
		if err := rows.Scan(&a.Id, &a.Name, &a.Url, &a.Icon, &a.Group); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
