package checks_test

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
)

// TestAnICMPCheckIsUpWhenTheHostAnswers needs unprivileged ICMP: CI sets
// net.ipv4.ping_group_range before go test (Docker does in containers).
func TestAnICMPCheckIsUpWhenTheHostAnswers(t *testing.T) {
	_, c := start(t)

	ch := firstBeat(t, c, create(t, c, &checkspb.CheckSpec{Type: checkspb.CheckType_CHECK_TYPE_ICMP, Target: "127.0.0.1"}).GetId())

	if ch.GetState() != checkspb.State_STATE_UP {
		t.Fatalf("state %v (%q), want up; if ICMP sockets are refused, allow them: sudo sysctl -w net.ipv4.ping_group_range='0 2147483647'", ch.GetState(), ch.GetReason())
	}
	if ch.GetLatencyMs() <= 0 {
		t.Errorf("latency %v ms, want the average round trip", ch.GetLatencyMs())
	}
}

// pro-bing waits forever by default; the probe always bounds it.
func TestAnICMPProbeOfAHostThatNeverAnswersEndsAtItsTimeout(t *testing.T) {
	begin := time.Now()

	up, reason := checks.PingFor("203.0.113.1", time.Second) // TEST-NET-3: nobody answers

	if up || reason == "" {
		t.Errorf("up %v (%q), want down with a reason", up, reason)
	}
	if took := time.Since(begin); took > 3*time.Second {
		t.Errorf("took %v, want it to end at its 1 s timeout", took)
	}
}

// dnsServer is a fake DNS server on a local UDP port. It knows
// jellyfin.home.example (A, MX), answers empty.home.example with NOERROR
// and no records, fails broken.home.example with SERVFAIL, and answers
// NXDOMAIN for anything else.
type dnsServer struct {
	addr    string
	queries atomic.Int32
}

func serveDNS(t *testing.T) *dnsServer {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	s := &dnsServer{addr: pc.LocalAddr().String()}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if reply := s.answer(buf[:n]); reply != nil {
				pc.WriteTo(reply, from)
			}
		}
	}()
	return s
}

func (s *dnsServer) answer(query []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(query)
	if err != nil {
		return nil
	}
	q, err := p.Question()
	if err != nil {
		return nil
	}
	s.queries.Add(1)
	rh := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 60}
	header := dnsmessage.Header{ID: h.ID, Response: true, RecursionAvailable: true}
	var answer func(*dnsmessage.Builder) error
	switch {
	case q.Name.String() == "jellyfin.home.example." && q.Type == dnsmessage.TypeA:
		answer = func(b *dnsmessage.Builder) error {
			return b.AResource(rh, dnsmessage.AResource{A: [4]byte{198, 18, 1, 10}})
		}
	case q.Name.String() == "jellyfin.home.example." && q.Type == dnsmessage.TypeMX:
		answer = func(b *dnsmessage.Builder) error {
			return b.MXResource(rh, dnsmessage.MXResource{Pref: 10, MX: dnsmessage.MustNewName("mail.home.example.")})
		}
	case q.Name.String() == "jellyfin.home.example.", q.Name.String() == "empty.home.example.":
	case q.Name.String() == "broken.home.example.":
		header.RCode = dnsmessage.RCodeServerFailure
	default:
		header.RCode = dnsmessage.RCodeNameError
	}
	b := dnsmessage.NewBuilder(nil, header)
	b.EnableCompression()
	if b.StartQuestions() != nil || b.Question(q) != nil || b.StartAnswers() != nil {
		return nil
	}
	if answer != nil && answer(&b) != nil {
		return nil
	}
	reply, err := b.Finish()
	if err != nil {
		return nil
	}
	return reply
}

func TestADNSCheckAsksItsServer(t *testing.T) {
	dns := serveDNS(t)
	_, c := start(t)
	dnsCheck := func(name string, rt checkspb.RecordType) *checkspb.Check {
		return firstBeat(t, c, create(t, c, &checkspb.CheckSpec{
			Type:   checkspb.CheckType_CHECK_TYPE_DNS,
			Target: dns.addr,
			Dns:    &checkspb.DnsQuery{Name: name, RecordType: rt},
		}).GetId())
	}

	for _, tc := range []struct {
		name   string
		rt     checkspb.RecordType
		up     bool
		reason string
	}{
		{"jellyfin.home.example", checkspb.RecordType_RECORD_TYPE_UNSPECIFIED, true, ""},
		{"jellyfin.home.example", checkspb.RecordType_RECORD_TYPE_MX, true, ""},
		{"jellyfin.home.example", checkspb.RecordType_RECORD_TYPE_TXT, false, "no answer"},
		{"empty.home.example", checkspb.RecordType_RECORD_TYPE_A, false, "no answer"},
		{"gone.home.example", checkspb.RecordType_RECORD_TYPE_A, false, "no answer"},
		{"broken.home.example", checkspb.RecordType_RECORD_TYPE_A, false, "server misbehaving"},
	} {
		ch := dnsCheck(tc.name, tc.rt)
		if up := ch.GetState() == checkspb.State_STATE_UP; up != tc.up || ch.GetReason() != tc.reason {
			t.Errorf("%s %v: state %v (%q), want up=%v (%q)", tc.name, tc.rt, ch.GetState(), ch.GetReason(), tc.up, tc.reason)
		}
	}
	if dns.queries.Load() == 0 {
		t.Error("the server was never asked")
	}
}

func TestADNSCheckAsksForAByDefault(t *testing.T) {
	_, c := start(t)

	ch := create(t, c, &checkspb.CheckSpec{Type: checkspb.CheckType_CHECK_TYPE_DNS, Target: "198.18.1.1", Dns: &checkspb.DnsQuery{Name: "jellyfin.home.example"}})

	if got := ch.GetSpec().GetDns().GetRecordType(); got != checkspb.RecordType_RECORD_TYPE_A {
		t.Errorf("record type %v, want A", got)
	}
}
