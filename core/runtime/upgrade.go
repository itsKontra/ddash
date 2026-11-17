package runtime

// One-off upgrades of what an earlier Release left in the core DB. A core
// DB migration that sees the old state adds a row to pending_upgrade; the
// step runs here, on the first start with a catalogue, and its row goes.

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
)

// upgrades are the steps a pending_upgrade row names.
var upgrades = map[string]func(r *Runtime, ctx context.Context) error{
	"channel-plugins": (*Runtime).installChannelPlugins,
}

// upgrade runs every pending upgrade step and reports whether none is left
// pending. It runs before dropRetired, which deletes the data of the
// Components the catalogue no longer ships that a step may still read. A
// step that fails is logged and stays pending, to run again on the next
// start.
func (r *Runtime) upgrade(ctx context.Context) (done bool) {
	rows, err := r.opts.DB.QueryContext(ctx, `SELECT name FROM pending_upgrade ORDER BY name`)
	if err != nil {
		r.log.Error("list the pending upgrades", "err", err)
		return false
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			r.log.Error("list the pending upgrades", "err", err)
			return false
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		r.log.Error("list the pending upgrades", "err", err)
		return false
	}
	done = true
	for _, name := range names {
		step, ok := upgrades[name]
		if !ok {
			// A newer Release's step, kept for when it runs again.
			continue
		}
		if err := step(r, ctx); err != nil {
			r.log.Error("upgrade", "step", name, "err", err)
			done = false
			continue
		}
		if _, err := r.opts.DB.ExecContext(ctx, `DELETE FROM pending_upgrade WHERE name = ?`, name); err != nil {
			r.log.Error("upgrade", "step", name, "err", err)
			done = false
		}
	}
	return done
}

// formerChannels are the Notification channels up to v0.0.3, when the
// notify Component sent every Notification to ntfy-api and telegram-api
// and kept each one's filter in its own config. The field numbers are
// v0.0.3's (ntfy_api.proto, telegram_api.proto and notify.proto), frozen
// here: the core has no compiled-in types of a Component's config, and
// notify is gone.
var formerChannels = []struct {
	plugin, component string
	// destination are the fields of the channel's config that all had to
	// be set for it to deliver: ntfy's topic; Telegram's bot token and
	// chat ID.
	destination []protowire.Number
	// filter are notify's switches for the channel's Checks, Image
	// updates and Action outcomes.
	filter [3]protowire.Number
}{
	{"ntfy", "ntfy-api", []protowire.Number{2}, [3]protowire.Number{1, 2, 3}},
	{"telegram", "telegram-api", []protowire.Number{2, 3}, [3]protowire.Number{4, 5, 6}},
}

// channelFilter are the filter's fields in every channel's config since
// (components/internal/channel): checks, image_updates, action_outcomes.
var channelFilter = [3]protowire.Number{4, 5, 6}

// installChannelPlugins installs the Plugin of every channel that was set
// up (its Component's config names a destination) and isn't installed,
// with the filter notify kept for it moved into the channel's own config,
// so its Notifications keep arriving after the update. A channel that
// wasn't set up stays uninstalled.
func (r *Runtime) installChannelPlugins(ctx context.Context) error {
	notify, _, err := r.opts.Config.Raw(ctx, "notify")
	if err != nil {
		return err
	}
	for _, ch := range formerChannels {
		cfg, ok, err := r.opts.Config.Raw(ctx, ch.component)
		if err != nil {
			return err
		}
		if !ok || !allSet(cfg, ch.destination) {
			continue
		}
		r.mu.Lock()
		_, installed := r.installed[ch.plugin]
		_, shipped := r.cat.plugin(ch.plugin)
		r.mu.Unlock()
		if installed || !shipped {
			continue
		}
		moved := cfg
		for i, f := range ch.filter {
			// Only a switch turned off differs from the default, on.
			if on, set := boolField(notify, f); set && !on {
				moved = protowire.AppendTag(moved, channelFilter[i], protowire.VarintType)
				moved = protowire.AppendVarint(moved, 0)
			}
		}
		if len(moved) != len(cfg) {
			if err := r.opts.Config.PutRaw(ctx, ch.component, moved); err != nil {
				return err
			}
		}
		if _, err := r.Install(ctx, ch.plugin); err != nil {
			return fmt.Errorf("install %s: %w", ch.plugin, err)
		}
		r.log.Info("installed the Plugin of a Notification channel that was set up", "plugin", ch.plugin, "component", ch.component)
	}
	return nil
}

// allSet reports whether every one of fields is set in msg, a message in
// binary proto encoding, to a non-empty value.
func allSet(msg []byte, fields []protowire.Number) bool {
	for _, f := range fields {
		v, typ, ok := last(msg, f)
		if !ok || typ != protowire.BytesType || len(v) == 0 {
			return false
		}
	}
	return true
}

// boolField returns the bool field f of msg, a message in binary proto
// encoding, and whether it is set.
func boolField(msg []byte, f protowire.Number) (on, set bool) {
	v, typ, ok := last(msg, f)
	if !ok || typ != protowire.VarintType {
		return false, false
	}
	n, _ := protowire.ConsumeVarint(v)
	return n != 0, true
}

// last returns the last occurrence of field f in msg, as protobuf reads
// it: the value's bytes (a varint's own encoding, a string's contents).
// A message that doesn't parse has no fields.
func last(msg []byte, f protowire.Number) (value []byte, typ protowire.Type, ok bool) {
	for len(msg) > 0 {
		num, t, n := protowire.ConsumeTag(msg)
		if n < 0 {
			return nil, 0, false
		}
		msg = msg[n:]
		m := protowire.ConsumeFieldValue(num, t, msg)
		if m < 0 {
			return nil, 0, false
		}
		if num == f {
			value, typ, ok = msg[:m], t, true
			if t == protowire.BytesType {
				value, _ = protowire.ConsumeBytes(msg[:m])
			}
		}
		msg = msg[m:]
	}
	return value, typ, ok
}
