package sdk

import (
	"context"
	"os"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

// Handshake is what the host and every Component binary agree on before
// talking. The host then checks name and Release through Describe.
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "DDASH_COMPONENT",
	MagicCookieValue: "a Component of ddash; run it from the ddash host",
}

// PluginName is the one plugin every Component binary serves. Dispensing it
// on the host returns a *grpc.ClientConn to the Component's lifecycle and API.
const PluginName = "component"

// EnvDBPath names the environment variable the host sets to the Component's
// SQLite file, /data/db/<name>.db.
const EnvDBPath = "DDASH_COMPONENT_DB"

// stopTimeout bounds how long Serve waits for background loops after the
// host stops the Component. The host kills the process after 10 s.
const stopTimeout = 8 * time.Second

// Serve runs def as a go-plugin subprocess until the host stops it. It is
// the whole main of a Component binary.
func Serve(def Definition) {
	hl := hclog.New(&hclog.LoggerOptions{Name: def.Name(), Output: os.Stderr, JSONFormat: true})
	inst := NewInstance(def, InstanceOptions{
		DBPath: os.Getenv(EnvDBPath),
		Logger: NewLogger(def.Name(), os.Stderr),
	})
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         Plugins(inst),
		GRPCServer:      plugin.DefaultGRPCServer,
		Logger:          hl,
	})
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	inst.Stop(ctx)
}

// Plugins is the go-plugin plugin set serving inst. The host passes
// Plugins(nil) in its ClientConfig.
func Plugins(inst *Instance) plugin.PluginSet {
	return plugin.PluginSet{PluginName: &grpcPlugin{inst: inst}}
}

type grpcPlugin struct {
	plugin.NetRPCUnsupportedPlugin
	inst *Instance
}

func (p *grpcPlugin) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	p.inst.Register(s)
	return nil
}

func (p *grpcPlugin) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return c, nil
}
