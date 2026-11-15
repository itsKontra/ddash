// Package hostagent is the Host Agent's operations on its host (Ubuntu,
// Debian, Alpine, Fedora or Arch Linux, as root): pending package updates
// and running them, "reboot required", and SSH: sshd's state and
// settings, enabling it, and the SSH Keys in users' authorized_keys.
//
// Every operation is a fixed, named one; none runs a command it was sent.
package hostagent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/itsKontra/ddash/agent"
)

// Runner runs a program, passing each output line to out (may be nil).
type Runner func(ctx context.Context, env []string, name string, args []string, out func(string)) error

// Exec is the real Runner.
func Exec(ctx context.Context, env []string, name string, args []string, out func(string)) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	return agent.RunLines(cmd, out)
}

// Options configure the Host Agent's operations.
type Options struct {
	// Root is where the host's filesystem is: "/" for real, a temp dir in
	// tests.
	Root string
	Run  Runner
	// Binary is the Agent's own executable, replaced by Update Agent.
	Binary string
	// Exit ends the Agent after an update, so its service manager starts
	// the new binary.
	Exit func()
	// HTTP downloads the new binary.
	HTTP *http.Client
}

// Agent is the Host Agent's operations.
type Agent struct {
	o    Options
	os   agent.OS
	jobs *agent.Jobs

	mu       sync.Mutex
	packages agent.PackagesResponse
	sshMu    sync.Mutex
}

// New detects the distribution from /etc/os-release.
func New(o Options) (*Agent, error) {
	if o.Root == "" {
		o.Root = "/"
	}
	if o.Run == nil {
		o.Run = Exec
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	a := &Agent{o: o}
	osr, err := a.detect()
	if err != nil {
		return nil, err
	}
	a.os = osr
	return a, nil
}

func (a *Agent) path(p string) string { return filepath.Join(a.o.Root, p) }

// detect reads /etc/os-release and picks the package manager and init.
func (a *Agent) detect() (agent.OS, error) {
	b, err := os.ReadFile(a.path("/etc/os-release"))
	if err != nil {
		return agent.OS{}, fmt.Errorf("can't tell the distribution: %w", err)
	}
	kv := map[string]string{}
	for line := range strings.Lines(string(b)) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			kv[k] = strings.Trim(v, `"'`)
		}
	}
	o := agent.OS{ID: kv["ID"], Version: kv["VERSION_ID"], Pretty: kv["PRETTY_NAME"]}
	like := append([]string{o.ID}, strings.Fields(kv["ID_LIKE"])...)
	switch {
	case slices.Contains(like, "debian") || slices.Contains(like, "ubuntu"):
		o.PackageManager = "apt"
	case slices.Contains(like, "fedora") || slices.Contains(like, "rhel"):
		o.PackageManager = "dnf"
	case slices.Contains(like, "alpine"):
		o.PackageManager = "apk"
	case slices.Contains(like, "arch"):
		o.PackageManager = "pacman"
	default:
		return o, fmt.Errorf("%s isn't a distribution the Host Agent supports (Ubuntu, Debian, Alpine, Fedora, Arch Linux)", o.Pretty)
	}
	o.Init = "systemd"
	if o.PackageManager == "apk" {
		o.Init = "openrc"
	}
	return o, nil
}

// OS is the host as detected, with "reboot required" read now.
func (a *Agent) OS(ctx context.Context) agent.OS {
	o := a.os
	o.RebootRequired = a.rebootRequired(ctx)
	return o
}

