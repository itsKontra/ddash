package checks_test

import (
	"slices"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	ddashpb "github.com/itsKontra/ddash/proto/ddash"
	"github.com/itsKontra/ddash/sdk/migrate"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

const day = 24 * time.Hour

// next lets time pass until Check id's next beat was recorded.
func next(t *testing.T, c checkspb.ChecksServiceClient, id int64) {
	t.Helper()
	before := find(t, c, id).GetLastBeat().AsTime()
	for range 2 * Interval11() / time.Second {
		after(time.Second)
		if find(t, c, id).GetLastBeat().AsTime().After(before) {
			return
		}
	}
	t.Fatalf("Check %d wasn't probed again", id)
}

func history(t *testing.T, c checkspb.ChecksServiceClient, id int64, w checkspb.Window) *checkspb.GetHistoryResponse {
	t.Helper()
	resp, err := c.GetHistory(t.Context(), &checkspb.GetHistoryRequest{Id: id, Window: w})
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	return resp
}

// uptime is a window's uptime as "up/down ratio", "-" for no ratio.
func uptime(u *checkspb.Uptime) string {
	ratio := "-"
	if u.Ratio != nil {
		ratio = num(u.GetRatio())
	}
	return u.GetWindow().String() + " " + itoa(u.GetUp()) + "/" + itoa(u.GetDown()) + " " + ratio
}

func uptimes(h *checkspb.GetHistoryResponse) []string {
	var out []string
	for _, u := range h.GetUptime() {
		out = append(out, uptime(u))
	}
	return out
}

func TestHistoryIsASlowGetter(t *testing.T) {
	for _, name := range []string{"GetHistory", "ListBeats"} {
		m := checkspb.File_checks_checks_proto.Services().ByName("ChecksService").Methods().ByName(protoreflect.Name(name))
		g, _ := proto.GetExtension(m.Options(), ddashpb.E_Getter).(*ddashpb.Getter)
		if g.GetRefresh() != ddashpb.Getter_SLOW {
			t.Errorf("%s refresh = %v, want SLOW", name, g.GetRefresh())
		}
	}
}

func TestUptimeIsUpBeatsOverUpAndDownBeatsWithPendingCountedAsDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		synctest.Wait()
		for range 2 {
			next(t, c, id)
		}
		l.set("nas:22", "refuse") // pending, pending, down
		for range 3 {
			next(t, c, id)
		}
		l.set("nas:22", "up")
		for range 4 {
			next(t, c, id)
		}

		h := history(t, c, id, checkspb.Window_WINDOW_UNSPECIFIED)
		want := []string{"WINDOW_24H 7/3 0.7", "WINDOW_30D 7/3 0.7", "WINDOW_365D 7/3 0.7"}
		if got := uptimes(h); !slices.Equal(got, want) {
			t.Errorf("uptime %v, want %v", got, want)
		}
		var changes []string
		for _, b := range h.GetChanges() {
			changes = append(changes, b.GetState().String())
		}
		if want := []string{"STATE_UP", "STATE_DOWN", "STATE_PENDING", "STATE_UP"}; !slices.Equal(changes, want) {
			t.Errorf("state changes %v, want %v (newest first)", changes, want)
		}
		if h.GetWindow() != checkspb.Window_WINDOW_24H {
			t.Errorf("window %v, want 24 h by default", h.GetWindow())
		}
	})
}

func TestAWindowWithoutBeatsHasNoUptime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "hang")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		synctest.Wait() // its first probe hangs, and disabling drops it
		if _, err := c.SetDisabled(t.Context(), &checkspb.SetDisabledRequest{Id: id, Disabled: true}); err != nil {
			t.Fatal(err)
		}
		after(time.Minute)

		want := []string{"WINDOW_24H 0/0 -", "WINDOW_30D 0/0 -", "WINDOW_365D 0/0 -"}
		if got := uptimes(history(t, c, id, checkspb.Window_WINDOW_24H)); !slices.Equal(got, want) {
			t.Errorf("uptime %v, want %v", got, want)
		}
	})
}

