package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"google.golang.org/grpc"

	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/inmem"
)

// A Launcher starts Component processes.
type Launcher interface {
	// Launch starts the Component's process and connects to it. The
	// Component is not started: the runtime calls Describe and Start next.
	Launch(ctx context.Context, spec LaunchSpec) (Process, error)
}

// LaunchSpec is what a Launcher needs to run one Component.
type LaunchSpec struct {
	// Name is the Component's name.
	Name string
	// DBPath is the Component's SQLite file, /data/db/<name>.db.
	DBPath string
	// HostServer returns the server that carries the Component's
	// sdk.Require calls to the Components it requires. The launcher serves
	// it to the Component: in memory, or over go-plugin's GRPCBroker
	// (sdk.HostPlugins). Nil when the host has none.
	HostServer func(opts ...grpc.ServerOption) *grpc.Server
}

// A Process is one running Component.
type Process interface {
	// Conn reaches the Component's lifecycle service and its API.
	Conn() grpc.ClientConnInterface
	// Stop cancels the Component's ctx and waits for it to finish until ctx
	// ends, then ends the process.
	Stop(ctx context.Context)
	// Done is closed once the process has ended: after Stop, or on its own
	// when it crashed.
	Done() <-chan struct{}
}

// InProcess returns a Launcher that runs each of defs inside the host's own
// process, served over in-memory gRPC. Log lines go to logger, tagged with
// the Component's name.
func InProcess(logger *slog.Logger, defs ...sdk.Definition) Launcher {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	l := inProcess{log: logger, defs: map[string]sdk.Definition{}}
	for _, d := range defs {
		l.defs[d.Name()] = d
	}
	return l
}

type inProcess struct {
	log  *slog.Logger
	defs map[string]sdk.Definition
}

func (l inProcess) Launch(_ context.Context, spec LaunchSpec) (Process, error) {
	def, ok := l.defs[spec.Name]
	if !ok {
		return nil, fmt.Errorf("%s isn't built into this host", spec.Name)
	}
	opts := sdk.InstanceOptions{
		DBPath: spec.DBPath,
		Logger: l.log.With("component", spec.Name),
	}
	stopHost := func() {}
	if spec.HostServer != nil {
		host, stop, err := inmem.ServeServer(spec.Name+".host", spec.HostServer())
		if err != nil {
			return nil, err
		}
		opts.Host, stopHost = host, stop
	}
	inst := sdk.NewInstance(def, opts)
	conn, stop, err := inmem.Serve(spec.Name, inst.Register)
	if err != nil {
		stopHost()
		return nil, err
	}
	return &inProcessComponent{inst: inst, conn: conn, stop: stop, stopHost: stopHost, done: make(chan struct{})}, nil
}

type inProcessComponent struct {
	inst     *sdk.Instance
	conn     *grpc.ClientConn
	stop     func()
	stopHost func()
	done     chan struct{}
	once     sync.Once
}

func (c *inProcessComponent) Conn() grpc.ClientConnInterface { return c.conn }

func (c *inProcessComponent) Done() <-chan struct{} { return c.done }

func (c *inProcessComponent) Stop(ctx context.Context) {
	c.once.Do(func() {
		c.inst.Stop(ctx)
		c.stop()
		c.stopHost()
		close(c.done)
	})
}
