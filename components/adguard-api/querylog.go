package adguardapi

import (
	"context"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
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
	// A text or type search reads at most searchLines of the client's log
	// lines and stops at searchMatches matches.
	searchLines   = 2000
	searchMatches = 50
)

// blockedReasons are the filtering reasons that block a query: filter
// lists, safe browsing, parental control and blocked services. Safe search
// rewrites and allow rules don't.
var blockedReasons = []string{"FilteredBlackList", "FilteredSafeBrowsing", "FilteredParental", "FilteredBlockedService"}

// allowedReasons are AdGuard's other reasons (v0.107.79): it refuses a
// name it doesn't know.
var allowedReasons = []string{
	"NotFilteredNotFound", "NotFilteredWhiteList", "NotFilteredError", "FilteredInvalid",
	"FilteredSafeSearch", "Rewrite", "RewriteEtcHosts", "RewriteRule",
}

// ListQueries asks AdGuard for the client's log lines in the mode, which
// it filters itself, with offset=0 so it reads past its 50,000-line scan
// cap until it has limit of them. Without text or type that one page is
// the answer. With them, ddash matches the lines itself over an older_than
// loop, until it has the matches, read its budget of lines or the log
// ended. The cursor is then the last line read, so a resumed search reads
// on from there.
func (c *component) ListQueries(ctx context.Context, req *adguardapipb.ListQueriesRequest) (*adguardapipb.ListQueriesResponse, error) {
	ip, err := netip.ParseAddr(req.GetClient())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "client %q is no address", req.GetClient())
	}
	f := filter{mode: req.GetMode(), words: strings.Fields(strings.ToLower(req.GetText())), qtype: req.GetType()}
	var reasons []string
	switch f.mode {
	case adguardapipb.QueryMode_QUERY_MODE_UNSPECIFIED:
	case adguardapipb.QueryMode_QUERY_MODE_BLOCKED:
		reasons = blockedReasons
	case adguardapipb.QueryMode_QUERY_MODE_ALLOWED:
		reasons = allowedReasons
	default:
		return nil, status.Errorf(codes.InvalidArgument, "mode %d is unknown", f.mode)
	}
	want := int(req.GetLimit())
	budget := searchLines
	if f.searches() {
		if want <= 0 || want > searchMatches {
			want = searchMatches
		}
	} else {
		if want <= 0 {
			want = defaultLimit
		}
		want = min(want, maxLimit)
		budget = want
	}

	var cfg struct {
		AnonymizeClientIP bool `json:"anonymize_client_ip"`
	}
	if err := c.api.get(ctx, "/control/querylog/config", nil, &cfg); err != nil {
		return nil, err
	}
	out := &adguardapipb.ListQueriesResponse{AnonymizeClientIp: cfg.AnonymizeClientIP}
	cursor, read := req.GetOlderThan(), 0
	for {
		// Quoted, AdGuard matches the client exactly instead of as a substring.
		q := url.Values{"search": {`"` + ip.String() + `"`}, "limit": {strconv.Itoa(min(maxLimit, budget-read))}, "offset": {"0"}}
		if cursor != "" {
			q.Set("older_than", cursor)
		}
		if reasons != nil {
			q["reason"] = reasons
		}
		var page struct {
			Oldest string     `json:"oldest"`
			Data   []logEntry `json:"data"`
		}
		if err := c.api.get(ctx, "/control/querylog", q, &page); err != nil {
			return nil, err
		}
		from, full := cursor, false
		for _, e := range page.Data {
			read++
			cursor = e.Time
			query := e.query()
			out.Reached = query.GetTime()
			if f.keeps(query) {
				out.Queries = append(out.Queries, query)
				if full = len(out.Queries) == want; full {
					break
				}
			}
		}
		if full || read >= budget {
			break
		}
		// The end of the log is an empty cursor, even after a page with
		// rows; a cursor that doesn't move on ends it too.
		if !older(page.Oldest, from) {
			cursor = ""
			break
		}
		cursor = page.Oldest
		if !f.searches() {
			break
		}
	}
	out.Oldest = cursor
	out.LinesRead = int32(read)
	return out, nil
}

// older says whether the cursor next is older than from (empty: newest).
func older(next, from string) bool {
	n, err := time.Parse(time.RFC3339Nano, next)
	if err != nil {
		return false
	}
	if from == "" {
		return true
	}
	f, err := time.Parse(time.RFC3339Nano, from)
	return err == nil && n.Before(f)
}

// filter is what a ListQueriesRequest keeps of the client's log lines.
type filter struct {
	mode  adguardapipb.QueryMode
	words []string
	qtype string
}

// searches says whether ddash filters on its own side, beyond the mode.
func (f filter) searches() bool { return len(f.words) > 0 || f.qtype != "" }

// keeps follows @ddash/ui's matches(): every word somewhere in the domain
// or the rule, ignoring case. It checks the mode too, since an AdGuard
// older than v0.107.77 ignores reason and sends every line.
func (f filter) keeps(q *adguardapipb.Query) bool {
	switch f.mode {
	case adguardapipb.QueryMode_QUERY_MODE_BLOCKED:
		if !q.GetBlocked() {
			return false
		}
	case adguardapipb.QueryMode_QUERY_MODE_ALLOWED:
		if q.GetBlocked() {
			return false
		}
	}
	if f.qtype != "" && !strings.EqualFold(q.GetType(), f.qtype) {
		return false
	}
	fields := []string{strings.ToLower(q.GetDomain()), strings.ToLower(q.GetRule())}
	for _, w := range f.words {
		// Like matches(), a word also matches without : and - (for a MAC).
		bare := separators.Replace(w)
		if !slices.ContainsFunc(fields, func(s string) bool {
			return strings.Contains(s, w) || bare != "" && strings.Contains(s, bare)
		}) {
			return false
		}
	}
	return true
}

var separators = strings.NewReplacer(":", "", "-", "")

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
