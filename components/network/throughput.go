package network

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// SampleInterval is how often network reads the WAN and LAN counters
// while someone watches: GetThroughput was asked within WatchFor.
const SampleInterval = 5 * time.Second

// WatchFor is how long after a GetThroughput network keeps sampling every
// SampleInterval. The Network Page and the Widget ask every 5 s.
const WatchFor = 30 * time.Second

// IdleInterval is how often network reads the counters while nobody
// watches: often enough for the hour's chart to have a line, and for the
// first rate shown on opening the Page to be a recent one.
const IdleInterval = time.Minute

// RingSize is how many samples network keeps: an hour of them at
// SampleInterval.
const RingSize = 720

// sampleTimeout bounds one read of the counters.
const sampleTimeout = 4 * time.Second

// throughput is the ring of WAN and LAN samples, in memory only.
type throughput struct {
	// sampling serializes reads of the counters: the loop's and the one a
	// GetThroughput makes when it ends an idle stretch.
	sampling sync.Mutex
	mu       sync.Mutex
	asked    time.Time                     // the last GetThroughput
	tried    time.Time                     // the last read of the counters, failed or not
	samples  []*networkpb.ThroughputSample // oldest first, at most RingSize
	last     *counters                     // the previous read, which rates are taken against
	err      string                        // why the last read failed
}

// counters are the WAN and LAN byte counters at one moment.
type counters struct {
	at                           time.Time
	wanIn, wanOut, lanIn, lanOut uint64
}

// sample reads the counters and adds the rates since the previous read to
// the ring. A counter that went backwards was reset: that sample is
// dropped rather than shown as a spike.
func (t *throughput) sample(ctx context.Context, pfsense pfsenseapipb.PfsenseApiServiceClient) {
	ctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	now, err := readCounters(ctx, pfsense)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.tried = time.Now()
	if err != nil {
		t.err = err.Error()
		return
	}
	t.err = ""
	prev := t.last
	t.last = now
	if prev == nil || now.wanIn < prev.wanIn || now.wanOut < prev.wanOut || now.lanIn < prev.lanIn || now.lanOut < prev.lanOut {
		return
	}
	secs := now.at.Sub(prev.at).Seconds()
	if secs <= 0 {
		return
	}
	rate := func(cur, old uint64) float64 { return float64(cur-old) / secs }
	t.samples = append(t.samples, &networkpb.ThroughputSample{
		At:                   timestamppb.New(now.at),
		WanInBytesPerSecond:  rate(now.wanIn, prev.wanIn),
		WanOutBytesPerSecond: rate(now.wanOut, prev.wanOut),
		LanInBytesPerSecond:  rate(now.lanIn, prev.lanIn),
		LanOutBytesPerSecond: rate(now.lanOut, prev.lanOut),
	})
	if over := len(t.samples) - RingSize; over > 0 {
		t.samples = append(t.samples[:0:0], t.samples[over:]...)
	}
}

func readCounters(ctx context.Context, pfsense pfsenseapipb.PfsenseApiServiceClient) (*counters, error) {
	resp, err := pfsense.ListInterfaces(ctx, &pfsenseapipb.ListInterfacesRequest{})
	if err != nil {
		return nil, fmt.Errorf("can't read the interface counters: %s", status.Convert(err).Message())
	}
	// The counters' own read time: pfsense-api may answer from its cache.
	at := time.Now()
	if r := resp.GetReadAt(); r != nil {
		at = r.AsTime()
	}
	byName := map[string]*pfsenseapipb.Interface{}
	for _, i := range resp.GetInterfaces() {
		byName[i.GetName()] = i
	}
	wan, lan := byName["wan"], byName["lan"]
	if wan == nil {
		return nil, fmt.Errorf("pfSense reports no wan interface")
	}
	if lan == nil {
		return nil, fmt.Errorf("pfSense reports no lan interface")
	}
	return &counters{at: at, wanIn: wan.GetInBytes(), wanOut: wan.GetOutBytes(), lanIn: lan.GetInBytes(), lanOut: lan.GetOutBytes()}, nil
}

// tick is the sampling loop's turn, every SampleInterval: it samples while
// someone watches, and every IdleInterval while nobody does.
func (t *throughput) tick(ctx context.Context, pfsense pfsenseapipb.PfsenseApiServiceClient) {
	t.sampling.Lock()
	defer t.sampling.Unlock()
	t.mu.Lock()
	now := time.Now()
	// Half an interval of slack: the loop's ticks carry jitter.
	due := now.Sub(t.asked) < WatchFor || now.Sub(t.tried) >= IdleInterval-SampleInterval/2
	t.mu.Unlock()
	if due {
		t.sample(ctx, pfsense)
	}
}

// watch notes that GetThroughput was asked. The first one after an idle
// stretch reads the counters before it answers, so the Operator sees a
// rate at once and not at the loop's next turn.
func (t *throughput) watch(ctx context.Context, pfsense pfsenseapipb.PfsenseApiServiceClient) {
	t.mu.Lock()
	now := time.Now()
	idle := now.Sub(t.asked) >= WatchFor
	t.asked = now
	t.mu.Unlock()
	if !idle {
		return
	}
	t.sampling.Lock()
	defer t.sampling.Unlock()
	t.mu.Lock()
	fresh := time.Since(t.tried) < SampleInterval
	t.mu.Unlock()
	if !fresh {
		t.sample(ctx, pfsense)
	}
}

// get returns the samples taken after since (all of them when nil).
func (t *throughput) get(since *timestamppb.Timestamp) *networkpb.GetThroughputResponse {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := &networkpb.GetThroughputResponse{Error: t.err}
	// Sampled once a minute, the ring reaches back further than the hour
	// it is for.
	from := time.Now().Add(-time.Hour)
	for _, s := range t.samples {
		at := s.GetAt().AsTime()
		if at.After(from) && (since == nil || at.After(since.AsTime())) {
			out.Samples = append(out.Samples, s)
		}
	}
	return out
}
