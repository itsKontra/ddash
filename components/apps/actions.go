package apps

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps/appspb"
)

// The ddash-only Actions: they change App records alone, take their turn
// with the syncs, and take effect at once (App Checks included), with no
// Plan and no read of the sources.

// iconName is a homarr-labs/dashboard-icons name, e.g. "home-assistant".
var iconName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// change runs edit on the App id, as derived now, and its record; edit
// returns the records to put and the ids to drop. It then redeclares the
// App Checks.
func (c *component) change(ctx context.Context, id string, edit func(a *appspb.App, r *record, recs []*record) (changes, error)) error {
	if err := c.waitSynced(ctx); err != nil {
		return err
	}
	c.turn.Lock()
	defer c.turn.Unlock()
	recs, err := c.store.records(ctx)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(recs, func(r *record) bool { return r.id == id })
	if i < 0 {
		return status.Errorf(codes.NotFound, "no App has the id %q", id)
	}
	r := recs[i].clone()
	ch, err := edit(derive([]*record{r}, c.view())[0], r, recs)
	if err != nil {
		return err
	}
	if err := c.store.save(ctx, ch.put, ch.drop); err != nil {
		return err
	}
	c.syncChecks(ctx)
	return nil
}

func (c *component) SetSettings(ctx context.Context, req *appspb.SetSettingsRequest) (*appspb.SetSettingsResponse, error) {
	in := req.GetSettings()
	s := &appspb.AppSettings{
		Name:   strings.TrimSpace(in.GetName()),
		Icon:   strings.ToLower(strings.TrimSpace(in.GetIcon())),
		Group:  strings.TrimSpace(in.GetGroup()),
		Hidden: in.GetHidden(),
	}
	if err := checkIcon(s.GetIcon()); err != nil {
		return nil, err
	}
	w, err := checkWorkloadRef(in.GetWorkload())
	if err != nil {
		return nil, err
	}
	s.Workload = w
	err = c.change(ctx, req.GetId(), func(_ *appspb.App, r *record, _ []*record) (changes, error) {
		r.settings = s
		r.touched = r.touched || s.GetName() != "" || s.GetIcon() != "" || s.GetGroup() != "" || s.GetHidden() || s.GetWorkload() != nil
		return changes{put: []*record{r}}, nil
	})
	if err != nil {
		return nil, err
	}
	return &appspb.SetSettingsResponse{}, nil
}

func (c *component) IgnoreWarning(ctx context.Context, req *appspb.IgnoreWarningRequest) (*appspb.IgnoreWarningResponse, error) {
	if err := c.setIgnored(ctx, req.GetId(), req.GetKind(), true); err != nil {
		return nil, err
	}
	return &appspb.IgnoreWarningResponse{}, nil
}

func (c *component) UnignoreWarning(ctx context.Context, req *appspb.UnignoreWarningRequest) (*appspb.UnignoreWarningResponse, error) {
	if err := c.setIgnored(ctx, req.GetId(), req.GetKind(), false); err != nil {
		return nil, err
	}
	return &appspb.UnignoreWarningResponse{}, nil
}

// setIgnored Ignores a kind of Warning on an App, or ends that Ignore. An
// Ignore touches the App.
func (c *component) setIgnored(ctx context.Context, id string, kind appspb.WarningKind, on bool) error {
	if _, known := appspb.WarningKind_name[int32(kind)]; !known || kind == appspb.WarningKind_WARNING_KIND_UNSPECIFIED {
		return status.Errorf(codes.InvalidArgument, "unknown Warning kind %d", kind)
	}
	return c.change(ctx, id, func(_ *appspb.App, r *record, _ []*record) (changes, error) {
		if r.ignores == nil {
			r.ignores = map[appspb.WarningKind]bool{}
		}
		if on {
			r.ignores[kind], r.touched = true, true
		} else {
			delete(r.ignores, kind)
		}
		return changes{put: []*record{r}}, nil
	})
}

func (c *component) Reattach(ctx context.Context, req *appspb.ReattachRequest) (*appspb.ReattachResponse, error) {
	if req.GetTo() == req.GetId() {
		return nil, status.Error(codes.InvalidArgument, "orphaned App settings can't be reattached to themselves")
	}
	err := c.change(ctx, req.GetId(), func(a *appspb.App, r *record, recs []*record) (changes, error) {
		if !a.GetOrphaned() {
			return changes{}, status.Errorf(codes.FailedPrecondition, "%s isn't orphaned App settings", a.GetName())
		}
		i := slices.IndexFunc(recs, func(r *record) bool { return r.id == req.GetTo() })
		if i < 0 {
			return changes{}, status.Errorf(codes.NotFound, "no App has the id %q", req.GetTo())
		}
		to := recs[i]
		target := derive([]*record{to}, c.view())[0]
		switch {
		case target.GetOrphaned():
			return changes{}, status.Errorf(codes.FailedPrecondition, "%s is orphaned App settings too", target.GetName())
		case to.touched:
			return changes{}, status.Errorf(codes.FailedPrecondition, "%s has settings of its own", target.GetName())
		}
		// The orphan's id, settings and Ignores; the other App's sources.
		r.url, r.aliases, r.proxyHostID, r.hostOverride, r.linkOnly = to.url, to.aliases, to.proxyHostID, to.hostOverride, to.linkOnly
		return changes{put: []*record{r}, drop: []string{to.id}}, nil
	})
	if err != nil {
		return nil, err
	}
	return &appspb.ReattachResponse{}, nil
}