func TestLatencyIsAveragedOverUpBeats(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up 10ms")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		after(time.Second) // the first beat, 10 ms in
		l.set("nas:22", "up 30ms")
		next(t, c, id)
		l.set("nas:22", "refuse")
		next(t, c, id)
		l.set("nas:22", "up 20ms")
		next(t, c, id)

		h := history(t, c, id, checkspb.Window_WINDOW_24H)
		if got := latency(h.GetUptime()[0].GetLatency()); got != "20/10/30" {
			t.Errorf("24 h latency avg/min/max %s, want 20/10/30", got)
		}
		var buckets []string
		for _, b := range h.GetBuckets() {
			buckets = append(buckets, latency(b.GetLatency()))
		}
		if want := []string{"10/10/10", "30/30/30", "-", "20/20/20"}; !slices.Equal(buckets, want) {
			t.Errorf("minute buckets' latency %v, want %v", buckets, want)
		}
		hour := history(t, c, id, checkspb.Window_WINDOW_30D).GetBuckets()
		if len(hour) != 1 || latency(hour[0].GetLatency()) != "20/10/30" {
			t.Errorf("hour buckets %v, want one with 20/10/30", hour)
		}
	})
}

// latency is "avg/min/max" in ms, "-" when unset.
func latency(l *checkspb.Latency) string {
	if l == nil {
		return "-"
	}
	return num(l.GetAvgMs()) + "/" + num(l.GetMinMs()) + "/" + num(l.GetMaxMs())
}

func TestBucketsRollOverEachMinuteHourAndDay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		midnight := time.Now().UTC().Truncate(day).Add(day)
		time.Sleep(time.Until(midnight.Add(-150 * time.Second)))
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId() // beats at 23:57:30, then 60-66 s apart
		synctest.Wait()
		for range 4 {
			next(t, c, id)
		}

		for _, tc := range []struct {
			window checkspb.Window
			size   time.Duration
			want   []string
		}{
			{checkspb.Window_WINDOW_24H, time.Minute, []string{"23:57 1", "23:58 1", "23:59 1", "00:00 1", "00:01 1"}},
			{checkspb.Window_WINDOW_30D, time.Hour, []string{"23:00 3", "00:00 2"}},
			{checkspb.Window_WINDOW_365D, day, []string{"Jan 1 3", "Jan 2 2"}},
		} {
			h := history(t, c, id, tc.window)
			var got []string
			for _, b := range h.GetBuckets() {
				at := b.GetStart().AsTime().UTC()
				label := at.Format("15:04")
				if tc.size == day {
					label = at.Format("Jan 2")
				}
				got = append(got, label+" "+itoa(b.GetUp()))
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("%v buckets %v, want %v", tc.window, got, tc.want)
			}
			if h.GetBucket().AsDuration() != tc.size {
				t.Errorf("%v bucket size %v, want %v", tc.window, h.GetBucket().AsDuration(), tc.size)
			}
		}

		h := history(t, c, id, checkspb.Window_WINDOW_24H)
		if from, want := h.GetFrom().AsTime(), time.Now().Truncate(time.Minute).Add(-1439*time.Minute); !from.Equal(want) {
			t.Errorf("24 h window from %v, want %v (1440 minute buckets, the last holding now)", from, want)
		}
	})
}

func TestEachWindowCountsOnlyItsOwnSpan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		_, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		synctest.Wait()
		if _, err := c.SetDisabled(t.Context(), &checkspb.SetDisabledRequest{Id: id, Disabled: true}); err != nil {
			t.Fatal(err)
		}

		for _, tc := range []struct {
			wait time.Duration
			want []string
		}{
			{day + time.Minute, []string{"WINDOW_24H 0/0 -", "WINDOW_30D 1/0 1", "WINDOW_365D 1/0 1"}},
			{30 * day, []string{"WINDOW_24H 0/0 -", "WINDOW_30D 0/0 -", "WINDOW_365D 1/0 1"}},
			{335 * day, []string{"WINDOW_24H 0/0 -", "WINDOW_30D 0/0 -", "WINDOW_365D 0/0 -"}},
		} {
			after(tc.wait)
			if got := uptimes(history(t, c, id, checkspb.Window_WINDOW_24H)); !slices.Equal(got, tc.want) {
				t.Errorf("uptime %v, want %v", got, tc.want)
			}
		}
	})
}

