package checks

import (
	"context"
	"crypto/x509"
	"net"
	"time"

	"github.com/itsKontra/ddash/sdk"
)

// DefinitionTrusting is checks trusting only roots for HTTPS.
func DefinitionTrusting(roots *x509.CertPool) sdk.Definition {
	return define((&net.Dialer{}).DialContext, roots)
}

// PingFor runs an ICMP probe of target bounded only by timeout.
func PingFor(target string, timeout time.Duration) (up bool, reason string) {
	r := probeICMP(context.Background(), target, timeout)
	return r.up, r.reason
}
