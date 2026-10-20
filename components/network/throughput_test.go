package network_test

import (
	"math"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// The steady rates traffic runs at, in bytes per second.
const (
	wanIn  = 2_500_000
	wanOut = 250_000
	lanIn  = 300_000
	lanOut = 2_400_000
)

// traffic makes the WAN and LAN counters grow at the steady rates, the
// way pfSense's cumulative counters do. They start high, like a firewall
// up for weeks.
type traffic struct {
	mu    sync.Mutex
	from  time.Time
	start uint64
	// lags are how old the counters of successive reads are, in turn (as
	// pfsense-api's cache can serve them); readAt the last read's time.
	lags   []time.Duration
	reads  int
	readAt time.Time
}

func newTraffic() *traffic { return &traffic{from: time.Now(), start: 900_000_000_000} }

// reset starts every counter again from zero, as an interface reset does.
func (tr *traffic) reset() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.from, tr.start = time.Now(), 0
}

func (tr *traffic) interfaces() []*pfsenseapipb.Interface {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.readAt = time.Now()
	if len(tr.lags) > 0 {
		tr.readAt = tr.readAt.Add(-tr.lags[tr.reads%len(tr.lags)])
	}
	tr.reads++
	secs := tr.readAt.Sub(tr.from).Seconds()
	at := func(rate float64) uint64 { return tr.start + uint64(rate*secs) }
	return []*pfsenseapipb.Interface{
		{Name: "wan", Description: "WAN", InBytes: at(wanIn), OutBytes: at(wanOut)},
		{Name: "lan", Description: "LAN", InBytes: at(lanIn), OutBytes: at(lanOut)},
		{Name: "opt1", Description: "REMOTEACCESS", InBytes: at(1000), OutBytes: at(9000)},
	}
}

// at is when the counters of the last read were taken.
func (tr *traffic) at() time.Time {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.readAt
}

func throughput(t *testing.T, c networkpb.NetworkServiceClient, since *timestamppb.Timestamp) *networkpb.GetThroughputResponse {
	t.Helper()
	resp, err := c.GetThroughput(t.Context(), &networkpb.GetThroughputRequest{Since: since})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// steady reports whether s holds the steady rates (to a byte per second:
// counters are whole bytes).
func steady(s *networkpb.ThroughputSample) bool {
	near := func(got, want float64) bool { return math.Abs(got-want) < 1 }
	return near(s.GetWanInBytesPerSecond(), wanIn) && near(s.GetWanOutBytesPerSecond(), wanOut) &&
		near(s.GetLanInBytesPerSecond(), lanIn) && near(s.GetLanOutBytesPerSecond(), lanOut)
}

// gaps returns the time between consecutive samples.
func gaps(samples []*networkpb.ThroughputSample) []time.Duration {
	var out []time.Duration
	for i := 1; i < len(samples); i++ {
		out = append(out, samples[i].GetAt().AsTime().Sub(samples[i-1].GetAt().AsTime()))
	}
	return out
}

// sampling is 5 s plus at most 10% jitter.
const maxGap = 5500 * time.Millisecond

func TestThroughputIsSampledEveryFiveSecondsWithNobodyWatching(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tr := newTraffic()
		_, c := start(t, &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: tr.interfaces})

		time.Sleep(time.Minute)
		synctest.Wait()
		got := throughput(t, c, nil)

		// The first read only sets where the counters stand.
		if n := len(got.GetSamples()); n < 10 || n > 12 {
			t.Fatalf("%d samples after a minute nobody watched, want 10 to 12", n)
		}
		for _, s := range got.GetSamples() {
			if !steady(s) {
				t.Errorf("sample %v, want WAN in %d out %d, LAN in %d out %d B/s", s, wanIn, wanOut, lanIn, lanOut)
			}
		}
		for _, g := range gaps(got.GetSamples()) {
			if g < 5*time.Second || g > maxGap {
				t.Errorf("samples %v apart, want 5 to 5.5 s", g)
			}
		}
		if got.GetError() != "" {
			t.Errorf("error = %q, want none", got.GetError())
		}
	})
}

// pfsense-api may answer from its 2 s cache: a rate divides by the time
// between the reads of the counters, not between the samples.
func TestRatesUseWhenTheCountersWereRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tr := newTraffic()
		tr.lags = []time.Duration{0, 2 * time.Second, time.Second}
		_, c := start(t, &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: tr.interfaces, readAt: tr.at})

		time.Sleep(time.Minute)
		synctest.Wait()

		for _, s := range throughput(t, c, nil).GetSamples() {
			if !steady(s) {
				t.Errorf("sample %v, want WAN in %d out %d, LAN in %d out %d B/s", s, wanIn, wanOut, lanIn, lanOut)
			}
		}
	})
}

