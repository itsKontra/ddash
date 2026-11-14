package calendarapi

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-ical"
	"github.com/teambition/rrule-go"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/calendar-api/calendarapipb"
)

// maxSteps bounds how many occurrences of one recurring event are walked
// through to reach the window, so a minutely rule from years ago can't
// stall a read.
const maxSteps = 100_000

// vevent is one VEVENT: a single event, a recurring event's series, or
// (with recurrenceID set) one occurrence's override.
type vevent struct {
	uid, title, location string
	// allDay events have start and end at midnight UTC of their dates.
	allDay     bool
	start, end time.Time
	cancelled  bool
	// set is a series' recurrence; nil for a single event or an override.
	set *rrule.Set
	// recurrenceID is the original start of the occurrence an override
	// replaces; zero otherwise.
	recurrenceID time.Time
}

// occurrence is one event in the window.
type occurrence struct {
	calendar, id, title, location string
	allDay                        bool
	start, end                    time.Time
}

func (o occurrence) proto() *calendarapipb.Event {
	e := &calendarapipb.Event{Calendar: o.calendar, Id: o.id, Title: o.title, Location: o.location, AllDay: o.allDay}
	if o.allDay {
		e.StartDate, e.EndDate = o.start.Format(time.DateOnly), o.end.Format(time.DateOnly)
	} else {
		e.Start, e.End = timestamppb.New(o.start), timestamppb.New(o.end)
	}
	return e
}

