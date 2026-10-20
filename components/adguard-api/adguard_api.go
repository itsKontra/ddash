// Package adguardapi is the adguard-api Component: it reads AdGuard Home
// through its HTTP API under /control, with HTTP Basic auth, and turns
// ad-blocking off or on for one client.
package adguardapi

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// requestTimeout bounds one API call, so a dead AdGuard can't hold a
// getter past the host's deadline.
const requestTimeout = 5 * time.Second

// Definition is adguard-api, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*adguardapipb.AdguardApiConfig]{
		Manifest: manifest,
		Service:  &adguardapipb.AdguardApiService_ServiceDesc,
		New: func() sdk.Component[*adguardapipb.AdguardApiConfig] {
			return &component{queue: make(chan struct{}, 1)}
		},
	})
}

type component struct {
	adguardapipb.UnimplementedAdguardApiServiceServer
	api     *api
	status  *sdk.Cache[*adguardapipb.GetStatusResponse]
	stats   *sdk.Cache[*adguardapipb.GetStatsResponse]
	clients *sdk.Cache[*adguardapipb.ListClientsResponse]

	// queue holds the one client write running (clients.go), so two
	// find-or-creates for one address can't both create.
	queue chan struct{}
}

func (c *component) Start(_ context.Context, _ *sdk.Env, cfg *adguardapipb.AdguardApiConfig) error {
	base, err := url.Parse(cfg.GetUrl())
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" {
		var errs sdk.FieldErrors
		errs.Add("url", "must be AdGuard Home's web address, like https://198.18.1.3")
		return errs.Err()
	}
	c.api = &api{
		base: strings.TrimSuffix(base.String(), "/"),
		user: cfg.GetUsername(),
		pass: cfg.GetPassword(),
		http: &http.Client{Timeout: requestTimeout},
	}
	c.status = sdk.Cached(sdk.TTLFast, c.fetchStatus)
	c.stats = sdk.Cached(sdk.TTLSlow, c.fetchStats)
	c.clients = sdk.Cached(sdk.TTLSlow, c.fetchClients)
	return nil
}

// Health is down while AdGuard's API can't be read with the credentials.
// It shares the status read, so it costs no extra request on the FAST
// tier. A DNS server that isn't running is the Widget's news.
func (c *component) Health(ctx context.Context) sdk.Health {
	if _, err := c.status.Get(ctx); err != nil {
		return sdk.Health{State: sdk.Down, Message: status.Convert(err).Message()}
	}
	return sdk.Health{State: sdk.OK}
}

func (c *component) GetStatus(ctx context.Context, _ *adguardapipb.GetStatusRequest) (*adguardapipb.GetStatusResponse, error) {
	return c.status.Get(ctx)
}

func (c *component) GetStats(ctx context.Context, _ *adguardapipb.GetStatsRequest) (*adguardapipb.GetStatsResponse, error) {
	return c.stats.Get(ctx)
}

func (c *component) fetchStatus(ctx context.Context) (*adguardapipb.GetStatusResponse, error) {
	var data struct {
		Running                    bool   `json:"running"`
		ProtectionEnabled          bool   `json:"protection_enabled"`
		ProtectionDisabledDuration int64  `json:"protection_disabled_duration"`
		Version                    string `json:"version"`
	}
	if err := c.api.get(ctx, "/control/status", nil, &data); err != nil {
		return nil, err
	}
	out := &adguardapipb.GetStatusResponse{Running: data.Running, ProtectionEnabled: data.ProtectionEnabled, Version: data.Version}
	if !data.ProtectionEnabled && data.ProtectionDisabledDuration > 0 {
		out.ProtectionPausedFor = durationpb.New(time.Duration(data.ProtectionDisabledDuration) * time.Millisecond)
	}
	return out, nil
}

// day is the stats window, as /control/stats's recent (milliseconds).
var day = fmt.Sprint((24 * time.Hour).Milliseconds())

