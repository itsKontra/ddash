package checks

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/checks/checkspb"
)

// spec is what the Operator set on a Check.
type spec struct {
	name       string
	typ        checkspb.CheckType
	target     string
	dnsName    string
	recordType checkspb.RecordType
}

// probes is what is probed: s without its name.
func (s spec) probes() spec {
	s.name = ""
	return s
}

// shownName is the name, or the target for a Check without one.
func (s spec) shownName() string {
	if s.name != "" {
		return s.name
	}
	return s.target
}

// parseSpec checks what the Operator sent and normalises it: DNS fields
// only on a DNS Check, A when no record type is set.
func parseSpec(p *checkspb.CheckSpec) (spec, error) {
	s := spec{
		name:   strings.TrimSpace(p.GetName()),
		typ:    p.GetType(),
		target: strings.TrimSpace(p.GetTarget()),
	}
	var problem string
	switch s.typ {
	case checkspb.CheckType_CHECK_TYPE_TCP:
		host, port, err := net.SplitHostPort(s.target)
		if n, perr := strconv.Atoi(port); err != nil || !validHost(host) || perr != nil || n < 1 || n > 65535 {
			problem = "target must be host:port, e.g. 198.18.1.10:22"
		}
	case checkspb.CheckType_CHECK_TYPE_ICMP:
		if !validHost(s.target) {
			problem = "target must be a host name or IP address"
		}
	case checkspb.CheckType_CHECK_TYPE_DNS:
		s.dnsName = strings.TrimSpace(p.GetDns().GetName())
		s.recordType = p.GetDns().GetRecordType()
		if s.recordType == checkspb.RecordType_RECORD_TYPE_UNSPECIFIED {
			s.recordType = checkspb.RecordType_RECORD_TYPE_A
		}
		host := s.target
		if h, port, err := net.SplitHostPort(s.target); err == nil {
			if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
				h = ""
			}
			host = h
		}
		switch {
		case !validHost(host):
			problem = "target must be the DNS server's address, e.g. 198.18.1.1 or 198.18.1.1:5353"
		case !validHost(strings.TrimSuffix(s.dnsName, ".")):
			problem = "dns.name must be the name to resolve, e.g. jellyfin.home.example"
		case checkspb.RecordType_name[int32(s.recordType)] == "":
			problem = fmt.Sprintf("dns.record_type %d is unknown", s.recordType)
		}
	default:
		problem = "type must be TCP, ICMP or DNS"
	}
	if problem != "" {
		return spec{}, status.Error(codes.InvalidArgument, problem)
	}
	return s, nil
}

// validHost is true for an IP address or a host name.
func validHost(h string) bool {
	if net.ParseIP(strings.Trim(h, "[]")) != nil {
		return true
	}
	if h == "" || len(h) > 253 {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		if strings.IndexFunc(label, notHostChar) >= 0 {
			return false
		}
	}
	return true
}

// notHostChar is true for a rune no host name label has.
func notHostChar(r rune) bool {
	letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	digit := r >= '0' && r <= '9'
	return !letter && !digit && r != '-' && r != '_'
}

// standing is where a Check stands after its beats so far.
type standing struct {
	state    checkspb.State
	failures int
	since    time.Time
	// lastBeat is zero before the first beat.
	lastBeat time.Time
	latency  time.Duration
	reason   string
	// certExpires is when the certificate seen by the last up HTTPS beat
	// expires; zero without one.
	certExpires time.Time
}

// after is s after a beat at at with result r: up on one success, down
// after DownAfter failures in a row, pending in between.
func (s standing) after(r result, at time.Time) standing {
	n := s
	n.lastBeat = at
	next := s.state
	if r.up {
		n.failures, n.latency, n.reason, n.certExpires = 0, r.latency, "", r.certExpires
		next = checkspb.State_STATE_UP
	} else {
		n.failures, n.latency, n.reason = s.failures+1, 0, r.reason
		switch {
		case s.state == checkspb.State_STATE_DOWN:
		case n.failures >= DownAfter:
			next = checkspb.State_STATE_DOWN
		default:
			next = checkspb.State_STATE_PENDING
		}
	}
	if next != s.state {
		n.state, n.since = next, at
	}
	return n
}

func (ch *check) proto() *checkspb.Check {
	p := &checkspb.Check{
		Id: ch.id,
		Spec: &checkspb.CheckSpec{
			Name:   ch.spec.name,
			Type:   ch.spec.typ,
			Target: ch.spec.target,
		},
		State:     ch.standing.state,
		Since:     timestamppb.New(ch.standing.since),
		LatencyMs: float64(ch.standing.latency) / float64(time.Millisecond),
		Reason:    ch.standing.reason,
		Failures:  int32(ch.standing.failures),
		Muted:     ch.muted,
		Disabled:  ch.disabled || !ch.appEnabled,
		AppId:     ch.appID,
		Workload:  ch.workload.proto(),
	}
	if exp := ch.standing.certExpires; !exp.IsZero() {
		p.CertExpires = timestamppb.New(exp)
		p.CertExpiring = time.Until(exp) < CertWarning
	}
	if ch.spec.typ == checkspb.CheckType_CHECK_TYPE_DNS {
		p.Spec.Dns = &checkspb.DnsQuery{Name: ch.spec.dnsName, RecordType: ch.spec.recordType}
	}
	if !ch.standing.lastBeat.IsZero() {
		p.LastBeat = timestamppb.New(ch.standing.lastBeat)
	}
	return p
}
