package sdk

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// HostBrokerID is the GRPCBroker service id the host serves its proxy on
// and every Component dials to reach it.
const HostBrokerID uint32 = 1

// Plugins is the go-plugin plugin set serving inst in a Component's
// process. The host passes HostPlugins in its ClientConfig instead.
func Plugins(inst *Instance) plugin.PluginSet {
	return plugin.PluginSet{PluginName: &grpcPlugin{inst: inst}}
}

// HostPlugins is the plugin set the host passes in its ClientConfig for
// one Component. Dispensing it returns a *grpc.ClientConn to the
// Component's lifecycle and API, and serves hostServer (the router's
// HostServer for that Component) on the GRPCBroker at HostBrokerID, where
// the Component's sdk.Require clients reach it. hostServer may be nil.
// The ClientConfig must set GRPCBrokerMultiplex, since the Component dials
// the host only on its first Require'd call.
func HostPlugins(hostServer func(opts ...grpc.ServerOption) *grpc.Server) plugin.PluginSet {
	return plugin.PluginSet{PluginName: &grpcPlugin{hostServer: hostServer}}
}

type grpcPlugin struct {
	plugin.NetRPCUnsupportedPlugin
	inst       *Instance
	hostServer func(opts ...grpc.ServerOption) *grpc.Server
}

func (p *grpcPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	if p.inst.opts.Host == nil {
		p.inst.opts.Host = &brokerHost{broker: broker}
	}
	p.inst.Register(s)
	return nil
}

func (p *grpcPlugin) GRPCClient(_ context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	if p.hostServer != nil {
		go broker.AcceptAndServe(HostBrokerID, func(opts []grpc.ServerOption) *grpc.Server {
			return p.hostServer(opts...)
		})
	}
	return c, nil
}

// brokerHost reaches the host over the GRPCBroker, dialing on first use and
// again after a failed dial.
type brokerHost struct {
	broker *plugin.GRPCBroker

	mu   sync.Mutex
	conn *grpc.ClientConn
}

func (b *brokerHost) dial() (*grpc.ClientConn, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn == nil {
		conn, err := b.broker.Dial(HostBrokerID)
		if err != nil {
			return nil, status.Errorf(codes.Unavailable, "no connection to the host: %v", err)
		}
		b.conn = conn
	}
	return b.conn, nil
}

func (b *brokerHost) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	conn, err := b.dial()
	if err != nil {
		return err
	}
	return conn.Invoke(ctx, method, args, reply, opts...)
}

func (b *brokerHost) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	conn, err := b.dial()
	if err != nil {
		return nil, err
	}
	return conn.NewStream(ctx, desc, method, opts...)
}