// Register adds the Host Agent's operations to s.
func (a *Agent) Register(s *agent.Server, info func(context.Context) agent.Info) {
	a.jobs = s.Jobs()
	s.Handle("GET "+agent.PathInfo, func(w http.ResponseWriter, r *http.Request) {
		i := info(r.Context())
		o := a.OS(r.Context())
		i.OS = &o
		agent.WriteJSON(w, http.StatusOK, i)
	})
	s.Handle("GET "+agent.PathPackages, func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		p := a.packages
		a.mu.Unlock()
		p.RebootRequired = a.rebootRequired(r.Context())
		if p.Packages == nil {
			p.Packages = []agent.Package{}
		}
		agent.WriteJSON(w, http.StatusOK, p)
	})
	s.Handle("POST "+agent.PathPackagesRefresh, a.startJob("packages.refresh", "Read pending package updates", []string{"Refresh the package index"}, a.refreshJob))
	s.Handle("POST "+agent.PathUpgrade, a.startJob("packages.upgrade", "Update packages", []string{"Refresh the package index", "Install updates", "Check whether a reboot is required"}, a.upgradeJob))
	s.Handle("GET "+agent.PathSSH, func(w http.ResponseWriter, r *http.Request) {
		st, err := a.SSH(r.Context())
		if err != nil {
			agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
			return
		}
		agent.WriteJSON(w, http.StatusOK, st)
	})
	s.Handle("POST "+agent.PathSSHEnable, a.startJob("ssh.enable", "Enable sshd", []string{"Install OpenSSH", "Enable and start sshd"}, a.enableSSHJob))
	s.Handle("POST "+agent.PathSSHKeys, a.addKey)
	s.Handle("DELETE "+agent.PathSSHKeys, a.removeKey)
	s.Handle("POST "+agent.PathSSHSettings, a.settings)
	s.Handle("POST "+agent.PathUpdate, a.update)
}

// startJob answers a call that starts a job with no request body.
func (a *Agent) startJob(op, title string, steps []string, run agent.Run) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		job, err := a.jobs.Start(op, title, "host", steps, run)
		if err != nil {
			agent.WriteError(w, http.StatusConflict, agent.CodeBusy, err.Error())
			return
		}
		agent.WriteJSON(w, http.StatusAccepted, agent.JobRef{Job: job})
	}
}

// RefreshInterval is how often the Agent reads pending updates itself.
const RefreshInterval = 6 * time.Hour

// Refresh reads pending updates now and then every RefreshInterval until
// ctx ends, without a job (and skipping a round while one runs).
func (a *Agent) Refresh(ctx context.Context) {
	for {
		a.RefreshNow()
		select {
		case <-ctx.Done():
			return
		case <-time.After(RefreshInterval):
		}
	}
}

// RefreshNow starts reading pending updates, unless a job runs.
func (a *Agent) RefreshNow() {
	_, _ = a.jobs.Start("packages.refresh", "Read pending package updates", "host", []string{"Refresh the package index"}, a.refreshJob)
}

var nonInteractive = []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a", "LC_ALL=C"}

// refreshIndex refreshes the package index (pacman: checkupdates syncs a
// temporary database, so the real one never gets a partial upgrade).
func (a *Agent) refreshIndex(ctx context.Context, p *agent.Progress, i int) error {
	log := func(l string) { p.Log(i, l) }
	switch a.os.PackageManager {
	case "apt":
		return a.o.Run(ctx, nonInteractive, "apt-get", []string{"update", "-q"}, log)
	case "dnf":
		return a.o.Run(ctx, nonInteractive, "dnf", []string{"-q", "makecache", "--refresh"}, log)
	case "apk":
		return a.o.Run(ctx, nonInteractive, "apk", []string{"update", "-q"}, log)
	}
	return nil
}

// Pending lists the pending updates from the refreshed index.
func (a *Agent) Pending(ctx context.Context) ([]agent.Package, error) {
	var lines []string
	collect := func(l string) { lines = append(lines, l) }
	var err error
	switch a.os.PackageManager {
	case "apt":
		err = a.o.Run(ctx, nonInteractive, "apt", []string{"list", "--upgradable", "-qq"}, collect)
		return parseApt(lines), err
	case "dnf":
		err = a.o.Run(ctx, nonInteractive, "dnf", []string{"-q", "list", "--upgrades"}, collect)
		return parseDnf(lines), err
	case "apk":
		err = a.o.Run(ctx, nonInteractive, "apk", []string{"version", "-l", "<"}, collect)
		return parseApk(lines), err
	case "pacman":
		err = a.o.Run(ctx, nonInteractive, "checkupdates", nil, collect)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 2 { // checkupdates: none
			return nil, nil
		}
		return parsePacman(lines), err
	}
	return nil, nil
}

// parseApt reads `apt list --upgradable`: "name/suite new arch [upgradable from: old]".
func parseApt(lines []string) []agent.Package {
	var out []agent.Package
	for _, l := range lines {
		name, rest, ok := strings.Cut(l, "/")
		if !ok || strings.HasPrefix(l, "Listing") || strings.HasPrefix(l, "WARNING") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 2 {
			continue
		}
		p := agent.Package{Name: name, New: f[1]}
		if _, from, ok := strings.Cut(l, "upgradable from: "); ok {
			p.Current = strings.TrimSuffix(from, "]")
		}
		out = append(out, p)
	}
	return out
}

