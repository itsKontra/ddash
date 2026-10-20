package fixture

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// SSH is an SSH server answering commands from a scenario's files, until
// Close. It runs nothing: a command's answer is the files named after it,
// its program's base name and arguments joined by "_" (both "nginx -T" and
// "/usr/sbin/nginx -T" are nginx_-T):
//
//	nginx_-T.stdout   what it prints on stdout (absent: nothing)
//	nginx_-T.stderr   what it prints on stderr (absent: nothing)
//	nginx_-T.exit     its exit status in decimal (absent: 0)
//
// A scenario that has none of a command's files answers it from the sibling
// baseline. A command neither has exits 127. Clients sign in with a public
// key; it honours an authorized_keys line's from= and command= options as
// sshd does.
type SSH struct {
	ln     net.Listener
	config *ssh.ServerConfig
	dirs   []string
	signer ssh.Signer

	mu         sync.Mutex
	authorized []authorizedKey
	anyKey     bool
	hits       map[string]int
	conns      map[net.Conn]bool
	closed     bool
}

type authorizedKey struct {
	key     ssh.PublicKey
	from    []string
	command string
}

// SSHOptions shape an SSH server.
type SSHOptions struct {
	// HostKey is the server's key; nil generates one.
	HostKey ed25519.PrivateKey
	// AnyKey lets every client key in, as if each were authorized without
	// options (fakelab). Otherwise only keys given to Authorize are.
	AnyKey bool
}

// SSHServer serves the scenario in dir, e.g. "testdata/config-test-failed",
// on a free loopback port until the test ends.
func SSHServer(t testing.TB, dir string, opts SSHOptions) *SSH {
	t.Helper()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s, err := ServeSSH(ln, dir, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// ServeSSH serves the scenario in dir on ln, outside a test too: fakelab
// serves baseline with it.
func ServeSSH(ln net.Listener, dir string, opts SSHOptions) (*SSH, error) {
	key := opts.HostKey
	if key == nil {
		var err error
		if _, key, err = ed25519.GenerateKey(rand.Reader); err != nil {
			return nil, err
		}
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, err
	}
	s := &SSH{ln: ln, signer: signer, anyKey: opts.AnyKey, hits: map[string]int{}, conns: map[net.Conn]bool{}}
	s.dirs = scenarioDirs(dir)
	s.config = &ssh.ServerConfig{PublicKeyCallback: s.checkKey}
	s.config.AddHostKey(signer)
	go s.serve()
	return s, nil
}

// Addr is the server's host:port.
func (s *SSH) Addr() string { return s.ln.Addr().String() }

// HostKey is the server's public key as an authorized_keys line without a
// comment, e.g. "ssh-ed25519 AAAA…".
func (s *SSH) HostKey() string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.signer.PublicKey())))
}

// Authorize adds an authorized_keys line, options and all, e.g.
// `restrict,from="127.0.0.1",command="/usr/sbin/nginx -T" ssh-ed25519 AAAA… ddash`.
func (s *SSH) Authorize(line string) error {
	key, _, options, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return err
	}
	ak := authorizedKey{key: key}
	for _, o := range options {
		name, value, _ := strings.Cut(o, "=")
		if v, err := strconv.Unquote(value); err == nil {
			value = v
		}
		switch strings.ToLower(name) {
		case "from":
			ak.from = strings.Split(value, ",")
		case "command":
			ak.command = value
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authorized = append(s.authorized, ak)
	return nil
}

// Use answers from the scenario in dir from now on, e.g. after the
// Operator fixed the config.
func (s *SSH) Use(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirs = scenarioDirs(dir)
}

// Hits counts the runs of a command, e.g. "nginx -T", by what ran (a
// forced command counts, not the one asked for).
func (s *SSH) Hits(command string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[command]
}

// Close stops serving and drops every connection.
func (s *SSH) Close() {
	s.mu.Lock()
	s.closed = true
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.ln.Close()
}

func (s *SSH) checkKey(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.anyKey {
		return &ssh.Permissions{}, nil
	}
	ip := meta.RemoteAddr().(*net.TCPAddr).IP.String()
	for _, ak := range s.authorized {
		if !slices.Equal(ak.key.Marshal(), key.Marshal()) {
			continue
		}
		if ak.from != nil && !slices.Contains(ak.from, ip) {
			continue
		}
		return &ssh.Permissions{CriticalOptions: map[string]string{"force-command": ak.command}}, nil
	}
	return nil, fmt.Errorf("unknown key for %s", meta.User())
}

func (s *SSH) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			c.Close()
			return
		}
		s.conns[c] = true
		s.mu.Unlock()
		go func() {
			defer func() {
				s.mu.Lock()
				delete(s.conns, c)
				s.mu.Unlock()
				c.Close()
			}()
			s.handle(c)
		}()
	}
}

func (s *SSH) handle(c net.Conn) {
	conn, chans, reqs, err := ssh.NewServerConn(c, s.config)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	forced := conn.Permissions.CriticalOptions["force-command"]
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			return
		}
		go s.session(ch, chReqs, forced)
	}
}

// session answers the first exec request, then closes the channel.
func (s *SSH) session(ch ssh.Channel, reqs <-chan *ssh.Request, forced string) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		_ = req.Reply(true, nil)
		command := payload.Command
		if forced != "" {
			command = forced
		}
		s.mu.Lock()
		s.hits[command]++
		s.mu.Unlock()
		stdout, stderr, status := s.answer(command)
		_, _ = ch.Write(stdout)
		_, _ = ch.Stderr().Write(stderr)
		_, _ = ch.SendRequest("exit-status", false, binary.BigEndian.AppendUint32(nil, status))
		return
	}
}

// answer reads command's files from the first dir that has any.
func (s *SSH) answer(command string) (stdout, stderr []byte, status uint32) {
	name := commandFile(command)
	s.mu.Lock()
	dirs := s.dirs
	s.mu.Unlock()
	for _, dir := range dirs {
		base := filepath.Join(dir, name)
		out, outErr := os.ReadFile(base + ".stdout")
		errOut, errErr := os.ReadFile(base + ".stderr")
		exit, exitErr := os.ReadFile(base + ".exit")
		if outErr != nil && errErr != nil && exitErr != nil {
			continue
		}
		if exitErr == nil {
			n, err := strconv.ParseUint(strings.TrimSpace(string(exit)), 10, 32)
			if err != nil {
				return nil, []byte(fmt.Sprintf("fixture: %s.exit: %v\n", base, err)), 1
			}
			status = uint32(n)
		}
		return out, errOut, status
	}
	return nil, []byte(command + ": command not found\n"), 127
}

// scenarioDirs is where a scenario's files are looked for: in dir, then in
// its sibling baseline.
func scenarioDirs(dir string) []string {
	dirs := []string{dir}
	if baseline := filepath.Join(filepath.Dir(dir), "baseline"); filepath.Clean(dir) != baseline {
		dirs = append(dirs, baseline)
	}
	return dirs
}

// commandFile names command's files: "/usr/sbin/nginx -T" is "nginx_-T".
func commandFile(command string) string {
	args := strings.Fields(command)
	if len(args) == 0 {
		return "_"
	}
	args[0] = filepath.Base(args[0])
	return strings.Join(args, "_")
}
