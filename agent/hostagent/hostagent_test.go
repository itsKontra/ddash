package hostagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/agent"
)

func TestParsers(t *testing.T) {
	apt := parseApt([]string{
		"Listing...",
		"openssl/stable-security 3.0.15-1~deb12u1 amd64 [upgradable from: 3.0.14-1~deb12u2]",
		"tzdata/stable-updates 2024b-0+deb12u1 all [upgradable from: 2024a-0+deb12u1]",
	})
	if len(apt) != 2 || apt[0] != (agent.Package{Name: "openssl", Current: "3.0.14-1~deb12u2", New: "3.0.15-1~deb12u1"}) {
		t.Errorf("apt = %+v", apt)
	}
	dnf := parseDnf([]string{"Available Upgrades", "curl.x86_64   8.6.0-10.fc40   updates", "glibc.x86_64 2.39-22.fc40 updates"})
	if len(dnf) != 2 || dnf[0] != (agent.Package{Name: "curl", New: "8.6.0-10.fc40"}) {
		t.Errorf("dnf = %+v", dnf)
	}
	apk := parseApk([]string{"Installed:                                Available:", "busybox-1.36.1-r28 < 1.36.1-r29", "libcrypto3-3.3.2-r0 < 3.3.2-r1"})
	if len(apk) != 2 || apk[1] != (agent.Package{Name: "libcrypto3", Current: "3.3.2-r0", New: "3.3.2-r1"}) {
		t.Errorf("apk = %+v", apk)
	}
	pac := parsePacman([]string{"linux 6.10.9.arch1-1 -> 6.10.10.arch1-1"})
	if len(pac) != 1 || pac[0] != (agent.Package{Name: "linux", Current: "6.10.9.arch1-1", New: "6.10.10.arch1-1"}) {
		t.Errorf("pacman = %+v", pac)
	}
}

// host is a fake root filesystem with an sshd whose -t passes unless
// failTest, recording every command.
type host struct {
	root     string
	ran      []string
	failTest bool
}

func newHost(t *testing.T, osRelease string) *host {
	h := &host{root: t.TempDir()}
	write := func(p, s string, mode os.FileMode) {
		full := filepath.Join(h.root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("/etc/os-release", osRelease, 0o644)
	write("/etc/passwd", "root:x:0:0:root:/root:/bin/bash\nmatt:x:1000:1000::"+"/home/matt:/bin/bash\nnobody:x:65534:65534::/:/usr/sbin/nologin\n", 0o644)
	write("/etc/ssh/sshd_config", "PermitRootLogin yes\n", 0o644)
	write("/usr/sbin/sshd", "", 0o755)
	return h
}

func (h *host) run(_ context.Context, _ []string, name string, args []string, out func(string)) error {
	h.ran = append(h.ran, strings.Join(append([]string{name}, args...), " "))
	if strings.HasSuffix(name, "sshd") && slices.Equal(args, []string{"-t"}) && h.failTest {
		if out != nil {
			out("Bad configuration option")
		}
		return errors.New("exit status 255")
	}
	if strings.HasSuffix(name, "sshd") && slices.Equal(args, []string{"-T"}) {
		out("passwordauthentication yes")
		out("permitrootlogin without-password")
	}
	return nil
}

const debian = "ID=debian\nVERSION_ID=\"12\"\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n"

func TestDetect(t *testing.T) {
	for osr, want := range map[string]string{
		debian:                           "apt",
		"ID=ubuntu\nID_LIKE=debian\n":    "apt",
		"ID=fedora\nVERSION_ID=40\n":     "dnf",
		"ID=alpine\n":                    "apk",
		"ID=arch\n":                      "pacman",
		"ID=endeavouros\nID_LIKE=arch\n": "pacman",
	} {
		h := newHost(t, osr)
		a, err := New(Options{Root: h.root, Run: h.run})
		if err != nil || a.os.PackageManager != want {
			t.Errorf("%q: %v %v", osr, a, err)
		}
	}
	h := newHost(t, "ID=gentoo\n")
	if _, err := New(Options{Root: h.root, Run: h.run}); err == nil {
		t.Error("gentoo was accepted")
	}
}

const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl laptop"

func TestPasswordLoginNeedsAKey(t *testing.T) {
	ctx := context.Background()
	h := newHost(t, debian)
	a, err := New(Options{Root: h.root, Run: h.run})
	if err != nil {
		t.Fatal(err)
	}
	var refused refusal
	if err := a.ApplySSH(ctx, agent.SSHSettings{PasswordAuthentication: false, PermitRootLogin: "no"}); !errors.As(err, &refused) {
		t.Fatalf("no key: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.root, dropIn)); err == nil {
		t.Fatal("a refused change wrote the drop-in")
	}
	// A key for root only doesn't help once root may not log in.
	keys := filepath.Join(h.root, "/root/.ssh/authorized_keys")
	os.MkdirAll(filepath.Dir(keys), 0o700)
	os.WriteFile(keys, []byte(key+"\n"), 0o600)
	if err := a.ApplySSH(ctx, agent.SSHSettings{PasswordAuthentication: false, PermitRootLogin: "no"}); !errors.As(err, &refused) {
		t.Fatalf("root's key with root login off: %v", err)
	}
	if err := a.ApplySSH(ctx, agent.SSHSettings{PasswordAuthentication: false, PermitRootLogin: "prohibit-password"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(h.root, dropIn))
	if !strings.Contains(string(b), "PasswordAuthentication no") || !strings.Contains(string(b), "PermitRootLogin prohibit-password") {
		t.Fatalf("drop-in = %s", b)
	}
	cfg, _ := os.ReadFile(filepath.Join(h.root, "/etc/ssh/sshd_config"))
	if !strings.HasPrefix(string(cfg), "Include /etc/ssh/sshd_config.d/*.conf\n") {
		t.Fatalf("sshd_config = %s", cfg)
	}
	if !slices.Contains(h.ran, "systemctl reload-or-restart ssh") {
		t.Fatalf("not reloaded: %v", h.ran)
	}
}

func TestSettingsSshdRefuses(t *testing.T) {
	ctx := context.Background()
	h := newHost(t, debian)
	a, _ := New(Options{Root: h.root, Run: h.run})
	os.MkdirAll(filepath.Join(h.root, "/etc/ssh/sshd_config.d"), 0o755)
	os.WriteFile(filepath.Join(h.root, dropIn), []byte("old\n"), 0o644)
	h.failTest = true
	if err := a.ApplySSH(ctx, agent.SSHSettings{PasswordAuthentication: true, PermitRootLogin: "yes"}); err == nil {
		t.Fatal("applied what sshd -t refused")
	}
	if b, _ := os.ReadFile(filepath.Join(h.root, dropIn)); string(b) != "old\n" {
		t.Fatalf("drop-in not restored: %q", b)
	}
	if slices.ContainsFunc(h.ran, func(c string) bool { return strings.Contains(c, "reload") }) {
		t.Fatal("reloaded after a refusal")
	}
}

func TestReadKeys(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "authorized_keys")
	os.WriteFile(p, []byte(key+"\n"+strings.Replace(key, "laptop", "ddash:phone", 1)+"\n"), 0o600)
	keys, err := readKeys(p, "matt")
	if err != nil || len(keys) != 2 {
		t.Fatal(keys, err)
	}
	if keys[0].Name != "" || keys[1].Name != "phone" || !strings.HasPrefix(keys[1].Fingerprint, "SHA256:") || keys[1].Type != "ssh-ed25519" {
		t.Fatalf("keys = %+v", keys)
	}
}
