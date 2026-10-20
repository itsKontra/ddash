// Package nginxconfig is the nginx-config Component: it reads nginx's
// effective config with `nginx -T` over SSH, using a key pair it generates
// on first Start and an authorized_keys line that lets that key run nothing
// else, and keeps the parsed server blocks in memory as a snapshot with a
// revision, re-read hourly and on Refresh.
package nginxconfig

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
	configpb "github.com/itsKontra/ddash/proto/ddash/config"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

//go:embed migrations/*.sql
var migrations embed.FS

// rereadEvery is how often the config is read without a Refresh.
const rereadEvery = time.Hour

// command is what the authorized_keys line forces the key to run.
const command = "/usr/sbin/nginx -T"

// readTimeout bounds one read, dial to exit, below the host's getter deadline.
const readTimeout = 8 * time.Second

// Definition is nginx-config, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*nginxconfigpb.NginxConfigConfig]{
		Manifest:   manifest,
		Service:    &nginxconfigpb.NginxConfigService_ServiceDesc,
		Migrations: migrations,
		New:        func() sdk.Component[*nginxconfigpb.NginxConfigConfig] { return &component{} },
	})
}

type component struct {
	nginxconfigpb.UnimplementedNginxConfigServiceServer

	host    string // as configured, for the hint
	addr    string // host:port to dial
	user    string
	hostKey ssh.PublicKey
	signer  ssh.Signer

	// readMu makes reads take turns.
	readMu sync.Mutex

	mu   sync.Mutex
	snap state
}

// state is the snapshot and how the last read went.
type state struct {
	attempted bool
	revision  int64
	// fingerprint is what the last read saw: its dump, or why it failed.
	fingerprint [sha256.Size]byte
	readAt      time.Time
	servers     []*nginxconfigpb.Server
	upstreams   []*nginxconfigpb.Upstream
	err         string
	// down is set when the last read couldn't reach nginx or sign in;
	// a config nginx refuses is not down.
	down bool
}

func (c *component) Start(ctx context.Context, env *sdk.Env, cfg *nginxconfigpb.NginxConfigConfig) error {
	if err := c.configure(cfg); err != nil {
		return err
	}
	signer, err := loadKey(ctx, env.DB)
	if err != nil {
		return err
	}
	c.signer = signer
	sdk.Every(ctx, rereadEvery, func(ctx context.Context) { c.read(ctx) })
	return nil
}

func (c *component) configure(cfg *nginxconfigpb.NginxConfigConfig) error {
	var errs sdk.FieldErrors
	c.host, c.user = cfg.GetHost(), cfg.GetUser()
	if addr, err := dialAddr(c.host); err != nil {
		errs.Add("host", "%s", err)
	} else {
		c.addr = addr
	}
	if key, err := parseHostKey(cfg.GetHostKey()); err != nil {
		errs.Add("host_key", "must be a public key like ssh-ed25519 AAAA…, as ssh-keyscan prints it")
	} else {
		c.hostKey = key
	}
	return errs.Err()
}

// dialAddr adds SSH's port 22 to a host without one.
func dialAddr(host string) (string, error) {
	if host == "" {
		return "", errors.New("is required")
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return net.JoinHostPort(host, "22"), nil
	}
	if !strings.Contains(host, ":") {
		return net.JoinHostPort(host, "22"), nil
	}
	h, port, err := net.SplitHostPort(host)
	if n, perr := strconv.Atoi(port); err != nil || h == "" || perr != nil || n < 1 || n > 65535 {
		return "", errors.New("must be a host, or host:port with a port number")
	}
	return host, nil
}

// parseHostKey reads an authorized_keys-style "ssh-ed25519 AAAA…" or a
// known_hosts line "host ssh-ed25519 AAAA…".
func parseHostKey(text string) (ssh.PublicKey, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(text))
	if err == nil {
		return key, nil
	}
	_, _, key, _, _, err = ssh.ParseKnownHosts([]byte(text))
	return key, err
}

