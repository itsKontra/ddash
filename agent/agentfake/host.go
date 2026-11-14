package agentfake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/hostagent"
)

// HostSeed is a fake host's starting state.
type HostSeed struct {
	// Hostname is what its Agent reports.
	Hostname string
	// OS is ubuntu, debian, alpine, fedora or arch.
	OS string
	// Pending are the package updates the next refresh finds.
	Pending []agent.Package
	// SSH: whether sshd is installed and running, and password login on.
	SSHInstalled, SSHRunning, PasswordAuth bool
	// Keys are authorized_keys lines by user (root, or "ops", uid 1000).
	Keys map[string][]string
	// RebootRequired is set from the start.
	RebootRequired bool
}

var osReleases = map[string]string{
	"ubuntu": "ID=ubuntu\nID_LIKE=debian\nVERSION_ID=\"24.04\"\nPRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\n",
	"debian": "ID=debian\nVERSION_ID=\"12\"\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n",
	"alpine": "ID=alpine\nVERSION_ID=3.20.3\nPRETTY_NAME=\"Alpine Linux v3.20\"\n",
	"fedora": "ID=fedora\nVERSION_ID=40\nPRETTY_NAME=\"Fedora Linux 40 (Server Edition)\"\n",
	"arch":   "ID=arch\nPRETTY_NAME=\"Arch Linux\"\n",
}

// Host is a fake Host Agent: the real operations over a fake root
// filesystem and fake package manager, sshd and service manager.
type Host struct {
	*server
	*fakeHost
	// Root is the fake host's filesystem.
	Root string
}

type fakeHost struct {
	root  string
	delay time.Duration
	os    string

	mu         sync.Mutex
	pending    []agent.Package
	sshRunning bool
	sshEnabled bool
	calls      []string
	fail       map[string]string
}

// NewHost starts a fake Host Agent over a fresh root (root: a temp dir
// when empty), unpaired, accepting any enrolment code.
func NewHost(root string, seed HostSeed, o Options) (*Host, error) {
	if root == "" {
		var err error
		if root, err = os.MkdirTemp("", "ddash-host-agent-"); err != nil {
			return nil, err
		}
	}
	osr, ok := osReleases[seed.OS]
	if !ok {
		return nil, fmt.Errorf("agentfake: no OS %q", seed.OS)
	}
	h := &fakeHost{root: root, delay: o.StepDelay, os: seed.OS, pending: seed.Pending, sshRunning: seed.SSHRunning, sshEnabled: seed.SSHRunning, fail: map[string]string{}}
	write := func(p, content string, mode os.FileMode) error {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		return os.WriteFile(full, []byte(content), mode)
	}
	files := map[string]string{
		"/etc/os-release":      osr,
		"/etc/passwd":          "root:x:0:0:root:/root:/bin/bash\nops:x:1000:1000:Operator:/home/ops:/bin/bash\nwww-data:x:33:33::/var/www:/usr/sbin/nologin\n",
		"/etc/ssh/sshd_config": "Include /etc/ssh/sshd_config.d/*.conf\nPermitRootLogin prohibit-password\nPasswordAuthentication " + yes(seed.PasswordAuth) + "\n",
	}
	for p, c := range files {
		if err := write(p, c, 0o644); err != nil {
			return nil, err
		}
	}
	if seed.SSHInstalled {
		if err := write("/usr/sbin/sshd", "", 0o755); err != nil {
			return nil, err
		}
	}
	if seed.RebootRequired {
		if err := write("/var/run/reboot-required", "", 0o644); err != nil {
			return nil, err
		}
	}
	for user, keys := range seed.Keys {
		home := "/home/" + user
		if user == "root" {
			home = "/root"
		}
		if err := write(home+"/.ssh/authorized_keys", strings.Join(keys, "\n")+"\n", 0o600); err != nil {
			return nil, err
		}
	}
	ops, err := hostagent.New(hostagent.Options{Root: root, Run: h.run, Binary: filepath.Join(root, "usr/local/bin/ddash-host-agent")})
	if err != nil {
		return nil, err
	}
	release := cmpOr(o.Release, agent.Release)
	hostname := cmpOr(seed.Hostname, seed.OS+"-fake")
	srv, err := newServer(agent.KindHost, o, func(s *agent.Server) {
		ops.Register(s, func(context.Context) agent.Info {
			return agent.Info{Kind: agent.KindHost, Release: release, Hostname: hostname, Arch: "amd64"}
		})
		// A first read, as the real Agent does at start.
		ops.RefreshNow()
	})
	if err != nil {
		return nil, err
	}
	return &Host{server: srv, fakeHost: h, Root: root}, nil
}

func yes(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// SetPending replaces the package updates the next refresh finds.
func (h *fakeHost) SetPending(p []agent.Package) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pending = p
}

// FailNext makes the next command named cmd ("apt-get", "sshd -t", …)
// fail with message.
func (h *fakeHost) FailNext(cmd, message string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fail[cmd] = message
}

// HostCalls returns every command run, in order.
func (h *fakeHost) HostCalls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.calls)
}

