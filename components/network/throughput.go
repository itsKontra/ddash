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

// SampleInterval is how often network reads the WAN and LAN counters,
// whether anyone is watching or not.
const SampleInterval = 5 * time.Second

// RingSize is how many samples network keeps: an hour of them.
const RingSize = 720

// sampleTimeout bounds one read of the counters.
const sampleTimeout = 4 * time.Second

// throughput is the ring of WAN and LAN samples, in memory only.
type throughput struct {
	mu      sync.Mutex
	samples []*networkpb.ThroughputSample // oldest first, at most RingSize
	last    *counters                     // the previous read, which rates are taken against
	err     string                        // why the last read failed
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

// get returns the samples taken after since (all of them when nil).
func (t *throughput) get(since *timestamppb.Timestamp) *networkpb.GetThroughputResponse {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := &networkpb.GetThroughputResponse{Error: t.err}
	for _, s := range t.samples {
		if since == nil || s.GetAt().AsTime().After(since.AsTime()) {
			out.Samples = append(out.Samples, s)
		}
	}
	return out
}
