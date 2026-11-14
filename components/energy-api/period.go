package energyapi

import (
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/itsKontra/ddash/components/energy-api/energyapipb"
)

const day = "2006-01-02"

// period is one day, week, month or year in a zone, split into buckets.
type period struct {
	span     pb.Span
	from, to time.Time
	// edges are the buckets' bounds: from, …, to.
	edges []time.Time
	// window is what readings are reduced to: counters' last value and
	// powers' mean in each window. A day or week returns its powers as
	// its curve.
	window time.Duration
	curve  bool
}

// periodOf is the span holding date (YYYY-MM-DD, today when empty) in loc.
func periodOf(span pb.Span, date string, loc *time.Location, now time.Time) (period, error) {
	d := now.In(loc)
	if date != "" {
		var err error
		if d, err = time.ParseInLocation(day, date, loc); err != nil {
			return period{}, status.Errorf(codes.InvalidArgument, "date %q isn't YYYY-MM-DD", date)
		}
	}
	midnight := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	p := period{span: span}
	// next steps a bucket's start to the next one's.
	var next func(time.Time) time.Time
	switch span {
	case pb.Span_SPAN_DAY:
		p.from, p.to = midnight, midnight.AddDate(0, 0, 1)
		p.window, p.curve = 5*time.Minute, true
		// In absolute time: a day with a DST change has 23 or 25 hours.
		next = func(t time.Time) time.Time { return t.Add(time.Hour) }
	case pb.Span_SPAN_WEEK:
		monday := midnight.AddDate(0, 0, -(int(midnight.Weekday())+6)%7)
		p.from, p.to = monday, monday.AddDate(0, 0, 7)
		p.window, p.curve = 15*time.Minute, true
		next = func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }
	case pb.Span_SPAN_MONTH:
		p.from = time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, loc)
		p.to = p.from.AddDate(0, 1, 0)
		p.window = time.Hour
		next = func(t time.Time) time.Time { return t.AddDate(0, 0, 1) }
	case pb.Span_SPAN_YEAR:
		p.from = time.Date(d.Year(), 1, 1, 0, 0, 0, 0, loc)
		p.to = p.from.AddDate(1, 0, 0)
		p.window = time.Hour
		next = func(t time.Time) time.Time { return t.AddDate(0, 1, 0) }
	default:
		return period{}, status.Error(codes.InvalidArgument, "span must be a day, week, month or year")
	}
	for t := p.from; t.Before(p.to); t = next(t) {
		p.edges = append(p.edges, t)
	}
	p.edges = append(p.edges, p.to)
	return p, nil
}

// previous and next are a day in the period before and after it.
func (p period) previous() string { return p.from.AddDate(0, 0, -1).Format(day) }
func (p period) next() string     { return p.to.Format(day) }

// bucket is the index of the bucket t lies in, or -1 outside the period.
func (p period) bucket(t time.Time) int {
	if t.Before(p.from) || !t.Before(p.to) {
		return -1
	}
	// Few buckets (at most 31): a scan is fine.
	for i := 1; i < len(p.edges); i++ {
		if t.Before(p.edges[i]) {
			return i - 1
		}
	}
	return -1
}