func TestADailySweepKeepsBeatsForADayAndStateChangesForAYear(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		h, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId() // pending → up: a change
		synctest.Wait()
		next(t, c, id) // up again
		l.set("nas:22", "refuse")
		next(t, c, id) // up → pending: a change
		if _, err := c.SetDisabled(t.Context(), &checkspb.SetDisabledRequest{Id: id, Disabled: true}); err != nil {
			t.Fatal(err)
		}

		after(3 * day)
		if got := states(beats(t, c, id)); !slices.Equal(got, []string{"up", "pending"}) {
			t.Errorf("after 3 days, beats kept %v, want only the state changes", got)
		}
		if n := rowsIn(t, h, "check_event", id); n != 2 {
			t.Errorf("after 3 days, %d check_event rows, want the 2 state changes", n)
		}

		after(364 * day) // a year, plus up to a day for the sweep's jitter to catch up
		if n := rowsIn(t, h, "check_event", id); n != 0 {
			t.Errorf("after 367 days, %d check_event rows, want none", n)
		}
	})
}

func TestADailySweepDropsMinuteBucketsAfterADayHourBucketsAfter30DaysAndDayBucketsAfterAYear(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLab()
		l.set("nas:22", "up")
		h, c := startIn(t, l)
		id := create(t, c, tcp("nas:22")).GetId()
		synctest.Wait()
		if _, err := c.SetDisabled(t.Context(), &checkspb.SetDisabledRequest{Id: id, Disabled: true}); err != nil {
			t.Fatal(err)
		}
		began := time.Now()
		count := func() []int {
			return []int{statRows(t, h, id, "minute"), statRows(t, h, id, "hour"), statRows(t, h, id, "day")}
		}
		if got := count(); !slices.Equal(got, []int{1, 1, 1}) {
			t.Fatalf("minute/hour/day buckets %v, want one of each", got)
		}

		for _, tc := range []struct {
			at   time.Duration
			want []int
		}{
			{3 * day, []int{0, 1, 1}},
			{33 * day, []int{0, 0, 1}},
			{368 * day, []int{0, 0, 0}},
		} {
			after(tc.at - time.Since(began))
			if got := count(); !slices.Equal(got, tc.want) {
				t.Errorf("after %v, minute/hour/day buckets %v, want %v", tc.at, got, tc.want)
			}
		}
	})
}

func TestHistoryOfAnUnknownCheckIsNotFound(t *testing.T) {
	_, c := start(t)
	_, err := c.GetHistory(t.Context(), &checkspb.GetHistoryRequest{Id: 42})
	if status.Code(err) != codes.NotFound {
		t.Errorf("GetHistory: %v, want NotFound", err)
	}
	_, err = c.ListBeats(t.Context(), &checkspb.ListBeatsRequest{Id: 42})
	if status.Code(err) != codes.NotFound {
		t.Errorf("ListBeats: %v, want NotFound", err)
	}
}

// rowsIn counts Check id's rows in table, straight from the DB: retention
// is about what is stored.
func rowsIn(t *testing.T, h *sdktest.Harness, table string, id int64) int {
	t.Helper()
	return count(t, h, `SELECT count(*) FROM `+table+` WHERE check_id = ?`, id)
}

// statRows counts Check id's check_stat buckets of a period.
func statRows(t *testing.T, h *sdktest.Harness, id int64, period string) int {
	t.Helper()
	return count(t, h, `SELECT count(*) FROM check_stat WHERE check_id = ? AND period = ?`, id, period)
}

func count(t *testing.T, h *sdktest.Harness, query string, args ...any) int {
	t.Helper()
	db, err := migrate.Open(h.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func num(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
