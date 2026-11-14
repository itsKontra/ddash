package energyapi

import (
	"math"
	"testing"
	"time"
)

var t0 = time.Date(2026, 4, 28, 17, 0, 0, 0, time.UTC)

// readings are a counter's values every 30 s from t0.
func readings(values ...float64) []point {
	out := make([]point, len(values))
	for i, v := range values {
		out[i] = point{at: t0.Add(time.Duration(i) * 30 * time.Second), v: v}
	}
	return out
}

func total(ss []step) float64 {
	var sum float64
	for _, s := range ss {
		sum += s.kwh
	}
	return math.Round(sum*100) / 100
}

func TestSteps(t *testing.T) {
	for _, tc := range []struct {
		name   string
		points []point
		want   float64
	}{
		{"counts each increase", readings(100, 100.01, 100.03, 100.06), 0.06},
		{"leaves out a spike and its return", readings(100, 100.01, 21460626.01, 21460626.02, 100.03, 100.04), 0.04},
		{"leaves out a drop to zero and its return", readings(100, 100.01, 0, 0, 100.03), 0.03},
		{"keeps the anchor through jitter backwards", readings(100, 100.01, 100.0, 100.02), 0.02},
		{"leaves out a step faster than 25 kW", readings(100, 100.5, 100.01), 0.01},
		{
			"counts a gap's energy when it fits",
			[]point{{t0, 100}, {t0.Add(4 * time.Hour), 104.34}, {t0.Add(4*time.Hour + 30*time.Second), 104.35}},
			4.35,
		},
		{"only anchors on the first reading", readings(100), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := total(steps(tc.points)); got != tc.want {
				t.Errorf("total = %v, want %v", got, tc.want)
			}
		})
	}
}

// A counter that restarts lower (a replaced meter) is followed again once
// its new readings have agreed with each other for an hour; the jump
// itself counts nothing.
func TestStepsReanchorAfterAnHour(t *testing.T) {
	var points []point
	for i := range 10 {
		points = append(points, point{at: t0.Add(time.Duration(i) * time.Minute), v: 5000 + float64(i)*0.01})
	}
	for i := range 90 {
		points = append(points, point{at: t0.Add(time.Duration(10+i) * time.Minute), v: 1 + float64(i)*0.01})
	}

	got := steps(points)

	// 9 steps before the restart, and the steps after the hour it took to
	// trust the new course (minutes 70 to 99).
	if n := total(got); n != 0.09+0.29 {
		t.Errorf("total = %v, want %v", n, 0.09+0.29)
	}
	if first := got[9].at; first.Sub(t0) != 71*time.Minute {
		t.Errorf("first step after the restart at +%v, want +71m", first.Sub(t0))
	}
}

// A glitch that starts the readings is left behind after an hour.
func TestStepsGlitchFirst(t *testing.T) {
	points := []point{{t0, 21460626}}
	for i := range 70 {
		points = append(points, point{at: t0.Add(time.Duration(1+i) * time.Minute), v: 100 + float64(i)*0.01})
	}

	if n := total(steps(points)); n > 0.1 {
		t.Errorf("total = %v, want only the steps after the hour", n)
	}
}
