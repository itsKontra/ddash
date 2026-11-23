package energyapi

import "time"

// point is one reading of a counter.
type point struct {
	at time.Time
	v  float64
}

// step is energy a counter counted, in kWh, by the time of its reading.
type step struct {
	at  time.Time
	kwh float64
}

const (
	// slack is a counter's resolution (0.01 kWh) with room to spare, which
	// a step may go backwards or beyond maxKW by.
	slack = 0.02
	// reanchor is how long rejected readings must agree with each other
	// before they are taken as the counter's new course, e.g. after the
	// meter was replaced. A glitch rarely lasts that long; one that does
	// costs the energy of its two jumps, never adds any.
	reanchor = time.Hour
)

// plausible tells whether a counter could have gone from a to b: never
// backwards, and no faster than maxKW. A reading after a gap may carry
// the gap's energy.
func plausible(a, b point) bool {
	d := b.v - a.v
	return d >= -slack && d <= maxKW*b.at.Sub(a.at).Hours()+slack
}

// steps turns a counter's readings, oldest first, into the energy it
// counted between them, leaving out implausible readings: each is
// measured from the last plausible one (the anchor), so a glitch and its
// return cost nothing. Readings that keep agreeing with each other but
// not with the anchor for reanchor become the new anchor. The first
// reading only anchors. Every plausible reading after it is a step, of
// 0 kWh when the counter stood still, so a step tells it was read.
func steps(points []point) []step {
	var out []step
	var anchor point
	var run []point
	for i, p := range points {
		if i == 0 {
			anchor = p
			continue
		}
		if plausible(anchor, p) {
			run = nil
			// Within slack below the anchor: jitter, keep the anchor.
			kwh := 0.0
			if p.v >= anchor.v {
				kwh = p.v - anchor.v
				anchor = p
			}
			out = append(out, step{at: p.at, kwh: kwh})
			continue
		}
		if len(run) > 0 && plausible(run[len(run)-1], p) {
			run = append(run, p)
		} else {
			run = []point{p}
		}
		if run[len(run)-1].at.Sub(run[0].at) >= reanchor {
			anchor, run = run[len(run)-1], nil
		}
	}
	return out
}
