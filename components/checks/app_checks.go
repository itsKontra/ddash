package checks

import (
	"context"
	"net/url"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/checks/checkspb"
)

// appCheck is one App's Check as apps declares it.
type appCheck struct {
	appID, name, url string
	enabled          bool
}

// parseAppChecks checks apps' set: every App once, each with an http or
// https URL.
func parseAppChecks(in []*checkspb.AppCheck) ([]appCheck, error) {
	out := make([]appCheck, 0, len(in))
	seen := map[string]bool{}
	for _, a := range in {
		ac := appCheck{
			appID:   strings.TrimSpace(a.GetAppId()),
			name:    strings.TrimSpace(a.GetName()),
			url:     strings.TrimSpace(a.GetUrl()),
			enabled: a.GetEnabled(),
		}
		if ac.appID == "" {
			return nil, status.Error(codes.InvalidArgument, "every App Check needs an app_id")
		}
		if seen[ac.appID] {
			return nil, status.Errorf(codes.InvalidArgument, "App %s is listed twice", ac.appID)
		}
		seen[ac.appID] = true
		if u, err := url.Parse(ac.url); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, status.Errorf(codes.InvalidArgument, "App %s: url must be an http or https URL", ac.appID)
		}
		out = append(out, ac)
	}
	return out, nil
}

func (c *component) SetAppChecks(ctx context.Context, req *checkspb.SetAppChecksRequest) (*checkspb.SetAppChecksResponse, error) {
	want, err := parseAppChecks(req.GetChecks())
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	byApp := map[string]*check{}
	for _, ch := range c.checks {
		if ch.appID != "" {
			byApp[ch.appID] = ch
		}
	}
	at := now()
	// next holds each changed App Check as it will be: a copy of the one in
	// memory, or a new one without an id.
	var next []*check
	listed := map[string]bool{}
	for _, a := range want {
		listed[a.appID] = true
		ch := byApp[a.appID]
		if ch == nil {
			next = append(next, &check{
				appID:      a.appID,
				appEnabled: a.enabled,
				spec:       spec{name: a.name, typ: checkspb.CheckType_CHECK_TYPE_HTTP, target: a.url},
				standing:   standing{state: checkspb.State_STATE_PENDING, since: at},
			})
			continue
		}
		n := *ch
		// Back in the set, a gone Check resumes from its last state.
		n.spec.name, n.appEnabled, n.goneAt = a.name, a.enabled, time.Time{}
		if n.spec.target != a.url {
			// Another URL is another target: start over, as a manual Check
			// does on a new target.
			n.spec.target = a.url
			n.standing = standing{state: checkspb.State_STATE_PENDING, since: at}
		}
		if n.spec != ch.spec || n.appEnabled != ch.appEnabled || !n.goneAt.Equal(ch.goneAt) {
			next = append(next, &n)
		}
	}
	for id, ch := range byApp {
		if !listed[id] && ch.goneAt.IsZero() {
			n := *ch
			n.goneAt = at
			next = append(next, &n)
		}
	}
	if len(next) == 0 {
		return &checkspb.SetAppChecksResponse{}, nil
	}
	if err := c.db.saveAppChecks(ctx, next); err != nil {
		return nil, err
	}

	for _, n := range next {
		ch := c.checks[n.id]
		if ch == nil {
			ch = n
			c.checks[ch.id] = ch
			if ch.probed() {
				c.probeEvery(ch)
			}
			continue
		}
		was, retarget := ch.probed(), ch.spec.target != n.spec.target
		ch.spec, ch.appEnabled, ch.goneAt, ch.standing = n.spec, n.appEnabled, n.goneAt, n.standing
		if was && (retarget || !ch.probed()) {
			ch.stopProbing()
		}
		if ch.probed() && (retarget || !was) {
			c.probeEvery(ch)
		}
	}
	return &checkspb.SetAppChecksResponse{}, nil
}
