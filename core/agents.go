package core

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/itsKontra/ddash/core/runtime"
)

// The Host Agent's downloads, public like the GitHub release they come
// from: install.sh, which installs the Agent from this ddash, and the
// Agent's binary per architecture.
const (
	hostAgentPrefix = "/agent/host/"
	installPath     = hostAgentPrefix + "install.sh"
)

// hostAgentBinary serves the Host Agent for {arch} from the Release,
// verified against its SHA256SUMS.
func (h *Host) hostAgentBinary(w http.ResponseWriter, r *http.Request) {
	arch := r.PathValue("arch")
	if !slices.Contains(runtime.HostAgentArchs, arch) {
		http.Error(w, "the Host Agent is built for "+strings.Join(runtime.HostAgentArchs, " and "), http.StatusNotFound)
		return
	}
	bin, err := h.release.File(r.Context(), runtime.HostAgentFile(arch))
	if err != nil {
		h.log.Warn("host agent download", "arch", arch, "err", err)
		http.Error(w, "the Host Agent isn't available: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	f, err := os.Open(bin.Path)
	if err != nil {
		http.Error(w, "the Host Agent isn't available", http.StatusServiceUnavailable)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="ddash-host-agent"`)
	w.Header().Set("X-Checksum-Sha256", hex.EncodeToString(bin.Checksum))
	http.ServeContent(w, r, "ddash-host-agent", modTime(f), f)
}

// hostAgentSums answers the Host Agent binaries' SHA256SUMS.
func (h *Host) hostAgentSums(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	for _, arch := range runtime.HostAgentArchs {
		bin, err := h.release.File(r.Context(), runtime.HostAgentFile(arch))
		if err != nil {
			http.Error(w, "the Host Agent isn't available: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(&b, "%x  %s\n", bin.Checksum, runtime.HostAgentFile(arch))
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// installScript serves install.sh for this ddash: it downloads the Agent
// from here and checks it against the checksum ddash pins in the script.
func (h *Host) installScript(w http.ResponseWriter, r *http.Request) {
	sums := map[string]string{}
	for _, arch := range runtime.HostAgentArchs {
		bin, err := h.release.File(r.Context(), runtime.HostAgentFile(arch))
		if err != nil {
			h.log.Warn("install.sh", "arch", arch, "err", err)
			continue
		}
		sums[arch] = hex.EncodeToString(bin.Checksum)
	}
	if len(sums) == 0 {
		http.Error(w, "the Host Agent isn't available in this Release", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := install.Execute(w, struct {
		URL, Release string
		Sums         map[string]string
	}{strings.TrimRight(h.publicURL, "/"), h.release.Release(), sums}); err != nil {
		h.log.Warn("install.sh", "err", err)
	}
}

func modTime(f *os.File) time.Time {
	if fi, err := f.Stat(); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

var install = template.Must(template.New("install.sh").Parse(`#!/bin/sh
# The ddash Host Agent's installer, for {{.URL}} (Release {{.Release}}).
#
#   curl -fsSL {{.URL}}/agent/host/install.sh | sudo sh -s -- --code <enrolment code>
#   curl -fsSL {{.URL}}/agent/host/install.sh | sudo sh -s -- --uninstall
#
# It installs /usr/local/bin/ddash-host-agent, checked against the SHA-256
# below, and runs it as a root service (systemd; OpenRC on Alpine) on port
# 7444. The Host Agent has root-equivalent control of this host: ddash can
# update its packages and change its SSH settings through it. Supported:
# Ubuntu, Debian, Alpine, Fedora and Arch Linux, amd64 and arm64.
set -eu

DDASH='{{.URL}}'
{{range $arch, $sum := .Sums}}SHA256_{{$arch}}='{{$sum}}'
{{end}}
code=''
listen=':7444'
uninstall=''
while [ $# -gt 0 ]; do
	case "$1" in
	--code) code="$2"; shift 2 ;;
	--listen) listen="$2"; shift 2 ;;
	--uninstall) uninstall=1; shift ;;
	*) echo "install.sh: unknown option $1" >&2; exit 2 ;;
	esac
done

if [ "$(id -u)" != 0 ]; then
	echo "install.sh: run it as root (sudo sh -s -- ...)" >&2
	exit 1
fi

if [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
	init=systemd
elif command -v rc-service >/dev/null 2>&1; then
	init=openrc
else
	echo "install.sh: neither systemd nor OpenRC runs here" >&2
	exit 1
fi

if [ -n "$uninstall" ]; then
	if [ "$init" = systemd ]; then
		systemctl disable --now ddash-host-agent 2>/dev/null || true
		rm -f /etc/systemd/system/ddash-host-agent.service
		systemctl daemon-reload
	else
		rc-service ddash-host-agent stop 2>/dev/null || true
		rc-update del ddash-host-agent default 2>/dev/null || true
		rm -f /etc/init.d/ddash-host-agent
	fi
	rm -rf /usr/local/bin/ddash-host-agent /etc/ddash-host-agent /var/lib/ddash-host-agent
	echo "The ddash Host Agent is uninstalled. Keys ddash installed (marked ddash:<name>) stay in authorized_keys."
	exit 0
fi

if [ -z "$code" ]; then
	echo "install.sh: --code <enrolment code> is required: ddash shows it where you add the Host Agent" >&2
	exit 2
fi

. /etc/os-release
case " ${ID:-} ${ID_LIKE:-} " in
*" debian "*|*" ubuntu "*|*" fedora "*|*" rhel "*|*" alpine "*|*" arch "*) ;;
*) echo "install.sh: ${PRETTY_NAME:-this distribution} isn't supported (Ubuntu, Debian, Alpine, Fedora, Arch Linux)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
x86_64|amd64) arch=amd64; want="${SHA256_amd64:-}" ;;
aarch64|arm64) arch=arm64; want="${SHA256_arm64:-}" ;;
*) echo "install.sh: $(uname -m) isn't supported (amd64, arm64)" >&2; exit 1 ;;
esac

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
if command -v curl >/dev/null 2>&1; then
	curl -fsSL "$DDASH/agent/host/$arch" -o "$tmp"
