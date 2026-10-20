package sdk

import (
	"context"
	"io"
	"log/slog"

	"github.com/hashicorp/go-hclog"
)

// NewLogger returns a slog.Logger that writes hclog JSON lines to w, named
// after the Component. go-plugin's host side parses those lines from the
// Component's stderr and logs them, prefixed with the name. Debug is dropped.
func NewLogger(name string, w io.Writer) *slog.Logger {
	h := hclog.New(&hclog.LoggerOptions{
		Name:       name,
		Level:      hclog.Info,
		Output:     w,
		JSONFormat: true,
	})
	return slog.New(&hclogHandler{log: h})
}

// hclogHandler forwards slog records to an hclog.Logger.
type hclogHandler struct {
	log   hclog.Logger
	attrs []any
	group string
}

func (h *hclogHandler) Enabled(_ context.Context, l slog.Level) bool {
	return h.log.GetLevel() <= hclogLevel(l)
}

func (h *hclogHandler) Handle(_ context.Context, r slog.Record) error {
	args := append([]any(nil), h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		args = append(args, h.flatten(a)...)
		return true
	})
	h.log.Log(hclogLevel(r.Level), r.Message, args...)
	return nil
}

func (h *hclogHandler) WithAttrs(as []slog.Attr) slog.Handler {
	n := *h
	n.attrs = append([]any(nil), h.attrs...)
	for _, a := range as {
		n.attrs = append(n.attrs, h.flatten(a)...)
	}
	return &n
}

func (h *hclogHandler) WithGroup(name string) slog.Handler {
	n := *h
	n.group = h.prefixed(name)
	return &n
}

func (h *hclogHandler) prefixed(key string) string {
	if h.group == "" {
		return key
	}
	return h.group + "." + key
}

// flatten turns an attr into hclog key/value pairs, dotting group keys.
func (h *hclogHandler) flatten(a slog.Attr) []any {
	a.Value = a.Value.Resolve()
	if a.Value.Kind() != slog.KindGroup {
		v := a.Value.Any()
		if err, ok := v.(error); ok {
			v = err.Error()
		}
		return []any{h.prefixed(a.Key), v}
	}
	inner := &hclogHandler{group: h.prefixed(a.Key)}
	if a.Key == "" {
		inner.group = h.group
	}
	var out []any
	for _, ga := range a.Value.Group() {
		out = append(out, inner.flatten(ga)...)
	}
	return out
}

func hclogLevel(l slog.Level) hclog.Level {
	switch {
	case l >= slog.LevelError:
		return hclog.Error
	case l >= slog.LevelWarn:
		return hclog.Warn
	case l >= slog.LevelInfo:
		return hclog.Info
	default:
		return hclog.Debug
	}
}