// parseDnf reads `dnf list --upgrades`: "name.arch new repo".
func parseDnf(lines []string) []agent.Package {
	var out []agent.Package
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) != 3 || strings.HasSuffix(f[0], ":") || f[0] == "Available" {
			continue
		}
		name := f[0]
		if i := strings.LastIndex(name, "."); i > 0 {
			name = name[:i]
		}
		out = append(out, agent.Package{Name: name, New: f[1]})
	}
	return out
}

// parseApk reads `apk version -l '<'`: "name-old < new".
func parseApk(lines []string) []agent.Package {
	var out []agent.Package
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) != 3 || f[1] != "<" {
			continue
		}
		// name-1.2.3-r0: the release is the last part, the version the one before.
		parts := strings.Split(f[0], "-")
		n := len(parts)
		if n < 3 || !strings.HasPrefix(parts[n-1], "r") {
			continue
		}
		name, ver := strings.Join(parts[:n-2], "-"), parts[n-2]+"-"+parts[n-1]
		out = append(out, agent.Package{Name: name, Current: ver, New: f[2]})
	}
	return out
}

// parsePacman reads checkupdates: "name old -> new".
func parsePacman(lines []string) []agent.Package {
	var out []agent.Package
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) == 4 && f[2] == "->" {
			out = append(out, agent.Package{Name: f[0], Current: f[1], New: f[3]})
		}
	}
	return out
}

func (a *Agent) refreshJob(ctx context.Context, p *agent.Progress) (map[string]string, error) {
	p.Begin(0)
	err := a.refreshIndex(ctx, p, 0)
	var pkgs []agent.Package
	if err == nil {
		pkgs, err = a.Pending(ctx)
	}
	a.mu.Lock()
	if err != nil {
		a.packages.Error = err.Error()
	} else {
		a.packages = agent.PackagesResponse{Packages: pkgs, Read: time.Now().UTC()}
	}
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	p.Done(0, fmt.Sprintf("%d pending", len(pkgs)))
	return map[string]string{"pending": strconv.Itoa(len(pkgs))}, nil
}

func (a *Agent) upgradeJob(ctx context.Context, p *agent.Progress) (map[string]string, error) {
	p.Begin(0)
	if err := a.refreshIndex(ctx, p, 0); err != nil {
		return nil, err
	}
	before, _ := a.Pending(ctx)
	p.Done(0, fmt.Sprintf("%d pending", len(before)))
	p.Begin(1)
	log := func(l string) { p.Log(1, l) }
	var err error
	switch a.os.PackageManager {
	case "apt":
		err = a.o.Run(ctx, nonInteractive, "apt-get", []string{"-y", "-q", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "dist-upgrade"}, log)
	case "dnf":
		err = a.o.Run(ctx, nonInteractive, "dnf", []string{"-y", "upgrade"}, log)
	case "apk":
		err = a.o.Run(ctx, nonInteractive, "apk", []string{"upgrade", "--no-interactive"}, log)
	case "pacman":
		err = a.o.Run(ctx, nonInteractive, "pacman", []string{"-Syu", "--noconfirm"}, log)
	}
	if err != nil {
		return nil, err
	}
	p.Done(1, fmt.Sprintf("%d updated", len(before)))
	a.mu.Lock()
	a.packages = agent.PackagesResponse{Packages: []agent.Package{}, Read: time.Now().UTC()}
	a.mu.Unlock()
	p.Begin(2)
	reboot := a.rebootRequired(ctx)
	detail := "no reboot required"
	if reboot {
		detail = "reboot required"
	}
	p.Done(2, detail)
	return map[string]string{"updated": strconv.Itoa(len(before)), "reboot_required": strconv.FormatBool(reboot)}, nil
}