else
	wget -qO "$tmp" "$DDASH/agent/host/$arch"
fi
got="$(sha256sum "$tmp" | cut -d' ' -f1)"
if [ -z "$want" ] || [ "$got" != "$want" ]; then
	echo "install.sh: the download's SHA-256 is $got, not ${want:-known}: not installing it" >&2
	exit 1
fi
install -m 0755 "$tmp" /usr/local/bin/ddash-host-agent

mkdir -p /etc/ddash-host-agent /var/lib/ddash-host-agent
chmod 0700 /etc/ddash-host-agent /var/lib/ddash-host-agent
umask 077
printf 'DDASH_ENROLMENT_CODE=%s\nDDASH_AGENT_LISTEN=%s\n' "$code" "$listen" > /etc/ddash-host-agent/env

if [ "$init" = systemd ]; then
	cat > /etc/systemd/system/ddash-host-agent.service <<'UNIT'
[Unit]
Description=ddash Host Agent
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=/etc/ddash-host-agent/env
ExecStart=/usr/local/bin/ddash-host-agent serve
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
UNIT
	systemctl daemon-reload
	systemctl enable ddash-host-agent >/dev/null
	systemctl restart ddash-host-agent
else
	cat > /etc/init.d/ddash-host-agent <<'RC'
#!/sbin/openrc-run
description="ddash Host Agent"
supervisor=supervise-daemon
command=/usr/local/bin/ddash-host-agent
command_args=serve
respawn_delay=3
depend() { need net; }
RC
	chmod 0755 /etc/init.d/ddash-host-agent
	rc-update add ddash-host-agent default >/dev/null
	rc-service ddash-host-agent restart
fi

echo "The ddash Host Agent runs on port ${listen#:}. ddash pairs with it within a minute."
`))
