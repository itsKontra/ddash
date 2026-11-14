package calendarapi_test

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	calendarapi "github.com/itsKontra/ddash/components/calendar-api"
	"github.com/itsKontra/ddash/components/calendar-api/calendarapipb"
	"github.com/itsKontra/ddash/components/calendar-api/calendarfake"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// now is the stopped clock of every test: a Saturday.
var now = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func start(t *testing.T, cfg *calendarapipb.CalendarApiConfig) (*sdktest.Harness, calendarapipb.CalendarApiServiceClient) {
	t.Helper()
	h := sdktest.New(t, calendarapi.DefineAt(now))
	h.MustStart(cfg)
	return h, calendarapipb.NewCalendarApiServiceClient(h.Conn)
}

func list(t *testing.T, c calendarapipb.CalendarApiServiceClient) *calendarapipb.ListEventsResponse {
	t.Helper()
	resp, err := c.ListEvents(t.Context(), &calendarapipb.ListEventsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// lines is each event as "calendar | title | start..end", in UTC.
func lines(r *calendarapipb.ListEventsResponse) []string {
	var out []string
	for _, e := range r.GetEvents() {
		if e.GetAllDay() {
			out = append(out, fmt.Sprintf("%s | %s | %s..%s", e.GetCalendar(), e.GetTitle(), e.GetStartDate(), e.GetEndDate()))
			continue
		}
		const f = "2006-01-02 15:04"
		out = append(out, fmt.Sprintf("%s | %s | %s..%s", e.GetCalendar(), e.GetTitle(), e.GetStart().AsTime().Format(f), e.GetEnd().AsTime().Format(f)))
	}
	return out
}

// serve answers GET /<name>.ics with each calendar's body, and counts the reads.
func serve(t *testing.T, calendars map[string]string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		body, ok := calendars[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/calendar")
		fmt.Fprint(w, strings.ReplaceAll(body, "\n", "\r\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, &reads
}

// vcalendar wraps events in a calendar with the given header lines.
func vcalendar(header string, events ...string) string {
	return "BEGIN:VCALENDAR\nVERSION:2.0\nPRODID:-//test//EN\n" + header + strings.Join(events, "") + "END:VCALENDAR\n"
}

func days(n int64) *int64 { return &n }

func TestNoCalendarsIsAValidConfig(t *testing.T) {
	h, c := start(t, &calendarapipb.CalendarApiConfig{})

	got := list(t, c)

	if len(got.GetCalendars()) != 0 || len(got.GetEvents()) != 0 {
		t.Errorf("calendars, events = %v, %v; want none", got.GetCalendars(), got.GetEvents())
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("health = %v, want ok", got)
	}
}

func TestEachCalendarIsNamedWithAnHTTPAddressAndNoErrorRepeatsTheAddress(t *testing.T) {
	const secret = "private-0123456789abcdef"
	for _, tc := range []struct {
		calendars string
		days      *int64
		want      []string
	}{
		{calendars: "https://calendar.example/" + secret + "/basic.ics", want: []string{"calendars: calendar 1 has no name: write each as Name = address"}},
		{calendars: " = https://calendar.example/" + secret, want: []string{"calendars: calendar 1 has no name: write each as Name = address"}},
		{calendars: "Family = ftp://calendar.example/" + secret, want: []string{"calendars: Family's address must be an https, http or webcal URL"}},
		{calendars: "Family = calendar.example/" + secret, want: []string{"calendars: Family's address must be an https, http or webcal URL"}},
		{
			calendars: "Family = https://calendar.example/a; Family = https://calendar.example/" + secret,
			want:      []string{"calendars: Family is named twice"},
		},
		{calendars: "", days: days(0), want: []string{"days_ahead: must be 1 to 90"}},
	} {
		h := sdktest.New(t, calendarapi.DefineAt(now))

		err := h.Start(&calendarapipb.CalendarApiConfig{Calendars: tc.calendars, DaysAhead: tc.days})

		var got []string
		for _, fe := range sdk.FieldErrorsOf(err) {
			got = append(got, fe.Path+": "+fe.Message)
		}
		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("%q: field errors (-want +got):\n%s", tc.calendars, diff)
		}
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Errorf("%q: the error repeats the address: %v", tc.calendars, err)
		}
	}
}

func TestAcceptsSeveralCalendarsOnLinesOrAfterSemicolonsAndWebcalAddresses(t *testing.T) {
	h := sdktest.New(t, calendarapi.DefineAt(now))

	err := h.Start(&calendarapipb.CalendarApiConfig{Calendars: "Family = webcal://calendar.example/a.ics\nWork=https://calendar.example/b.ics;\n Holidays = http://calendar.example/c.ics ;"})
	if err != nil {
		t.Fatal(err)
	}
}

// The fake's baseline, rendered on the stopped clock: a recurring weekly
// event with an exception and a moved occurrence, all-day and multi-day
// events, a weekday stand-up in Berlin across the end of summer time, and
// a cancelled event.
func TestListsTheEventsFromTwoDaysAgoToDaysAheadSortedByStart(t *testing.T) {
	fake, err := calendarfake.New("testdata/baseline", calendarfake.Options{Now: func() time.Time { return now }, Miss: func(r string, err error) { t.Errorf("%s: %v", r, err) }})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	_, c := start(t, &calendarapipb.CalendarApiConfig{Calendars: "Home = " + srv.URL + "/home.ics; Work = " + srv.URL + "/work.ics", DaysAhead: days(15)})

	got := list(t, c)

	want := []string{
		"Work | Stand-up | 2026-11-13 08:30..2026-11-13 08:45",
		"Home | Dentist | 2026-11-13 10:00..2026-11-13 11:00",
		"Home | Put the bins out | 2026-11-14..2026-11-15",
		"Home | Football training | 2026-11-14 18:00..2026-11-14 19:30",
		"Home | Dinner with friends | 2026-11-15 19:30..2026-11-15 22:00",
		"Home | Grandma's birthday | 2026-11-16..2026-11-17",
		"Work | Stand-up | 2026-11-16 08:30..2026-11-16 08:45",
		"Work | Stand-up | 2026-11-17 08:30..2026-11-17 08:45",
		"Work | Quarterly review | 2026-11-17 14:00..2026-11-17 15:30",
		"Home | Weekend in the mountains | 2026-11-18..2026-11-21",
		"Work | Stand-up | 2026-11-18 08:30..2026-11-18 08:45",
		"Work | Stand-up | 2026-11-19 08:30..2026-11-19 08:45",
		"Work | Stand-up | 2026-11-20 08:30..2026-11-20 08:45",
		"Work | Stand-up | 2026-11-23 08:30..2026-11-23 08:45",
		"Work | Team offsite | 2026-11-23 09:00..2026-11-24 17:00",
		"Work | Stand-up | 2026-11-24 08:30..2026-11-24 08:45",
		"Work | Stand-up | 2026-11-25 08:30..2026-11-25 08:45",
		"Work | Stand-up | 2026-11-26 08:30..2026-11-26 08:45",
		"Work | Stand-up | 2026-11-27 08:30..2026-11-27 08:45",
		"Home | Football training (late pitch) | 2026-11-28 19:00..2026-11-28 20:30",
	}
	if diff := cmp.Diff(want, lines(got)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if from, to := got.GetFrom().AsTime(), got.GetTo().AsTime(); !from.Equal(now.Add(-48*time.Hour)) || !to.Equal(now.AddDate(0, 0, 15)) {
		t.Errorf("window = %v..%v, want two days ago to 15 days ahead", from, to)
	}
	var names []string
	for _, cal := range got.GetCalendars() {
		names = append(names, cal.GetName()+":"+cal.GetError())
		if !cal.GetReadAt().AsTime().Equal(now) {
			t.Errorf("%s read at %v, want %v", cal.GetName(), cal.GetReadAt().AsTime(), now)
		}
	}
	if diff := cmp.Diff([]string{"Home:", "Work:"}, names); diff != "" {
		t.Errorf("calendars (-want +got):\n%s", diff)
	}
	ids := map[string]bool{}
	for _, e := range got.GetEvents() {
		if ids[e.GetId()] {
			t.Errorf("id %q is used twice", e.GetId())
		}
		ids[e.GetId()] = true
		if e.GetTitle() == "Quarterly review" && e.GetLocation() != "Room 2" {
			t.Errorf("Quarterly review's location = %q, want Room 2", e.GetLocation())
		}
	}
}

func TestExpandsRecurrenceRulesWithExceptionsExtraDatesAndOverrides(t *testing.T) {
	// Daily at 09:00 in New York for 6 days from the 13th: the 15th and
	// 16th are excepted (one EXDATE with two values), the 14th is
	// cancelled, the 17th moved, and the 21st added.
	cal := vcalendar("",
		"BEGIN:VEVENT\nUID:daily\nDTSTART;TZID=America/New_York:20261113T090000\nDTEND;TZID=America/New_York:20261113T093000\n"+
			"RRULE:FREQ=DAILY;COUNT=6\nEXDATE;TZID=America/New_York:20261115T090000,20261116T090000\nRDATE;TZID=America/New_York:20261121T090000\n"+
			"SUMMARY:Daily\nEND:VEVENT\n",
		"BEGIN:VEVENT\nUID:daily\nRECURRENCE-ID;TZID=America/New_York:20261114T090000\nDTSTART;TZID=America/New_York:20261114T090000\n"+
			"DTEND;TZID=America/New_York:20261114T093000\nSTATUS:CANCELLED\nSUMMARY:Daily\nEND:VEVENT\n",
		"BEGIN:VEVENT\nUID:daily\nRECURRENCE-ID:20261117T140000Z\nDTSTART;TZID=America/New_York:20261117T150000\n"+
			"DTEND;TZID=America/New_York:20261117T160000\nSUMMARY:Daily (moved)\nEND:VEVENT\n",
	)
	srv, _ := serve(t, map[string]string{"a.ics": cal})
	_, c := start(t, &calendarapipb.CalendarApiConfig{Calendars: "Ops = " + srv.URL + "/a.ics"})

	want := []string{
		"Ops | Daily | 2026-11-13 14:00..2026-11-13 14:30",
		"Ops | Daily | 2026-11-18 14:00..2026-11-18 14:30",
		"Ops | Daily (moved) | 2026-11-17 20:00..2026-11-17 21:00",
		"Ops | Daily | 2026-11-21 14:00..2026-11-21 14:30",
	}
	if diff := cmp.Diff(sorted(want), lines(list(t, c))); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

// sorted orders lines as ListEvents does, by start.
func sorted(want []string) []string {
	out := append([]string(nil), want...)
	start := func(l string) string { return strings.SplitN(l, " | ", 3)[2] }
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if start(out[j]) < start(out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestAllDayEventsKeepTheirDates(t *testing.T) {
	cal := vcalendar("X-WR-TIMEZONE:Pacific/Auckland\n",
		// No DTEND: one day.
		"BEGIN:VEVENT\nUID:holiday\nDTSTART;VALUE=DATE:20261115\nSUMMARY:Holiday\nEND:VEVENT\n",
		// Every year since 1950, as a birthday is.
		"BEGIN:VEVENT\nUID:birthday\nDTSTART;VALUE=DATE:19501120\nDTEND;VALUE=DATE:19501121\nRRULE:FREQ=YEARLY\nSUMMARY:Birthday\nEND:VEVENT\n",
		// Monday to Wednesday, ended by an exclusive DTEND.
		"BEGIN:VEVENT\nUID:trip\nDTSTART;VALUE=DATE:20261116\nDTEND;VALUE=DATE:20261119\nSUMMARY:Trip\nEND:VEVENT\n",
		// Began before the window and still runs.
		"BEGIN:VEVENT\nUID:exhibition\nDTSTART;VALUE=DATE:20261101\nDTEND;VALUE=DATE:20261201\nSUMMARY:Exhibition\nEND:VEVENT\n",
		// Ended before the window.
		"BEGIN:VEVENT\nUID:old\nDTSTART;VALUE=DATE:20261101\nDTEND;VALUE=DATE:20261102\nSUMMARY:Old\nEND:VEVENT\n",
	)
	srv, _ := serve(t, map[string]string{"a.ics": cal})
	_, c := start(t, &calendarapipb.CalendarApiConfig{Calendars: "Family = " + srv.URL + "/a.ics"})

	want := []string{
		"Family | Exhibition | 2026-11-01..2026-12-01",
		"Family | Holiday | 2026-11-15..2026-11-16",
		"Family | Trip | 2026-11-16..2026-11-19",
		"Family | Birthday | 2026-11-20..2026-11-21",
	}
	if diff := cmp.Diff(want, lines(list(t, c))); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestTimesWithoutAKnownZoneAreInTheCalendarsZone(t *testing.T) {
	cal := vcalendar("X-WR-TIMEZONE:Europe/Berlin\n",
		// Floating: the calendar's zone.
		"BEGIN:VEVENT\nUID:floating\nDTSTART:20261115T100000\nDURATION:PT1H\nSUMMARY:Floating\nEND:VEVENT\n",
		// An unknown TZID: the calendar's zone too.
		"BEGIN:VEVENT\nUID:unknown\nDTSTART;TZID=Somewhere Standard Time:20261115T120000\nDTEND;TZID=Somewhere Standard Time:20261115T130000\nSUMMARY:Unknown zone\nEND:VEVENT\n",
		// A TZID with a path before its IANA name.
		"BEGIN:VEVENT\nUID:prefixed\nDTSTART;TZID=/mozilla.org/20050126_1/America/New_York:20261115T090000\nDTEND;TZID=/mozilla.org/20050126_1/America/New_York:20261115T100000\nSUMMARY:Prefixed\nEND:VEVENT\n",
		// No end and no duration: it ends as it starts.
		"BEGIN:VEVENT\nUID:instant\nDTSTART:20261116T070000Z\nSUMMARY:Reminder\nEND:VEVENT\n",
	)
	srv, _ := serve(t, map[string]string{"a.ics": cal})
	_, c := start(t, &calendarapipb.CalendarApiConfig{Calendars: "Family = " + srv.URL + "/a.ics"})

	want := []string{
		"Family | Floating | 2026-11-15 09:00..2026-11-15 10:00",
		"Family | Unknown zone | 2026-11-15 11:00..2026-11-15 12:00",
		"Family | Prefixed | 2026-11-15 14:00..2026-11-15 15:00",
		"Family | Reminder | 2026-11-16 07:00..2026-11-16 07:00",
	}
	if diff := cmp.Diff(want, lines(list(t, c))); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
}

func TestACalendarThatCantBeReadIsListedWithItsErrorAndTheOthersStillAre(t *testing.T) {
	const secret = "private-0123456789abcdef"
	ok := vcalendar("", "BEGIN:VEVENT\nUID:a\nDTSTART:20261115T100000Z\nDTEND:20261115T110000Z\nSUMMARY:Readable\nEND:VEVENT\n")
	srv, _ := serve(t, map[string]string{"ok.ics": ok, "page.ics": "<html>Sign in</html>\n"})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := l.Addr().String()
	l.Close()
	h, c := start(t, &calendarapipb.CalendarApiConfig{Calendars: strings.Join([]string{
		"Readable = " + srv.URL + "/ok.ics",
		"Reset = " + srv.URL + "/" + secret + ".ics",
		"Page = " + srv.URL + "/page.ics?" + secret,
		"Gone = http://" + dead + "/" + secret + ".ics",
	}, ";")})

	got := list(t, c)

	if diff := cmp.Diff([]string{"Readable | Readable | 2026-11-15 10:00..2026-11-15 11:00"}, lines(got)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	errs := map[string]string{}
	for _, cal := range got.GetCalendars() {
		errs[cal.GetName()] = cal.GetError()
		if strings.Contains(cal.GetError(), secret) {
			t.Errorf("%s's error repeats its address: %s", cal.GetName(), cal.GetError())
		}
	}
	host := strings.TrimPrefix(srv.URL, "http://")
	want := map[string]string{
		"Readable": "",
		"Reset":    host + " answered 404 Not Found",
		"Page":     "not an iCalendar file",
		"Gone":     dead + " unreachable",
	}
	for name, prefix := range want {
		if got := errs[name]; (prefix == "") != (got == "") || !strings.HasPrefix(got, prefix) {
			t.Errorf("%s's error = %q, want it to start with %q", name, got, prefix)
		}
	}
	health := h.Health()
	if health.State != sdk.Degraded || !strings.Contains(health.Message, "Reset: ") || strings.Contains(health.Message, secret) {
		t.Errorf("health = %v, want degraded naming Reset, without its address", health)
	}
}

func TestHealthIsDownWhenNoCalendarCanBeRead(t *testing.T) {
	srv, _ := serve(t, map[string]string{})
	h, _ := start(t, &calendarapipb.CalendarApiConfig{Calendars: "Family = " + srv.URL + "/gone.ics"})

	if got := h.Health(); got.State != sdk.Down {
		t.Errorf("health = %v, want down", got)
	}
}

func TestReadsEachCalendarOnceAndServesThatReadForAWhile(t *testing.T) {
	srv, reads := serve(t, map[string]string{"a.ics": vcalendar("")})
	h, c := start(t, &calendarapipb.CalendarApiConfig{Calendars: "Family = " + srv.URL + "/a.ics"})

	list(t, c)
	list(t, c)
	h.Health()

	if got := reads.Load(); got != 1 {
		t.Errorf("reads = %d, want 1", got)
	}
}
