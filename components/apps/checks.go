package apps

import (
	"cmp"
	"context"
	"slices"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/checks/checkspb"
)

// syncChecks declares every App's Check to checks when the set differs
// from the one checks last accepted: so only on change, plus once after
// Start. Orphaned App settings have none. Nothing is declared before the
// first good read of the Proxy Hosts since Start, since a set without
// them would stop their Checks. A failed declaration is tried again on
// the next call. The caller holds c.turn.
func (c *component) syncChecks(ctx context.Context) {
	if c.view().hosts == nil {
		return
	}
	all, err := c.apps(ctx)
	if err != nil {
		c.log.Error("read Apps for their Checks", "err", err)
		return
	}
	set := appChecks(all)
	c.mu.Lock()
	sent := c.sent
	c.mu.Unlock()
	if sent != nil && slices.EqualFunc(set, sent, func(a, b *checkspb.AppCheck) bool { return proto.Equal(a, b) }) {
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

// appChecks is each App's Check, by App id: an HTTP(S) Check of its URL,
// disabled while the App is hidden, naming its Workload. Orphaned App
// settings have none.
func appChecks(all []*appspb.App) []*checkspb.AppCheck {
	out := make([]*checkspb.AppCheck, 0, len(all))
	for _, a := range all {
		if a.GetOrphaned() {
			continue
		}
		out = append(out, &checkspb.AppCheck{
			AppId: a.GetId(), Name: a.GetName(), Url: a.GetUrl(), Enabled: !a.GetHidden(),
			Workload: a.GetWorkload().GetRef(),
		})
	}
	slices.SortFunc(out, func(a, b *checkspb.AppCheck) int { return cmp.Compare(a.GetAppId(), b.GetAppId()) })
	return out
}
