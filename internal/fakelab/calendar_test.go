package fakelab_test

import (
	"testing"
	"time"

	calendarapi "github.com/itsKontra/ddash/components/calendar-api"
	"github.com/itsKontra/ddash/components/calendar-api/calendarapipb"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// The lab's calendars follow the clock, so `make dev` always shows events
// just past, today and coming up, from every calendar.
func TestTheLabsCalendarsHaveEventsJustPastAndComingUp(t *testing.T) {
	lab, err := fakelab.Start(repoRoot, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lab.Close)
	h := sdktest.New(t, calendarapi.Definition())
	h.MustStart(lab.Config()["calendar-api"])

	resp, err := calendarapipb.NewCalendarApiServiceClient(h.Conn).ListEvents(t.Context(), &calendarapipb.ListEventsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range resp.GetCalendars() {
		if c.GetError() != "" {
			t.Errorf("%s: %s", c.GetName(), c.GetError())
		}
	}
	now := time.Now()
	var past, coming, recurring, allDay int
	for _, e := range resp.GetEvents() {
		switch {
		case e.GetAllDay():
			allDay++
		case e.GetEnd().AsTime().Before(now):
			past++
		default:
			coming++
		}
		if e.GetTitle() == "Football training" {
			recurring++
		}
	}
	if len(resp.GetCalendars()) != 2 || past == 0 || coming == 0 || allDay == 0 || recurring == 0 {
		t.Errorf("%d calendars, %d events past, %d coming, %d all-day, %d weekly; want 2 calendars and some of each", len(resp.GetCalendars()), past, coming, allDay, recurring)
	}
}