// rebootRequired: Debian and Ubuntu's flag file, dnf's needs-restarting,
// or a running kernel whose modules are gone (Arch, Alpine on a VM).
func (a *Agent) rebootRequired(ctx context.Context) bool {
	if _, err := os.Stat(a.path("/var/run/reboot-required")); err == nil {
		return true
	}
	if a.os.PackageManager == "dnf" {
		err := a.o.Run(ctx, nil, "dnf", []string{"-q", "needs-restarting", "-r"}, nil)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return true
		}
	}
	var release []string
	if a.o.Run(ctx, nil, "uname", []string{"-r"}, func(l string) { release = append(release, l) }) == nil && len(release) > 0 {
		mods := a.path("/lib/modules")
		if entries, err := os.ReadDir(mods); err == nil && len(entries) > 0 {
			if _, err := os.Stat(filepath.Join(mods, strings.TrimSpace(release[0]))); errors.Is(err, os.ErrNotExist) {
				return true
			}
		}
	}
	return false
}

// update downloads the new binary, checks its SHA-256, replaces the
// Agent's own and exits, so the service manager starts it.
func (a *Agent) update(w http.ResponseWriter, r *http.Request) {
	var req agent.UpdateRequest
	if !agent.ReadJSON(w, r, &req) {
		return
	}
	if req.URL == "" || len(req.SHA256) != 64 {
		agent.WriteError(w, http.StatusBadRequest, agent.CodeInvalid, "url and sha256 are required")
		return
	}
	job, err := a.jobs.Start("agent.update", "Update the Host Agent to "+req.Release, "agent", []string{"Download the new Host Agent", "Check its checksum", "Restart the Host Agent"}, func(ctx context.Context, p *agent.Progress) (map[string]string, error) {
		p.Begin(0)
		hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
		if err != nil {
			return nil, err
		}
		res, err := a.o.HTTP.Do(hreq)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("ddash answered %s", res.Status)
		}
		tmp := a.o.Binary + ".new"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		if _, err := io.Copy(io.MultiWriter(f, h), res.Body); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
		p.Done(0, "")
		p.Begin(1)
		if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(req.SHA256) {
			os.Remove(tmp)
			return nil, fmt.Errorf("the download's SHA-256 is %s, not %s", got, req.SHA256)
		}
		p.Done(1, "")
		if err := os.Rename(tmp, a.o.Binary); err != nil {
			return nil, err
		}
		p.Done(2, "restarting")
		if a.o.Exit != nil {
			go func() { time.Sleep(time.Second); a.o.Exit() }()
		}
		return nil, nil
	})
	if err != nil {
		agent.WriteError(w, http.StatusConflict, agent.CodeBusy, err.Error())
		return
	}
	agent.WriteJSON(w, http.StatusAccepted, agent.JobRef{Job: job})
}

// --- SSH ---

// dropIn is where the Agent's sshd settings live: early in the include
// order, as sshd keeps the first value it reads.
const dropIn = "/etc/ssh/sshd_config.d/10-ddash.conf"

// sshUnit is the service's name: Debian and Ubuntu call it ssh.
func (a *Agent) sshUnit() string {
	if a.os.PackageManager == "apt" {
		return "ssh"
	}
	return "sshd"
}

func (a *Agent) sshdBinary() string {
	for _, p := range []string{"/usr/sbin/sshd", "/usr/bin/sshd", "/sbin/sshd"} {
		if _, err := os.Stat(a.path(p)); err == nil {
			return p
		}
	}
	return ""
}

// SSH reads sshd's state, its effective settings and the keys.
func (a *Agent) SSH(ctx context.Context) (agent.SSHState, error) {
	st := agent.SSHState{Keys: []agent.AuthorizedKey{}}
	bin := a.sshdBinary()
	st.Installed = bin != ""
	if st.Installed {
		switch a.os.Init {
		case "systemd":
			st.Running = a.o.Run(ctx, nil, "systemctl", []string{"is-active", "--quiet", a.sshUnit()}, nil) == nil ||
				a.o.Run(ctx, nil, "systemctl", []string{"is-active", "--quiet", "ssh.socket"}, nil) == nil
			st.Enabled = a.o.Run(ctx, nil, "systemctl", []string{"is-enabled", "--quiet", a.sshUnit()}, nil) == nil ||
				a.o.Run(ctx, nil, "systemctl", []string{"is-enabled", "--quiet", "ssh.socket"}, nil) == nil
		case "openrc":
			st.Running = a.o.Run(ctx, nil, "rc-service", []string{"sshd", "status"}, nil) == nil
			var lines []string
			_ = a.o.Run(ctx, nil, "rc-update", []string{"show", "default"}, func(l string) { lines = append(lines, l) })
			st.Enabled = slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "sshd ") })
		}
		eff, err := a.effective(ctx, bin)
		if err == nil {
			st.PasswordAuthentication = eff["passwordauthentication"] == "yes"
			st.PermitRootLogin = eff["permitrootlogin"]
			if st.PermitRootLogin == "without-password" {
				st.PermitRootLogin = "prohibit-password"
			}
		}
	}
	users, err := a.users()
	if err != nil {
		return st, err
	}
	for _, u := range users {
		st.Users = append(st.Users, u.name)
		keys, _ := readKeys(a.path(u.authorizedKeys()), u.name)
		st.Keys = append(st.Keys, keys...)
	}
	return st, nil
}

