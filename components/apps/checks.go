package apps

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/checks/checkspb"
)

// syncChecks declares every App's Check to checks when the set differs
// from the one checks last accepted: so only on change, plus once after
// Start. Nothing is declared before nginx-config's snapshot was first
// read, since an empty set would stop every Proxy Host's Check. A failed
// declaration is tried again on the next call.
func (c *component) syncChecks(ctx context.Context) {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	c.mu.Lock()
	read, found := c.wasRead, c.found
	c.mu.Unlock()
	if !read {
		return
	}
	all, _, err := c.joined(ctx, found)
	if err != nil {
		c.log.Error("read Apps for their Checks", "err", err)
		return
	}
	set := appChecks(all)
	if c.sent != nil && slices.EqualFunc(set, c.sent, func(a, b *checkspb.AppCheck) bool { return proto.Equal(a, b) }) {
		return
	}
	_, err = c.checks.SetAppChecks(ctx, &checkspb.SetAppChecksRequest{Checks: set})
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.checksErr = status.Convert(err).Message()
		c.log.Warn("declare App Checks", "err", err)
		return
	}
	c.checksErr, c.sent = "", set
}

// The nginx config item, reported to checks so it is notified when
// nginx-config's `nginx -T` read fails and when it recovers.
const (
	nginxItem     = "nginx-config"
	nginxItemName = "nginx config"
	nginxItemPath = "/reverse-proxy"
)

// reportNginx reports the nginx config item to checks when it differs
// from what checks last accepted: so only on change, plus once after
// Start. Nothing is reported before a snapshot was read (nginx-config
// being unavailable is Component health, never notified). A refused
// report is tried again on the next call.
func (c *component) reportNginx(ctx context.Context) {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	c.mu.Lock()
	known, reason := c.nginxKnown, c.nginxDown
	c.mu.Unlock()
	if !known {
		return
	}
	req := &checkspb.ReportStateRequest{Item: nginxItem, Name: nginxItemName, State: checkspb.State_STATE_UP, Path: nginxItemPath}
	if reason != "" {
		req.State, req.Reason = checkspb.State_STATE_DOWN, reason
	}
	if proto.Equal(req, c.reported) {
		return
	}
	if _, err := c.checks.ReportState(ctx, req); err != nil {
		c.log.Warn("report the nginx config item", "err", err)
		return
	}
	c.reported = req
}

// firstLine is s's first non-empty line, trimmed: nginx's stderr ends
// with "configuration file ... test failed" after the line that says why.
func firstLine(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// appChecks is each App's Check, by App id: an HTTP(S) Check of its URL,
// disabled while the App is hidden, naming its Workload.
func appChecks(all []*appspb.App) []*checkspb.AppCheck {
	out := make([]*checkspb.AppCheck, 0, len(all))
	for _, a := range all {
		out = append(out, &checkspb.AppCheck{
			AppId: a.GetId(), Name: a.GetName(), Url: a.GetUrl(), Enabled: !a.GetHidden(),
			Workload: a.GetWorkload().GetRef(),
		})
	}
	slices.SortFunc(out, func(a, b *checkspb.AppCheck) int { return cmp.Compare(a.GetAppId(), b.GetAppId()) })
	return out
}