func (c *component) fetchStats(ctx context.Context) (*adguardapipb.GetStatsResponse, error) {
	var data struct {
		TimeUnits               string               `json:"time_units"`
		NumDNSQueries           int64                `json:"num_dns_queries"`
		NumBlockedFiltering     int64                `json:"num_blocked_filtering"`
		NumReplacedSafebrowsing int64                `json:"num_replaced_safebrowsing"`
		NumReplacedSafesearch   int64                `json:"num_replaced_safesearch"`
		NumReplacedParental     int64                `json:"num_replaced_parental"`
		AvgProcessingTime       float64              `json:"avg_processing_time"`
		DNSQueries              []int64              `json:"dns_queries"`
		BlockedFiltering        []int64              `json:"blocked_filtering"`
		TopQueriedDomains       []map[string]float64 `json:"top_queried_domains"`
		TopBlockedDomains       []map[string]float64 `json:"top_blocked_domains"`
		TopClients              []map[string]float64 `json:"top_clients"`
	}
	if err := c.api.get(ctx, "/control/stats", url.Values{"recent": {day}}, &data); err != nil {
		return nil, err
	}
	return &adguardapipb.GetStatsResponse{
		DnsQueries:             data.NumDNSQueries,
		BlockedFiltering:       data.NumBlockedFiltering,
		ReplacedSafebrowsing:   data.NumReplacedSafebrowsing,
		ReplacedSafesearch:     data.NumReplacedSafesearch,
		ReplacedParental:       data.NumReplacedParental,
		AvgProcessingMs:        math.Round(data.AvgProcessingTime*1e6) / 1e3,
		TimeUnit:               data.TimeUnits,
		DnsQueriesSeries:       data.DNSQueries,
		BlockedFilteringSeries: data.BlockedFiltering,
		TopQueriedDomains:      top(data.TopQueriedDomains),
		TopBlockedDomains:      top(data.TopBlockedDomains),
		TopClients:             top(data.TopClients),
	}, nil
}

// top reads AdGuard's top lists: single-key objects {"<name>": <count>}.
func top(entries []map[string]float64) []*adguardapipb.TopEntry {
	var out []*adguardapipb.TopEntry
	for _, e := range entries {
		for name, n := range e {
			out = append(out, &adguardapipb.TopEntry{Name: name, Count: int64(n)})
		}
	}
	return out
}

// api calls AdGuard Home's /control API with Basic auth.
type api struct {
	base, user, pass string
	http             *http.Client

	// refused, once AdGuard refused the credentials, is why: no request is
	// sent after that, since AdGuard locks out an address that keeps
	// failing. Saving the config restarts the Component with a fresh api.
	mu      sync.Mutex
	refused error
}

func (a *api) get(ctx context.Context, path string, query url.Values, out any) error {
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return a.send(ctx, http.MethodGet, path, nil, out)
}

func (a *api) post(ctx context.Context, path string, body any) error {
	return a.send(ctx, http.MethodPost, path, body, nil)
}

// send sends body (JSON, nil for none) and decodes a JSON answer into out
// (nil to ignore it). A write AdGuard refuses as invalid (400) is
// FailedPrecondition with AdGuard's message; any other failure is
// Unavailable.
func (a *api) send(ctx context.Context, method, path string, body, out any) error {
	a.mu.Lock()
	refused := a.refused
	a.mu.Unlock()
	if refused != nil {
		return refused
	}
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, reqBody)
	if err != nil {
		return err
	}
	req.SetBasicAuth(a.user, a.pass)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return status.Errorf(codes.Unavailable, "AdGuard Home unreachable: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return status.Errorf(codes.Unavailable, "AdGuard Home: reading %s: %v", path, err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		err := status.Errorf(codes.Unavailable, "AdGuard Home refused the username or password (%d); fix them in adguard-api's config", resp.StatusCode)
		a.mu.Lock()
		a.refused = err
		a.mu.Unlock()
		return err
	case resp.StatusCode == http.StatusBadRequest && method != http.MethodGet:
		return status.Errorf(codes.FailedPrecondition, "AdGuard Home refused it: %s", message(raw))
	case resp.StatusCode != http.StatusOK:
		return status.Errorf(codes.Unavailable, "AdGuard Home answered %s with %d: %s", strings.SplitN(path, "?", 2)[0], resp.StatusCode, message(raw))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return status.Errorf(codes.Unavailable, "AdGuard Home answered %s with no JSON: %v", strings.SplitN(path, "?", 2)[0], err)
	}
	return nil
}

// message is AdGuard's plain-text error, trimmed to one line.
func message(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
