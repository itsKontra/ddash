package checks

import (
	"context"
	"net"
	"time"

	"github.com/itsKontra/ddash/sdk"
)

// DefinitionDialing is checks with TCP and DNS probes dialing through dial,
// so time-rule tests can fake the targets' transport inside a synctest
// bubble.
func DefinitionDialing(dial func(ctx context.Context, network, addr string) (net.Conn, error)) sdk.Definition {
	return define(dial)
}

// PingFor runs an ICMP probe of target bounded only by timeout.
func PingFor(target string, timeout time.Duration) (up bool, reason string) {
	r := probeICMP(context.Background(), target, timeout)
	return r.up, r.reason
}
