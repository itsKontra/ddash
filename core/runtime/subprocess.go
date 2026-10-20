package runtime

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/itsKontra/ddash/sdk"
)

// SumsFile lists the checksum of every binary in a Release dir, in
// sha256sum's format: "<hex>  <file>" per line.
const SumsFile = "SHA256SUMS"

// exitPoll is how often a launched process is checked for having exited.
const exitPoll = 50 * time.Millisecond

// Subprocess returns a Launcher that runs each Component as a go-plugin
// subprocess: the binary dir/<name>, verified by go-plugin's SecureConfig
// against its line in dir/SHA256SUMS before it runs, over gRPC with
// AutoMTLS. The Components' log lines and go-plugin's own go to log as
// hclog JSON; nil discards them.
func Subprocess(dir string, log io.Writer) Launcher {
	if log == nil {
		log = io.Discard
	}
	return subprocess{dir: dir, log: log}
}

type subprocess struct {
	dir string
	log io.Writer
}

func (l subprocess) Launch(_ context.Context, spec LaunchSpec) (Process, error) {
	sum, err := l.checksum(spec.Name)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(filepath.Join(l.dir, spec.Name))
	cmd.Env = []string{sdk.EnvDBPath + "=" + spec.DBPath}
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:     sdk.Handshake,
		Plugins:             sdk.HostPlugins(spec.HostServer),
		Cmd:                 cmd,
		SecureConfig:        &plugin.SecureConfig{Checksum: sum, Hash: sha256.New()},
		AllowedProtocols:    []plugin.Protocol{plugin.ProtocolGRPC},
		AutoMTLS:            true,
		GRPCBrokerMultiplex: true,
		Logger: hclog.New(&hclog.LoggerOptions{
			Name:       "component",
			Level:      hclog.Info,
			Output:     l.log,
			JSONFormat: true,
		}),
	})
	rpc, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("launch %s: %w", spec.Name, err)
	}
	raw, err := rpc.Dispense(sdk.PluginName)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("launch %s: %w", spec.Name, err)
	}
	p := &process{client: client, rpc: rpc, cmd: cmd, conn: raw.(*grpc.ClientConn), done: make(chan struct{})}
	go p.watch()
	return p, nil
}

// checksum reads name's checksum from the Release dir's SHA256SUMS.
func (l subprocess) checksum(name string) ([]byte, error) {
	f, err := os.Open(filepath.Join(l.dir, SumsFile))
	if err != nil {
		return nil, fmt.Errorf("launch %s: %w", name, err)
	}
	defer f.Close()
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		sum, file, ok := strings.Cut(lines.Text(), "  ")
		if ok && strings.TrimPrefix(file, "*") == name {
			b, err := hex.DecodeString(sum)
			if err != nil || len(b) != sha256.Size {
				return nil, fmt.Errorf("launch %s: %s: bad checksum %q", name, SumsFile, sum)
			}
			return b, nil
		}
	}
	if err := lines.Err(); err != nil {
		return nil, fmt.Errorf("launch %s: %w", name, err)
	}
	return nil, fmt.Errorf("launch %s: it has no checksum in %s", name, SumsFile)
}

// process is one Component subprocess.
type process struct {
	client *plugin.Client
	rpc    plugin.ClientProtocol
	cmd    *exec.Cmd
	conn   *grpc.ClientConn
	done   chan struct{}
}

func (p *process) Conn() grpc.ClientConnInterface { return p.conn }

func (p *process) Done() <-chan struct{} { return p.done }

// watch closes done once the process has exited, for whatever reason.
func (p *process) watch() {
	t := time.NewTicker(exitPoll)
	defer t.Stop()
	for range t.C {
		if p.client.Exited() {
			close(p.done)
			return
		}
	}
}

// Stop asks the Component to stop, which makes sdk.Serve cancel its ctx and
// wait for its work, and kills the process if it hasn't exited by the time
// ctx ends.
func (p *process) Stop(ctx context.Context) {
	go p.rpc.Close() //nolint:errcheck // the process exits either way
	select {
	case <-p.done:
	case <-ctx.Done():
		p.cmd.Process.Kill() //nolint:errcheck // it may have just exited
		<-p.done
	}
}