func TestTheRingKeepsTheLast720Samples(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tr := newTraffic()
		_, c := start(t, &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: tr.interfaces})

		time.Sleep(2 * time.Hour)
		synctest.Wait()
		got := throughput(t, c, nil).GetSamples()

		if len(got) != 720 {
			t.Fatalf("%d samples after two hours, want 720", len(got))
		}
		if newest := time.Since(got[719].GetAt().AsTime()); newest > maxGap {
			t.Errorf("newest sample is %v old, want the latest", newest)
		}
		if oldest := time.Since(got[0].GetAt().AsTime()); oldest < time.Hour-maxGap || oldest > 720*maxGap {
			t.Errorf("oldest sample is %v old, want about an hour", oldest)
		}
	})
}

func TestGetThroughputReturnsOnlyTheSamplesAfterSince(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tr := newTraffic()
		_, c := start(t, &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: tr.interfaces})
		time.Sleep(time.Minute)
		synctest.Wait()
		all := throughput(t, c, nil).GetSamples()

		got := throughput(t, c, all[len(all)-3].GetAt()).GetSamples()

		if len(got) != 2 || !got[0].GetAt().AsTime().Equal(all[len(all)-2].GetAt().AsTime()) {
			t.Errorf("samples after the third newest = %v, want the two newest", got)
		}
		if got := throughput(t, c, all[len(all)-1].GetAt()).GetSamples(); len(got) != 0 {
			t.Errorf("samples after the newest = %v, want none", got)
		}
	})
}

// A counter that goes backwards was reset (pfSense rebooted, the interface
// came up again): that sample is dropped rather than shown as a spike.
func TestACounterResetDropsOneSampleInsteadOfMakingASpike(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tr := newTraffic()
		_, c := start(t, &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: tr.interfaces})
		time.Sleep(30 * time.Second)
		synctest.Wait()
		before := len(throughput(t, c, nil).GetSamples())

		tr.reset()
		time.Sleep(30 * time.Second)
		synctest.Wait()
		got := throughput(t, c, nil).GetSamples()

		for _, s := range got {
			if !steady(s) {
				t.Errorf("sample %v, want only the steady rates: no spike, no dip", s)
			}
		}
		var long []time.Duration
		for _, g := range gaps(got) {
			if g > maxGap {
				long = append(long, g)
			}
		}
		if len(long) != 1 || long[0] > 2*maxGap {
			t.Errorf("gaps over 5.5 s: %v, want one where the reset's sample was dropped", long)
		}
		if after := len(got) - before; after < 4 || after > 5 {
			t.Errorf("%d samples in the 30 s after the reset, want 4 or 5 (one dropped)", after)
		}
	})
}

func TestAFailedReadSaysWhyAndSamplingCarriesOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tr := newTraffic()
		fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: tr.interfaces}
		_, c := start(t, fw)
		time.Sleep(20 * time.Second)
		synctest.Wait()
		before := len(throughput(t, c, nil).GetSamples())

		fw.fail(status.Error(codes.Unavailable, "pfSense unreachable: connection refused"))
		time.Sleep(20 * time.Second)
		synctest.Wait()
		got := throughput(t, c, nil)
		if len(got.GetSamples()) != before || !strings.Contains(got.GetError(), "pfSense unreachable") {
			t.Errorf("while pfsense-api fails: %d samples (want %d), error %q (want pfsense-api's)", len(got.GetSamples()), before, got.GetError())
		}

		fw.fail(nil)
		time.Sleep(20 * time.Second)
		synctest.Wait()
		got = throughput(t, c, nil)
		if len(got.GetSamples()) <= before || got.GetError() != "" {
			t.Errorf("after pfsense-api recovered: %d samples, error %q, want new samples and no error", len(got.GetSamples()), got.GetError())
		}
		for _, s := range got.GetSamples() {
			if !steady(s) {
				t.Errorf("sample %v, want the steady rates (averaged over the gap)", s)
			}
		}
	})
}

func TestWithoutAWANInterfaceThereIsNoSampleAndThroughputSaysWhy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tr := newTraffic()
		lanOnly := func() []*pfsenseapipb.Interface { return tr.interfaces()[1:] }
		_, c := start(t, &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, interfaces: lanOnly})
		time.Sleep(20 * time.Second)
		synctest.Wait()

		got := throughput(t, c, nil)

		if len(got.GetSamples()) != 0 || !strings.Contains(got.GetError(), "no wan interface") {
			t.Errorf("throughput = %v, want no samples and an error naming the missing wan interface", got)
		}
	})
}
