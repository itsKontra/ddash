package proxmoxapi

import (
	"crypto/x509"
	"testing"
	"time"
)

// TrustRoots makes the Components started during t trust pool instead of
// the system's roots.
func TrustRoots(t *testing.T, pool *x509.CertPool) {
	old := rootCAs
	rootCAs = pool
	t.Cleanup(func() { rootCAs = old })
}

// PollTasksEvery makes the power Actions started during t read their
// task's status every d.
func PollTasksEvery(t *testing.T, d time.Duration) {
	old := taskPollInterval
	taskPollInterval = d
	t.Cleanup(func() { taskPollInterval = old })
}
