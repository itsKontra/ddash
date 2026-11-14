// Package calendarapi is the calendar-api Component: it reads each
// configured calendar's iCalendar file from its secret iCal address with
// GET, at most every readTTL, and lists the events from two days ago to
// days_ahead days from now with recurring events expanded. It never
// writes anything.
package calendarapi

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	// The zones TZIDs name, whatever the host has installed.
	_ "time/tzdata"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/calendar-api/calendarapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

const (
	// readTTL is how long one read of the calendars is served: calendars
	// change rarely, and Google refreshes a secret address's file only
	// every few hours anyway.
	readTTL = 5 * time.Minute
	// readTimeout bounds one calendar's read.
	readTimeout = 15 * time.Second
	// maxSize is the largest iCalendar file read: years of history fit.
	maxSize = 20 << 20
	// past is how far back the events reach.
	past = 48 * time.Hour
	// defaultDaysAhead is how far ahead they reach unless configured.
	defaultDaysAhead = 14
)

// Definition is calendar-api, ready for sdk.Serve.
func Definition() sdk.Definition {
	return define(time.Now, &http.Client{Timeout: readTimeout})
}

// define is calendar-api on the clock now, reading through client.
func define(now func() time.Time, client *http.Client) sdk.Definition {
	return sdk.Define(sdk.Spec[*calendarapipb.CalendarApiConfig]{
		Manifest: manifest,
		Service:  &calendarapipb.CalendarApiService_ServiceDesc,
		New: func() sdk.Component[*calendarapipb.CalendarApiConfig] {
			return &component{now: now, client: client}
		},
	})
}

type component struct {
	calendarapipb.UnimplementedCalendarApiServiceServer
	now    func() time.Time
	client *http.Client

	sources   []source
	daysAhead int
	feeds     *sdk.Cache[[]feed]
}

// source is one configured calendar.
type source struct {
	name string
	// address is its secret iCal address: never logged or returned.
	address string
}

// feed is one read of a calendar.
type feed struct {
	name   string
	at     time.Time
	events []vevent
	err    error
}

func (c *component) Start(_ context.Context, _ *sdk.Env, cfg *calendarapipb.CalendarApiConfig) error {
	var errs sdk.FieldErrors
	c.sources = parseCalendars(cfg.GetCalendars(), &errs)
	c.daysAhead = defaultDaysAhead
	if cfg.DaysAhead != nil {
		c.daysAhead = int(cfg.GetDaysAhead())
		if c.daysAhead < 1 || c.daysAhead > 90 {
			errs.Add("days_ahead", "must be 1 to 90")
		}
	}
	if err := errs.Err(); err != nil {
		return err
	}
	c.feeds = sdk.Cached(readTTL, c.readAll)
	return nil
}

// parseCalendars reads "Name = address" entries separated by ";" or line
// breaks. Its errors name a calendar by its name or position, never by its
// address.
func parseCalendars(s string, errs *sdk.FieldErrors) []source {
	var out []source
	seen := map[string]bool{}
	entries := strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '\n' || r == '\r' })
	n := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		n++
		name, address, ok := strings.Cut(entry, "=")
		name, address = strings.TrimSpace(name), strings.TrimSpace(address)
		if !ok || name == "" || strings.Contains(name, "/") {
			errs.Add("calendars", "calendar %d has no name: write each as Name = address", n)
			continue
		}
		u, err := url.Parse(address)
		if err == nil && u.Scheme == "webcal" {
			u.Scheme = "https"
		}
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			errs.Add("calendars", "%s's address must be an https, http or webcal URL", name)
			continue
		}
		if seen[name] {
			errs.Add("calendars", "%s is named twice", name)
			continue
		}
		seen[name] = true
		out = append(out, source{name: name, address: u.String()})
	}
	return out
}

func (c *component) ListEvents(ctx context.Context, _ *calendarapipb.ListEventsRequest) (*calendarapipb.ListEventsResponse, error) {
	feeds, err := c.feeds.Get(ctx)
	if err != nil {
		return nil, err
	}
	now := c.now()
	from, to := now.Add(-past), now.AddDate(0, 0, c.daysAhead)
	resp := &calendarapipb.ListEventsResponse{From: timestamppb.New(from), To: timestamppb.New(to)}
	var all []occurrence
	for _, f := range feeds {
		cal := &calendarapipb.Calendar{Name: f.name, ReadAt: timestamppb.New(f.at)}
		if f.err != nil {
			cal.Error = f.err.Error()
		}
		resp.Calendars = append(resp.Calendars, cal)
		all = append(all, expand(f.name, f.events, from, to)...)
	}
	slices.SortStableFunc(all, func(a, b occurrence) int {
		if c := a.start.Compare(b.start); c != 0 {
			return c
		}
		if a.allDay != b.allDay {
			if a.allDay {
				return -1
			}
			return 1
		}
		return strings.Compare(a.title, b.title)
	})
	for _, o := range all {
		resp.Events = append(resp.Events, o.proto())
	}
	return resp, nil
}

// Health is down when no calendar can be read, and degraded when some can't.
func (c *component) Health(ctx context.Context) sdk.Health {
	feeds, err := c.feeds.Get(ctx)
	if err != nil {
		return sdk.Health{State: sdk.Down, Message: err.Error()}
	}
	var problems []string
	for _, f := range feeds {
		if f.err != nil {
			problems = append(problems, f.name+": "+f.err.Error())
		}
	}
	switch {
	case len(problems) == 0:
		return sdk.Health{State: sdk.OK}
	case len(problems) == len(feeds):
		return sdk.Health{State: sdk.Down, Message: strings.Join(problems, "; ")}
	default:
		return sdk.Health{State: sdk.Degraded, Message: strings.Join(problems, "; ")}
	}
}

// readAll reads every calendar in parallel. A calendar that fails is
// listed with its error, so readAll itself never fails.
func (c *component) readAll(ctx context.Context) ([]feed, error) {
	feeds := make([]feed, len(c.sources))
	var wg sync.WaitGroup
	for i, s := range c.sources {
		wg.Go(func() {
			events, err := c.read(ctx, s)
			feeds[i] = feed{name: s.name, at: c.now(), events: events, err: err}
		})
	}
	wg.Wait()
	return feeds, nil
}

// read fetches and parses one calendar. Its error names the address's
// host, never the address: the rest of it is the secret.
func (c *component) read(ctx context.Context, s source) ([]vevent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.address, nil)
	if err != nil {
		return nil, errors.New("its address can't be requested")
	}
	host := req.URL.Host
	req.Header.Set("Accept", "text/calendar")
	req.Header.Set("User-Agent", "ddash")
	resp, err := c.client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, fmt.Errorf("%s unreachable: %w", host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s answered %s", host, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("%s: reading: %w", host, err)
	}
	if len(body) > maxSize {
		return nil, fmt.Errorf("%s: the calendar is larger than %d MiB", host, maxSize>>20)
	}
	return parse(body)
}