// parse reads an iCalendar file's events. An event it can't make sense of
// (no start, say) is left out rather than failing the calendar.
func parse(body []byte) ([]vevent, error) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")) // a byte order mark
	cal, err := ical.NewDecoder(bytes.NewReader(body)).Decode()
	if err != nil {
		return nil, fmt.Errorf("not an iCalendar file: %w", err)
	}
	// Floating times are in the calendar's zone, else the host's.
	zone := time.Local
	if p := cal.Props.Get("X-WR-TIMEZONE"); p != nil {
		zone = location(p.Value, zone)
	}
	var out []vevent
	for _, e := range cal.Events() {
		if v, ok := toVevent(e, zone); ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func toVevent(e ical.Event, zone *time.Location) (vevent, bool) {
	startProp := e.Props.Get(ical.PropDateTimeStart)
	if startProp == nil {
		return vevent{}, false
	}
	start, allDay, err := parseTime(startProp, startProp.Value, zone)
	if err != nil {
		return vevent{}, false
	}
	v := vevent{allDay: allDay, start: start}
	v.uid, _ = e.Props.Text(ical.PropUID)
	v.title, _ = e.Props.Text(ical.PropSummary)
	v.location, _ = e.Props.Text(ical.PropLocation)
	if status, _ := e.Props.Text(ical.PropStatus); strings.EqualFold(status, "CANCELLED") {
		v.cancelled = true
	}

	v.end = start
	if allDay {
		v.end = start.AddDate(0, 0, 1)
	}
	if p := e.Props.Get(ical.PropDateTimeEnd); p != nil {
		if end, _, err := parseTime(p, p.Value, zone); err == nil {
			v.end = end
		}
	} else if p := e.Props.Get(ical.PropDuration); p != nil {
		if d, err := p.Duration(); err == nil {
			v.end = start.Add(d)
		}
	}
	if v.end.Before(v.start) {
		v.end = v.start
	}

	if p := e.Props.Get(ical.PropRecurrenceID); p != nil {
		if id, _, err := parseTime(p, p.Value, zone); err == nil {
			v.recurrenceID = id
		}
		return v, true
	}
	v.set = recurrence(e, start, zone)
	return v, true
}

// recurrence is a series' RRULE, RDATEs and EXDATEs from start, or nil
// when it has neither rule nor extra dates (or a rule rrule-go can't read:
// then it is listed once).
func recurrence(e ical.Event, start time.Time, zone *time.Location) *rrule.Set {
	set := &rrule.Set{}
	recurs := false
	if p := e.Props.Get(ical.PropRecurrenceRule); p != nil {
		opt, err := rrule.StrToROptionInLocation(p.Value, start.Location())
		if err == nil {
			opt.Dtstart = start
			if r, err := rrule.NewRRule(*opt); err == nil {
				set.RRule(r)
				recurs = true
			}
		}
	}
	for _, p := range e.Props.Values(ical.PropRecurrenceDates) {
		for _, t := range parseTimes(&p, zone) {
			set.RDate(t)
			recurs = true
		}
	}
	if !recurs {
		return nil
	}
	if set.GetRRule() == nil {
		// Without a rule, DTSTART is still the first occurrence.
		set.RDate(start)
	}
	set.DTStart(start)
	for _, p := range e.Props.Values(ical.PropExceptionDates) {
		for _, t := range parseTimes(&p, zone) {
			set.ExDate(t)
		}
	}
	return set
}

// parseTimes reads a list property (EXDATE, RDATE): comma-separated
// values sharing its parameters. A value it can't read (an RDATE period,
// say) is skipped.
func parseTimes(p *ical.Prop, zone *time.Location) []time.Time {
	var out []time.Time
	for value := range strings.SplitSeq(p.Value, ",") {
		if t, _, err := parseTime(p, strings.TrimSpace(value), zone); err == nil {
			out = append(out, t)
		}
	}
	return out
}

// parseTime reads a DATE or DATE-TIME value of p: a date at midnight UTC
// (allDay), a UTC time, a time in its TZID's zone, or a floating time in
// zone.
func parseTime(p *ical.Prop, value string, zone *time.Location) (t time.Time, allDay bool, err error) {
	if p.Params.Get(ical.ParamValue) == string(ical.ValueDate) || len(value) == len("20060102") {
		t, err = time.ParseInLocation("20060102", value, time.UTC)
		return t, true, err
	}
	if v, ok := strings.CutSuffix(value, "Z"); ok {
		t, err = time.ParseInLocation("20060102T150405", v, time.UTC)
		return t, false, err
	}
	if tzid := p.Params.Get(ical.ParamTimezoneID); tzid != "" {
		zone = location(tzid, zone)
	}
	t, err = time.ParseInLocation("20060102T150405", value, zone)
	return t, false, err
}

// location is the IANA zone tzid names, also when a path precedes the
// name ("/mozilla.org/20050126_1/Europe/Berlin"), else fallback (e.g. for
// a Windows zone name).
func location(tzid string, fallback *time.Location) *time.Location {
	name := strings.Trim(tzid, `"`)
	for name != "" {
		if loc, err := time.LoadLocation(name); err == nil && name != "Local" {
			return loc
		}
		_, rest, ok := strings.Cut(name, "/")
		if !ok {
			break
		}
		name = rest
	}
	return fallback
}

// expand lists the events on calendar that overlap [from, to): each
// single event, each occurrence of a series that no override replaces,
// and each override, leaving out what is cancelled.
func expand(calendar string, events []vevent, from, to time.Time) []occurrence {
	type key struct {
		uid string
		at  int64
	}
	overridden := map[key]bool{}
	for _, v := range events {
		if !v.recurrenceID.IsZero() {
			overridden[key{v.uid, v.recurrenceID.Unix()}] = true
		}
	}
	var out []occurrence
	add := func(v vevent, start, end time.Time, id string) {
		// An event without length overlaps when it starts in the window.
		overlaps := start.Before(to) && (end.After(from) || (start.Equal(end) && !start.Before(from)))
		if v.cancelled || !overlaps {
			return
		}
		out = append(out, occurrence{calendar: calendar, id: id, title: v.title, location: v.location, allDay: v.allDay, start: start, end: end})
	}
	for _, v := range events {
		switch {
		case !v.recurrenceID.IsZero():
			add(v, v.start, v.end, occurrenceID(calendar, v.uid, v.recurrenceID))
		case v.set == nil:
			add(v, v.start, v.end, calendar+"/"+v.uid)
		default:
			length := v.end.Sub(v.start)
			next := v.set.Iterator()
			for range maxSteps {
				t, ok := next()
				if !ok || !t.Before(to) {
					break
				}
				// All-day dates are in UTC, so their length is whole days.
				end := t.Add(length)
				if end.Before(from) || overridden[key{v.uid, t.Unix()}] {
					continue
				}
				add(v, t, end, occurrenceID(calendar, v.uid, t))
			}
		}
	}
	return out
}

func occurrenceID(calendar, uid string, at time.Time) string {
	return calendar + "/" + uid + "/" + at.UTC().Format(time.RFC3339)
}