func (c *component) Health(ctx context.Context) sdk.Health {
	s := c.state()
	if !s.attempted || s.err != "" {
		// A failed read is retried on every poll, so a key the Operator
		// has just authorized, or a fixed config, shows at once.
		s = c.read(ctx)
	}
	switch {
	case s.down:
		return sdk.Health{State: sdk.Down, Message: s.err}
	case s.err != "":
		first, _, _ := strings.Cut(s.err, "\n")
		return sdk.Health{State: sdk.Degraded, Message: "nginx config test failed: " + first}
	}
	return sdk.Health{State: sdk.OK}
}

func (c *component) GetSnapshot(ctx context.Context, _ *nginxconfigpb.GetSnapshotRequest) (*nginxconfigpb.GetSnapshotResponse, error) {
	s := c.state()
	if !s.attempted {
		s = c.read(ctx)
	}
	return &nginxconfigpb.GetSnapshotResponse{Snapshot: s.proto()}, nil
}

func (c *component) Refresh(ctx context.Context, _ *nginxconfigpb.RefreshRequest) (*nginxconfigpb.RefreshResponse, error) {
	return &nginxconfigpb.RefreshResponse{Snapshot: c.read(ctx).proto()}, nil
}

func (c *component) GetSetupHint(context.Context, *nginxconfigpb.GetSetupHintRequest) (*nginxconfigpb.GetSetupHintResponse, error) {
	ip := sourceIP(c.addr)
	key := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(c.signer.PublicKey())))
	return &nginxconfigpb.GetSetupHintResponse{Hint: &configpb.SetupHint{
		Text: fmt.Sprintf("Add this line to %s's ~/.ssh/authorized_keys on %s. It lets ddash's key run `nginx -T` and nothing else, and only from %s, the address ddash reaches %s from.",
			c.user, c.host, ip, c.host),
		Block: fmt.Sprintf(`restrict,from="%s",command="%s" %s ddash`, ip, command, key),
	}}, nil
}

// sourceIP is the address ddash's packets to addr leave from, as nginx's
// host sees it unless NAT sits between them.
func sourceIP(addr string) string {
	conn, err := net.Dial("udp", addr) // sends nothing; picks the route
	if err != nil {
		return "<ddash-ip>"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func (c *component) state() state {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snap
}

// read reads nginx's config now and records the outcome.
func (c *component) read(ctx context.Context) state {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()

	var conf parsed
	stdout, stderr, exit, err := c.run(ctx)
	down := err != nil
	if err == nil && exit != 0 {
		err = errors.New(cmpOr(strings.TrimSpace(stderr), fmt.Sprintf("nginx -T exited %d", exit)))
	}
	if err == nil {
		var d dump
		if d, err = split(stdout); err == nil {
			conf, err = d.parse()
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	s := &c.snap
	s.attempted = true
	var fp [sha256.Size]byte
	if err != nil {
		fp = sha256.Sum256([]byte("error\x00" + err.Error()))
		s.err, s.down = err.Error(), down
	} else {
		fp = sha256.Sum256([]byte("config\x00" + stdout))
		s.err, s.down = "", false
		s.readAt, s.servers, s.upstreams = time.Now(), conf.servers, conf.upstreams
	}
	if fp != s.fingerprint {
		s.fingerprint = fp
		s.revision++
	}
	return *s
}

func (s state) proto() *nginxconfigpb.Snapshot {
	out := &nginxconfigpb.Snapshot{Revision: s.revision, Error: s.err, Servers: s.servers, Upstreams: s.upstreams}
	if !s.readAt.IsZero() {
		out.ReadAt = timestamppb.New(s.readAt)
	}
	return out
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// loadKey returns the Component's key pair, generating and storing it on
// first Start.
func loadKey(ctx context.Context, db *sql.DB) (ssh.Signer, error) {
	var seed []byte
	err := db.QueryRowContext(ctx, `SELECT seed FROM key WHERE id = 1`).Scan(&seed)
	if errors.Is(err, sql.ErrNoRows) {
		seed, err = newSeed()
		if err == nil {
			_, err = db.ExecContext(ctx, `INSERT INTO key (id, seed) VALUES (1, ?)`, seed)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("ssh key: %w", err)
	}
	return signerFromSeed(seed)
}