// effective is `sshd -T`'s settings.
func (a *Agent) effective(ctx context.Context, bin string) (map[string]string, error) {
	out := map[string]string{}
	err := a.o.Run(ctx, nil, bin, []string{"-T"}, func(l string) {
		if k, v, ok := strings.Cut(l, " "); ok {
			out[strings.ToLower(k)] = strings.TrimSpace(v)
		}
	})
	return out, err
}

type user struct {
	name, home string
	uid, gid   int
}

func (u user) authorizedKeys() string { return filepath.Join(u.home, ".ssh", "authorized_keys") }

// keysFile is authorized_keys inside a user's home.
const keysFile = ".ssh/authorized_keys"

// home opens u's home as a root for file access. The Agent runs as root
// and the home belongs to u, so everything it reads, writes or chowns there
// goes through this: a symlink that leads out of the home is an error,
// never followed. A home that doesn't exist yet is created, as before.
func (a *Agent) home(u user) (*os.Root, error) {
	if err := os.MkdirAll(a.path(u.home), 0o700); err != nil {
		return nil, err
	}
	return os.OpenRoot(a.path(u.home))
}

// writeKeys replaces u's authorized_keys in home with content, atomically,
// creating ~/.ssh, and leaves both owned by u.
func writeKeys(home *os.Root, u user, content []byte) error {
	if err := home.MkdirAll(".ssh", 0o700); err != nil {
		return err
	}
	tmp := ".ssh/.authorized_keys." + rand.Text()
	f, err := home.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = home.Remove(tmp) }() // gone already once renamed
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := home.Rename(tmp, keysFile); err != nil {
		return err
	}
	_ = home.Lchown(".ssh", u.uid, u.gid)
	_ = home.Lchown(keysFile, u.uid, u.gid)
	return nil
}

// users are root and every user with a login shell and uid ≥ 1000.
func (a *Agent) users() ([]user, error) {
	b, err := os.ReadFile(a.path("/etc/passwd"))
	if err != nil {
		return nil, err
	}
	var out []user
	for line := range strings.Lines(string(b)) {
		f := strings.Split(strings.TrimSpace(line), ":")
		if len(f) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(f[2])
		gid, _ := strconv.Atoi(f[3])
		shell := f[6]
		login := !strings.HasSuffix(shell, "nologin") && !strings.HasSuffix(shell, "false") && shell != ""
		if uid == 0 || (uid >= 1000 && uid < 60000 && login) {
			out = append(out, user{name: f[0], home: f[5], uid: uid, gid: gid})
		}
	}
	return out, nil
}

func (a *Agent) user(name string) (user, error) {
	users, err := a.users()
	if err != nil {
		return user{}, err
	}
	for _, u := range users {
		if u.name == name {
			return u, nil
		}
	}
	return user{}, fmt.Errorf("no user %q who can log in", name)
}

// readKeys reads an authorized_keys file's keys.
func readKeys(path, userName string) ([]agent.AuthorizedKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseKeys(b, userName), nil
}

// parseKeys reads the keys of an authorized_keys file's content.
func parseKeys(b []byte, userName string) []agent.AuthorizedKey {
	var out []agent.AuthorizedKey
	rest := b
	for len(rest) > 0 {
		pub, comment, _, next, err := ssh.ParseAuthorizedKey(rest)
		if err != nil {
			break
		}
		rest = next
		k := agent.AuthorizedKey{User: userName, Type: pub.Type(), Fingerprint: ssh.FingerprintSHA256(pub), Comment: comment}
		if name, ok := strings.CutPrefix(comment, "ddash:"); ok {
			k.Name = name
		}
		out = append(out, k)
	}
	return out
}

