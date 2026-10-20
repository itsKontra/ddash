package proxmoxapi

import (
	"crypto/x509"
	"testing"
)

// TrustRoots makes the Components started during t trust pool instead of
// the system's roots.
func TrustRoots(t *testing.T, pool *x509.CertPool) {
	old := rootCAs
	rootCAs = pool
	t.Cleanup(func() { rootCAs = old })
}