func (h *fakeHost) sleep(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(h.delay):
	}
}

func (h *fakeHost) path(p string) string { return filepath.Join(h.root, p) }

// run is the fake host's commands.
func (h *fakeHost) run(ctx context.Context, _ []string, name string, args []string, out func(string)) error {
	if out == nil {
		out = func(string) {}
	}
	call := strings.Join(append([]string{filepath.Base(name)}, args...), " ")
	h.mu.Lock()
	h.calls = append(h.calls, call)
	msg, failing := h.fail[filepath.Base(name)]
	if !failing {
		msg, failing = h.fail[call]
	}
	if failing {
		delete(h.fail, filepath.Base(name))
		delete(h.fail, call)
	}
	pending := slices.Clone(h.pending)
	h.mu.Unlock()
	if failing {
		h.sleep(ctx)
		out(msg)
		return errors.New("exit status 100")
	}
	switch filepath.Base(name) {
	case "uname":
		out("6.8.12-4-pve")
		return nil
	case "sshd":
		if slices.Equal(args, []string{"-T"}) {
			out("passwordauthentication " + h.effective("PasswordAuthentication", "yes"))
			out("permitrootlogin " + h.effective("PermitRootLogin", "prohibit-password"))
		}
		return nil
	case "systemctl", "rc-service", "rc-update":
		return h.service(args, out)
	case "apt", "apt-get", "dnf", "apk", "pacman", "checkupdates":
		return h.packages(ctx, filepath.Base(name), args, pending, out)
	}
	return nil
}

// effective is sshd's first value of key across the drop-in and config.
func (h *fakeHost) effective(key, def string) string {
	for _, f := range []string{"/etc/ssh/sshd_config.d/10-ddash.conf", "/etc/ssh/sshd_config"} {
		b, _ := os.ReadFile(h.path(f))
		for line := range strings.Lines(string(b)) {
			fields := strings.Fields(line)
			if len(fields) == 2 && strings.EqualFold(fields[0], key) {
				return fields[1]
			}
		}
	}
	return def
}

func (h *fakeHost) service(args []string, _ func(string)) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	has := func(s string) bool { return slices.Contains(args, s) }
	switch {
	case has("is-active") || (has("status") && !has("show")):
		if h.sshRunning {
			return nil
		}
		return errors.New("inactive")
	case has("is-enabled"):
		if h.sshEnabled {
			return nil
		}
		return errors.New("disabled")
	case has("show"):
		return nil
	case has("enable") || has("add"):
		h.sshEnabled = true
		if has("--now") {
			h.sshRunning = true
		}
	case has("start"):
		h.sshRunning = true
	}
	return nil
}

func (h *fakeHost) packages(ctx context.Context, name string, args []string, pending []agent.Package, out func(string)) error {
	has := func(s string) bool { return slices.Contains(args, s) }
	switch {
	case has("update") || has("makecache"):
		h.sleep(ctx)
		out("Reading package lists... Done")
	case name == "apt" && has("--upgradable"):
		for _, p := range pending {
			out(fmt.Sprintf("%s/stable %s amd64 [upgradable from: %s]", p.Name, p.New, p.Current))
		}
	case name == "dnf" && has("--upgrades"):
		for _, p := range pending {
			out(fmt.Sprintf("%s.x86_64 %s updates", p.Name, p.New))
		}
	case name == "dnf" && has("needs-restarting"):
		if _, err := os.Stat(h.path("/var/run/reboot-required")); err == nil {
			return errors.New("exit status 1")
		}
	case name == "apk" && has("version"):
		for _, p := range pending {
			out(fmt.Sprintf("%s-%s < %s", p.Name, p.Current, p.New))
		}
	case name == "checkupdates":
		for _, p := range pending {
			out(fmt.Sprintf("%s %s -> %s", p.Name, p.Current, p.New))
		}
	case has("dist-upgrade") || has("upgrade") || has("-Syu"):
		reboot := false
		for _, p := range pending {
			h.sleep(ctx)
			out(fmt.Sprintf("Unpacking %s (%s) over (%s) ...", p.Name, p.New, p.Current))
			out(fmt.Sprintf("Setting up %s (%s) ...", p.Name, p.New))
			if strings.HasPrefix(p.Name, "linux-") || p.Name == "linux" || strings.HasPrefix(p.Name, "kernel") || p.Name == "libc6" || p.Name == "systemd" {
				reboot = true
			}
		}
		h.mu.Lock()
		h.pending = nil
		h.mu.Unlock()
		if reboot {
			_ = os.MkdirAll(h.path("/var/run"), 0o755)
			_ = os.WriteFile(h.path("/var/run/reboot-required"), nil, 0o644)
		}
	case has("install") || has("add") || has("-S"):
		h.sleep(ctx)
		out("Setting up openssh-server ...")
		_ = os.MkdirAll(h.path("/usr/sbin"), 0o755)
		_ = os.WriteFile(h.path("/usr/sbin/sshd"), nil, 0o755)
	}
	return nil
}