func (a *Agent) addKey(w http.ResponseWriter, r *http.Request) {
	var req agent.KeyRequest
	if !agent.ReadJSON(w, r, &req) {
		return
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
	if err != nil || req.Name == "" || strings.ContainsAny(req.Name, " \n\t") {
		agent.WriteError(w, http.StatusBadRequest, agent.CodeInvalid, "a key needs a name without spaces and an authorized_keys line")
		return
	}
	a.sshMu.Lock()
	defer a.sshMu.Unlock()
	u, err := a.user(req.User)
	if err != nil {
		agent.WriteError(w, http.StatusNotFound, agent.CodeNotFound, err.Error())
		return
	}
	home, err := a.home(u)
	if err != nil {
		agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
		return
	}
	defer home.Close()
	old, err := home.ReadFile(keysFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
		return
	}
	for _, k := range parseKeys(old, u.name) {
		if k.Name == req.Name || k.Fingerprint == ssh.FingerprintSHA256(pub) {
			agent.WriteError(w, http.StatusConflict, agent.CodeConflict, fmt.Sprintf("%s already has this key", u.name))
			return
		}
	}
	if len(old) > 0 && old[len(old)-1] != '\n' {
		old = append(old, '\n')
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))) + " ddash:" + req.Name + "\n"
	if err := writeKeys(home, u, append(old, line...)); err != nil {
		agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Agent) removeKey(w http.ResponseWriter, r *http.Request) {
	var req agent.KeyRequest
	if !agent.ReadJSON(w, r, &req) {
		return
	}
	a.sshMu.Lock()
	defer a.sshMu.Unlock()
	u, err := a.user(req.User)
	if err != nil {
		agent.WriteError(w, http.StatusNotFound, agent.CodeNotFound, err.Error())
		return
	}
	st, err := a.SSH(r.Context())
	if err != nil {
		agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
		return
	}
	if !st.PasswordAuthentication && len(st.Keys) <= 1 {
		agent.WriteError(w, http.StatusConflict, agent.CodeConflict, "this is the last key and password login is off: removing it would lock everyone out")
		return
	}
	home, err := a.home(u)
	if err != nil {
		agent.WriteError(w, http.StatusNotFound, agent.CodeNotFound, err.Error())
		return
	}
	defer home.Close()
	b, err := home.ReadFile(keysFile)
	if err != nil {
		agent.WriteError(w, http.StatusNotFound, agent.CodeNotFound, err.Error())
		return
	}
	var kept []string
	found := false
	for line := range strings.Lines(string(b)) {
		f := strings.Fields(line)
		if len(f) >= 3 && f[len(f)-1] == "ddash:"+req.Name {
			found = true
			continue
		}
		kept = append(kept, strings.TrimRight(line, "\n"))
	}
	if !found {
		agent.WriteError(w, http.StatusNotFound, agent.CodeNotFound, fmt.Sprintf("%s has no SSH Key %s", u.name, req.Name))
		return
	}
	content := strings.Join(kept, "\n")
	if content != "" {
		content += "\n"
	}
	if err := writeKeys(home, u, []byte(content)); err != nil {
		agent.WriteError(w, http.StatusInternalServerError, agent.CodeFailed, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Agent) enableSSHJob(ctx context.Context, p *agent.Progress) (map[string]string, error) {
	p.Begin(0)
	log := func(i int) func(string) { return func(l string) { p.Log(i, l) } }
	if a.sshdBinary() != "" {
		p.Skip(0, "already installed")
	} else {
		var err error
		switch a.os.PackageManager {
		case "apt":
			err = a.o.Run(ctx, nonInteractive, "apt-get", []string{"install", "-y", "-q", "openssh-server"}, log(0))
		case "dnf":
			err = a.o.Run(ctx, nonInteractive, "dnf", []string{"install", "-y", "openssh-server"}, log(0))
		case "apk":
			err = a.o.Run(ctx, nonInteractive, "apk", []string{"add", "--no-interactive", "openssh"}, log(0))
		case "pacman":
			err = a.o.Run(ctx, nonInteractive, "pacman", []string{"-S", "--noconfirm", "--needed", "openssh"}, log(0))
		}
		if err != nil {
			return nil, err
		}
		p.Done(0, "")
	}
	p.Begin(1)
	var err error
	switch a.os.Init {
	case "systemd":
		err = a.o.Run(ctx, nil, "systemctl", []string{"enable", "--now", a.sshUnit()}, log(1))
	case "openrc":
		if err = a.o.Run(ctx, nil, "rc-update", []string{"add", "sshd", "default"}, log(1)); err == nil {
			err = a.o.Run(ctx, nil, "rc-service", []string{"sshd", "start"}, log(1))
		}
	}
	if err != nil {
		return nil, err
	}
	p.Done(1, "")
	return nil, nil
}

func (a *Agent) settings(w http.ResponseWriter, r *http.Request) {
	var req agent.SSHSettings
	if !agent.ReadJSON(w, r, &req) {
		return
	}
	if !slices.Contains([]string{"yes", "prohibit-password", "no"}, req.PermitRootLogin) {
		agent.WriteError(w, http.StatusBadRequest, agent.CodeInvalid, `permit_root_login is "yes", "prohibit-password" or "no"`)
		return
	}
	a.sshMu.Lock()
	defer a.sshMu.Unlock()
	if err := a.ApplySSH(r.Context(), req); err != nil {
		code := agent.CodeFailed
		var refused refusal
		if errors.As(err, &refused) {
			code = agent.CodeConflict
		}
		agent.WriteError(w, http.StatusConflict, code, err.Error())
		return
	}
	st, _ := a.SSH(r.Context())
	agent.WriteJSON(w, http.StatusOK, st)
}

// refusal is a settings change the Agent won't make.
type refusal string

func (r refusal) Error() string { return string(r) }

// ApplySSH writes the drop-in, keeping it only if `sshd -t` accepts it,
// then reloads sshd so open sessions survive. It refuses to turn password
// login off unless a key would still let someone in.
func (a *Agent) ApplySSH(ctx context.Context, s agent.SSHSettings) error {
	bin := a.sshdBinary()
	if bin == "" {
		return refusal("sshd isn't installed: enable it first")
	}
	if !s.PasswordAuthentication {
		st, err := a.SSH(ctx)
		if err != nil {
			return err
		}
		usable := slices.ContainsFunc(st.Keys, func(k agent.AuthorizedKey) bool { return k.User != "root" || s.PermitRootLogin != "no" })
		if !usable {
			return refusal("no SSH Key is installed for a user who could still log in: install one before turning password login off")
		}
	}
	if err := a.ensureInclude(); err != nil {
		return err
	}
	path := a.path(dropIn)
	old, readErr := os.ReadFile(path)
	content := fmt.Sprintf("# Written by the ddash Host Agent. Change it in ddash.\nPasswordAuthentication %s\nKbdInteractiveAuthentication %s\nPermitRootLogin %s\n",
		yesNo(s.PasswordAuthentication), yesNo(s.PasswordAuthentication), s.PermitRootLogin)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := agent.WriteFileAtomic(path, []byte(content), 0o644); err != nil {
		return err
	}
	var out []string
	if err := a.o.Run(ctx, nil, bin, []string{"-t"}, func(l string) { out = append(out, l) }); err != nil {
		if readErr == nil {
			_ = agent.WriteFileAtomic(path, old, 0o644)
		} else {
			_ = os.Remove(path)
		}
		return fmt.Errorf("sshd -t refused the new settings, nothing changed: %s", strings.Join(out, " "))
	}
	switch a.os.Init {
	case "systemd":
		return a.o.Run(ctx, nil, "systemctl", []string{"reload-or-restart", a.sshUnit()}, nil)
	case "openrc":
		return a.o.Run(ctx, nil, "rc-service", []string{"sshd", "reload"}, nil)
	}
	return nil
}

// ensureInclude makes sshd_config read the drop-in directory, first.
func (a *Agent) ensureInclude() error {
	path := a.path("/etc/ssh/sshd_config")
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("can't read sshd_config: %w", err)
	}
	for line := range strings.Lines(string(b)) {
		f := strings.Fields(line)
		if len(f) >= 2 && strings.EqualFold(f[0], "Include") && strings.Contains(f[1], "sshd_config.d") {
			return nil
		}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return agent.WriteFileAtomic(path, append([]byte("Include /etc/ssh/sshd_config.d/*.conf\n"), b...), fi.Mode().Perm())
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