func (c *component) Merge(ctx context.Context, req *appspb.MergeRequest) (*appspb.MergeResponse, error) {
	if req.GetFrom() == req.GetId() {
		return nil, status.Error(codes.InvalidArgument, "an App can't be merged into itself")
	}
	err := c.change(ctx, req.GetId(), func(a *appspb.App, r *record, recs []*record) (changes, error) {
		i := slices.IndexFunc(recs, func(r *record) bool { return r.id == req.GetFrom() })
		if i < 0 {
			return changes{}, status.Errorf(codes.NotFound, "no App has the id %q", req.GetFrom())
		}
		from := recs[i]
		other := derive([]*record{from}, c.view())[0]
		shared := slices.ContainsFunc(appHostnames(a), func(n string) bool { return slices.Contains(appHostnames(other), n) })
		switch {
		case a.GetOrphaned() || other.GetOrphaned():
			return changes{}, status.Error(codes.FailedPrecondition, "orphaned App settings are reattached, not merged")
		case !shared:
			return changes{}, status.Errorf(codes.FailedPrecondition, "%s and %s share no hostname", a.GetName(), other.GetName())
		case r.proxyHostID != 0 && from.proxyHostID != 0:
			return changes{}, status.Errorf(codes.FailedPrecondition, "%s and %s each have a Proxy Host: delete one of them", a.GetName(), other.GetName())
		case !r.hostOverride.isZero() && !from.hostOverride.isZero():
			return changes{}, status.Errorf(codes.FailedPrecondition, "%s and %s each have a Host Override: delete one of them", a.GetName(), other.GetName())
		}
		// The App's id, URL, settings and Ignores; both Apps' sources.
		if r.proxyHostID == 0 {
			r.proxyHostID = from.proxyHostID
		}
		if r.hostOverride.isZero() {
			r.hostOverride = from.hostOverride
		}
		r.linkOnly = r.linkOnly && from.linkOnly
		r.aliases = mergedAliases(r, from)
		return changes{put: []*record{r}, drop: []string{from.id}}, nil
	})
	if err != nil {
		return nil, err
	}
	return &appspb.MergeResponse{}, nil
}

// appHostnames are an App's URL's hostname and its aliases, canonical.
func appHostnames(a *appspb.App) []string {
	return append([]string{hostname(a.GetUrl())}, canonNames(a.GetAliases())...)
}

// mergedAliases are both records' hostnames but r's URL's, until the next
// sync derives them from the sources.
func mergedAliases(r, from *record) []string {
	var out []string
	for _, n := range append(append([]string{}, r.aliases...), append([]string{hostname(from.url)}, from.aliases...)...) {
		if c := canonName(n); c != hostname(r.url) && !slices.ContainsFunc(out, func(x string) bool { return canonName(x) == c }) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

func (c *component) DeleteOrphan(ctx context.Context, req *appspb.DeleteOrphanRequest) (*appspb.DeleteOrphanResponse, error) {
	err := c.change(ctx, req.GetId(), func(a *appspb.App, r *record, _ []*record) (changes, error) {
		if !a.GetOrphaned() {
			return changes{}, status.Errorf(codes.FailedPrecondition, "%s isn't orphaned App settings", a.GetName())
		}
		return changes{drop: []string{r.id}}, nil
	})
	if err != nil {
		return nil, err
	}
	return &appspb.DeleteOrphanResponse{}, nil
}

func (c *component) PromoteAlias(ctx context.Context, req *appspb.PromoteAliasRequest) (*appspb.PromoteAliasResponse, error) {
	alias := canonName(strings.TrimSpace(req.GetAlias()))
	err := c.change(ctx, req.GetId(), func(a *appspb.App, r *record, _ []*record) (changes, error) {
		i := slices.IndexFunc(a.GetAliases(), func(n string) bool { return canonName(n) == alias })
		if i < 0 {
			return changes{}, status.Errorf(codes.InvalidArgument, "%s isn't one of %s's aliases", req.GetAlias(), a.GetName())
		}
		old := hostname(r.url)
		r.url = withHostname(r.url, a.GetAliases()[i])
		// Until the next sync derives them: the URL's old hostname
		// stays an alias where its source still has it.
		r.aliases = slices.DeleteFunc(slices.Clone(a.GetAliases()), func(n string) bool { return canonName(n) == alias })
		if a.GetProxyHost() == nil || slices.ContainsFunc(a.GetProxyHost().GetDomainNames(), func(n string) bool { return canonName(n) == old }) {
			r.aliases = append(r.aliases, old)
			slices.Sort(r.aliases)
		}
		// The name derived from the old URL stays the App's.
		r.settings.Name = a.GetName()
		r.touched = true
		return changes{put: []*record{r}}, nil
	})
	if err != nil {
		return nil, err
	}
	return &appspb.PromoteAliasResponse{}, nil
}

func checkIcon(icon string) error {
	if icon != "" && !iconName.MatchString(icon) {
		return status.Error(codes.InvalidArgument, "icon must be a dashboard-icons name, e.g. jellyfin or home-assistant")
	}
	return nil
}
