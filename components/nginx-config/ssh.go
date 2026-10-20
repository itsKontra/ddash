package nginxconfig

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/crypto/ssh"
)

// errHostKey is a host offering another key than the pinned one.
type errHostKey struct{ got, want ssh.PublicKey }

func (e errHostKey) Error() string {
	return fmt.Sprintf("host key mismatch: the host offered %s %s, want %s %s",
		e.got.Type(), ssh.FingerprintSHA256(e.got), e.want.Type(), ssh.FingerprintSHA256(e.want))
}

// run runs `nginx -T` on nginx's host as the configured user. err is set
// only when it didn't run; a non-zero exit is exit with nginx's stderr.
func (c *component) run(ctx context.Context) (stdout, stderr string, exit int, err error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return "", "", 0, fmt.Errorf("%s unreachable: %w", c.host, err)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()

	pinned := c.hostKey
	sc, chans, reqs, err := ssh.NewClientConn(conn, c.addr, &ssh.ClientConfig{
		User: c.user,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(c.signer)},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if !bytes.Equal(key.Marshal(), pinned.Marshal()) {
				return errHostKey{got: key, want: pinned}
			}
			return nil
		},
		HostKeyAlgorithms: hostKeyAlgorithms(pinned),
	})
	if err != nil {
		return "", "", 0, sshError(ctx, c.host, err)
	}
	client := ssh.NewClient(sc, chans, reqs)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", "", 0, fmt.Errorf("ssh session: %w", err)
	}
	defer session.Close()
	var out, errOut bytes.Buffer
	session.Stdout, session.Stderr = &out, &errOut
	// The authorized_keys line forces the command; ask for it anyway, so
	// a key without command= runs the same.
	err = session.Run("nginx -T")
	var exitErr *ssh.ExitError
	switch {
	case errors.As(err, &exitErr):
		return out.String(), errOut.String(), exitErr.ExitStatus(), nil
	case err != nil:
		return "", "", 0, sshError(ctx, c.host, err)
	}
	return out.String(), errOut.String(), 0, nil
}

// hostKeyAlgorithms asks the host for the pinned key's kind only.
func hostKeyAlgorithms(key ssh.PublicKey) []string {
	if key.Type() == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{key.Type()}
}

// sshError says why SSH failed in the Operator's words.
func sshError(ctx context.Context, host string, err error) error {
	var hk errHostKey
	switch {
	case errors.As(err, &hk):
		return hk
	case strings.Contains(err.Error(), "unable to authenticate"):
		// OpenSSH's own words for a key not in authorized_keys.
		return errors.New("permission denied (publickey)")
	case ctx.Err() != nil:
		return fmt.Errorf("%s unreachable: %w", host, ctx.Err())
	}
	return fmt.Errorf("ssh %s: %w", host, err)
}

func newSeed() ([]byte, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return key.Seed(), nil
}

func signerFromSeed(seed []byte) (ssh.Signer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("ssh key: stored seed is %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	return ssh.NewSignerFromKey(ed25519.NewKeyFromSeed(seed))
}
