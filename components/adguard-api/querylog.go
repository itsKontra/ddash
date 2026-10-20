package adguardapi

import (
	"context"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
)

// The query log is read per call, never cached: it is the NONE tier, read
// on mount and after Actions.

const (
	defaultLimit = 100
	maxLimit     = 500
)

// blockedReasons are the filtering reasons that block a query: filter
// lists, safe browsing, parental control and blocked services. Safe search
// rewrites and allow rules don't.
var blockedReasons = []string{"FilteredBlackList", "FilteredSafeBrowsing", "FilteredParental", "FilteredBlockedService"}

func (c *component) ListQueries(ctx context.Context, req *adguardapipb.ListQueriesRequest) (*adguardapipb.ListQueriesResponse, error) {
	ip, err := netip.ParseAddr(req.GetClient())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "client %q is no address", req.GetClient())
	}
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	// Quoted, AdGuard matches the client exactly instead of as a substring.
	q := url.Values{"search": {`"` + ip.String() + `"`}, "limit": {strconv.Itoa(limit)}}
	if req.GetOlderThan() != "" {
		q.Set("older_than", req.GetOlderThan())
	}
	if req.GetBlockedOnly() {
		q["reason"] = blockedReasons
	}
	var data struct {
		Oldest string     `json:"oldest"`
		Data   []logEntry `json:"data"`
	}
	if err := c.api.get(ctx, "/control/querylog", q, &data); err != nil {
		return nil, err
	}
	out := &adguardapipb.ListQueriesResponse{Oldest: data.Oldest}
	for _, e := range data.Data {
		query := e.query()
		// An AdGuard too old to filter by reason sends everything.
		if req.GetBlockedOnly() && !query.GetBlocked() {
			continue
		}
		out.Queries = append(out.Queries, query)
	}
	return out, nil
}

// logEntry is one entry of GET /control/querylog's data.
type logEntry struct {
	Time       string `json:"time"`
	Client     string `json:"client"`
	ClientInfo struct {
		Name string `json:"name"`
	} `json:"client_info"`
	Question struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"question"`
	Reason string `json:"reason"`
	Rules  []struct {
		Text string `json:"text"`
	} `json:"rules"`
	Status    string `json:"status"`
	ElapsedMs string `json:"elapsedMs"`
	Upstream  string `json:"upstream"`
	Cached    bool   `json:"cached"`
}

func (e logEntry) query() *adguardapipb.Query {
	q := &adguardapipb.Query{
		Domain:     e.Question.Name,
		Type:       e.Question.Type,
		Client:     e.Client,
		ClientName: e.ClientInfo.Name,
		Reason:     e.Reason,
		Blocked:    slices.Contains(blockedReasons, e.Reason),
		Status:     e.Status,
		Upstream:   e.Upstream,
		Cached:     e.Cached,
	}
	if t, err := time.Parse(time.RFC3339Nano, e.Time); err == nil {
		q.Time = timestamppb.New(t)
	}
	if len(e.Rules) > 0 {
		q.Rule = e.Rules[0].Text
	}
	if ms, err := strconv.ParseFloat(e.ElapsedMs, 64); err == nil {
		q.ElapsedMs = ms
	}
	return q
}
