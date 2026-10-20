package checks

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"github.com/itsKontra/ddash/components/checks/checkspb"
)

// dialFunc opens a connection; net.Dialer.DialContext in production. TCP
// and DNS probes go through it.
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// result is one probe's outcome.
type result struct {
	up      bool
	latency time.Duration
	// reason says why a failed probe failed, e.g. "timeout".
	reason string
}

func failed(reason string) result { return result{reason: reason} }

// probe runs one beat's probe of s, within Timeout.
func (c *component) probe(ctx context.Context, s spec) result {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	switch s.typ {
	case checkspb.CheckType_CHECK_TYPE_TCP:
		return c.probeTCP(ctx, s.target)
	case checkspb.CheckType_CHECK_TYPE_ICMP:
		return probeICMP(ctx, s.target, Timeout)
	case checkspb.CheckType_CHECK_TYPE_DNS:
		return c.probeDNS(ctx, s)
	}
	return failed("unknown type " + s.typ.String())
}

// probeTCP is up when a connection to target (host:port) opens. Its
// latency is the connect time.
func (c *component) probeTCP(ctx context.Context, target string) result {
	begin := time.Now()
	conn, err := c.dial(ctx, "tcp", target)
	if err != nil {
		return failed(reason(ctx, err))
	}
	latency := time.Since(begin)
	conn.Close()
	return result{up: true, latency: latency}
}

// Echoes is how many echo requests an ICMP probe sends, a second apart.
const Echoes = 3

// probeICMP is up when target answers any of Echoes echo requests within
// timeout, with the average round trip as latency. It uses unprivileged
// ICMP (datagram) sockets, which need net.ipv4.ping_group_range to cover
// the process's group; Docker sets it in every bridge-networked container.
func probeICMP(ctx context.Context, target string, timeout time.Duration) result {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, target)
	if err != nil {
		return failed(reason(ctx, err))
	}
	p := probing.New(target)
	p.SetLogger(probing.NoopLogger{})
	p.SetPrivileged(false)
	p.SetIPAddr(&addrs[0])
	p.Count = Echoes
	// pro-bing's default timeout is forever: a dead host would hang the probe.
	p.Timeout = timeout
	if err := p.RunWithContext(ctx); err != nil && ctx.Err() == nil {
		return failed(reason(ctx, err))
	}
	if st := p.Statistics(); st.PacketsRecv > 0 {
		return result{up: true, latency: st.AvgRtt}
	}
	if ctx.Err() != nil {
		return failed("timeout")
	}
	return failed("no reply")
}

// probeDNS asks the server in s.target for s.dnsName with Go's own
// resolver, and is up on NOERROR with at least one record. Its latency is
// the lookup's duration.
func (c *component) probeDNS(ctx context.Context, s spec) result {
	server := s.target
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(strings.Trim(server, "[]"), "53")
	}
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return c.dial(ctx, network, server)
		},
	}
	// Rooted, so the search domains of the host's resolv.conf aren't tried.
	name := strings.TrimSuffix(s.dnsName, ".") + "."
	begin := time.Now()
	var n int
	var err error
	switch s.recordType {
	case checkspb.RecordType_RECORD_TYPE_AAAA:
		n, err = count(r.LookupNetIP(ctx, "ip6", name))
	case checkspb.RecordType_RECORD_TYPE_MX:
		n, err = count(r.LookupMX(ctx, name))
	case checkspb.RecordType_RECORD_TYPE_NS:
		n, err = count(r.LookupNS(ctx, name))
	case checkspb.RecordType_RECORD_TYPE_TXT:
		n, err = count(r.LookupTXT(ctx, name))
	default:
		n, err = count(r.LookupNetIP(ctx, "ip4", name))
	}
	latency := time.Since(begin)
	switch {
	case err != nil:
		return failed(reason(ctx, err))
	case n == 0:
		return failed("no answer")
	}
	return result{up: true, latency: latency}
}

// count counts a lookup's answers.
func count[T any](answers []T, err error) (int, error) { return len(answers), err }

// reason says in a few words why a probe failed.
func reason(ctx context.Context, err error) string {
	var dnsErr *net.DNSError
	var netErr net.Error
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.EHOSTUNREACH):
		return "host unreachable"
	case errors.Is(err, syscall.ENETUNREACH):
		return "network unreachable"
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		// NXDOMAIN, or NOERROR without records: Go doesn't tell them apart.
		return "no answer"
	case errors.As(err, &dnsErr):
		return dnsErr.Err
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Err.Error()
	}
	return err.Error()
}
