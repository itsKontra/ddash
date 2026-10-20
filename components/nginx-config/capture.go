package nginxconfig

import (
	"context"
	"fmt"
	"os"

	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	"github.com/itsKontra/ddash/sdk/migrate"
)

// Capture runs `nginx -T` on the host cfg names, as the Component whose DB
// is at dbPath would: with its key, pinning the host key. It is how `make
// record` captures the dump for the fixtures; the Component must have
// started once (it made its key) and its key must be authorized. err is set
// only when nginx didn't run.
func Capture(ctx context.Context, dbPath string, cfg *nginxconfigpb.NginxConfigConfig) (stdout, stderr string, exit int, err error) {
	noKey := fmt.Errorf("nginx-config has no key yet in %s: start it once and authorize its key", dbPath)
	if _, err := os.Stat(dbPath); err != nil {
		return "", "", 0, noKey
	}
	db, err := migrate.Open(dbPath)
	if err != nil {
		return "", "", 0, err
	}
	defer db.Close()
	var seed []byte
	if err := db.QueryRowContext(ctx, `SELECT seed FROM key WHERE id = 1`).Scan(&seed); err != nil {
		return "", "", 0, noKey
	}
	c := &component{}
	if err := c.configure(cfg); err != nil {
		return "", "", 0, err
	}
	if c.signer, err = signerFromSeed(seed); err != nil {
		return "", "", 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	return c.run(ctx)
}
