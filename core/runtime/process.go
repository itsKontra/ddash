package runtime

// One Component's process: launch handshake, Start, health, crashes,
// restart and reconfigure.

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/itsKontra/ddash/core/config"
	componentpb "github.com/itsKontra/ddash/proto/ddash/component"
	"github.com/itsKontra/ddash/sdk"
)

// healthLoop polls every launched Component's health each HealthInterval.
func (r *Runtime) healthLoop(ctx context.Context) {
	ticker := time.NewTicker(HealthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		r.mu.Lock()
		var names []string
		for name, c := range r.components {
			if c.proc != nil {
				names = append(names, name)
			}
		}
		r.mu.Unlock()
		for _, name := range names {
			r.loops.Go(func() { r.pollHealth(ctx, name) })
		}
	}
}

// watch waits for proc to end and handles that as a crash, unless the

// runtime stopped it.
func (r *Runtime) watch(ctx context.Context, name string, proc Process) {
	select {
	case <-ctx.Done():
		return
	case <-proc.Done():
	}
	r.mu.Lock()
	c := r.components[name]
	if c == nil || c.proc != proc || ctx.Err() != nil {
		r.mu.Unlock()
		return // stopped on purpose
	}
	c.proc, c.started = nil, false
	now := time.Now()
	c.crashes = append(slices.DeleteFunc(c.crashes, func(t time.Time) bool {
		return now.Sub(t) >= crashWindow
	}), now)
	var delay time.Duration
	n := len(c.crashes)
	if n >= crashLimit {
		c.held = true
		c.status = Status{State: Crashed, Message: fmt.Sprintf("crashed %d times in %s; restart it from the Catalogue", n, crashWindow)}
	} else {
		delay = retryMin << (n - 1)
		c.notBefore = now.Add(delay)
		c.status = Status{State: Crashed, Message: fmt.Sprintf("exited unexpectedly; restarting in %s", delay)}
	}
	r.mu.Unlock()
	r.log.Error("crashed", "component", name, "crashes", n, "restart_in", delay)

	stopCtx, cancel := context.WithTimeout(ctx, StopTimeout)
	proc.Stop(stopCtx) // releases what is left of it
	cancel()
	if delay == 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(delay):
		r.poke()
	}
}

// Restart stops a wanted Component and starts it again, forgetting its

// crashes. The Components that require it keep running.
func (r *Runtime) Restart(ctx context.Context, name string) error {
	r.changing.Lock()
	defer r.changing.Unlock()
	r.mu.Lock()
	c, ok := r.components[name]
	if !ok || !r.wanted()[name] {
		r.mu.Unlock()
		return status.Errorf(codes.NotFound, "no installed Plugin requires %s", name)
	}
	proc := c.proc
	c.proc, c.started = nil, false
	c.crashes, c.held, c.notBefore = nil, false, time.Time{}
	c.status = Status{State: Starting}
	r.mu.Unlock()
	if proc != nil {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), StopTimeout)
		proc.Stop(stopCtx)
		cancel()
	}
	r.poke()
	return nil
}

// Reconfigure restarts a wanted Component's process with cfg, a candidate

// config with its defaults applied, and returns once it has polled the

// health after Start. When Start rejects cfg, the error is returned and

// the Component restarts on its stored config. The Components that require

// it see Unavailable meanwhile.
func (r *Runtime) Reconfigure(ctx context.Context, name string, cfg proto.Message) error {
	r.changing.Lock()
	defer r.changing.Unlock()
	r.mu.Lock()
	c, ok := r.components[name]
	loops := r.ctx
	if !ok || !r.wanted()[name] || loops == nil {
		r.mu.Unlock()
		return status.Errorf(codes.NotFound, "no installed Plugin requires %s", name)
	}
	proc := c.proc
	c.proc, c.started = nil, false
	c.crashes, c.held, c.notBefore = nil, false, time.Time{}
	c.status = Status{State: Starting}
	r.mu.Unlock()
	r.stop(ctx, proc)

	startErr, err := r.launch(loops, name, cfg)
	if err == nil && startErr == nil {
		r.pollHealth(loops, name)
		return nil
	}
	if err != nil {
		r.setStatus(name, launchFailed(err))
	}
	// Back to the stored config.
	r.mu.Lock()
	proc = c.proc
	c.proc, c.started = nil, false
	r.mu.Unlock()
	r.stop(ctx, proc)
	if _, err := r.launch(loops, name, nil); err != nil {
		r.log.Error("relaunch on the stored config", "component", name, "err", err)
		r.setStatus(name, launchFailed(err))
		r.poke() // the reconcile loop retries
	}
	return cmp.Or(startErr, err)
}

// stop stops proc, if any, waiting at most StopTimeout.
func (r *Runtime) stop(ctx context.Context, proc Process) {
	if proc == nil {
		return
	}
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), StopTimeout)
	defer cancel()
	proc.Stop(stopCtx)
}

// describe asks the Component what it is and builds its Endpoint (without

// Conn) and the descriptor of its config message (nil when it takes none).

// It refuses a Component that says it is another one or of another Release.
func describe(ctx context.Context, lc componentpb.LifecycleServiceClient, name, release string) (Endpoint, protoreflect.MessageDescriptor, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	d, err := lc.Describe(ctx, &componentpb.DescribeRequest{})
	if err != nil {
		return Endpoint{}, nil, fmt.Errorf("describe: %w", err)
	}
	if d.GetName() != name {
		return Endpoint{}, nil, fmt.Errorf("describe: launched %s, but it says it is %s", name, d.GetName())
	}
	if d.GetRelease() != release {
		return Endpoint{}, nil, fmt.Errorf("describe: %s is of Release %s, but this is Release %s", name, d.GetRelease(), release)
	}
	files, err := protodesc.NewFiles(d.GetDescriptors())
	if err != nil {
		return Endpoint{}, nil, fmt.Errorf("describe: descriptors: %w", err)
	}
	svc, err := findDescriptor[protoreflect.ServiceDescriptor](files, d.GetService())
	if err != nil {
		return Endpoint{}, nil, err
	}
	var cfg protoreflect.MessageDescriptor
	if d.GetConfigMessage() != "" {
		if cfg, err = findDescriptor[protoreflect.MessageDescriptor](files, d.GetConfigMessage()); err != nil {
			return Endpoint{}, nil, err
		}
	}
	return Endpoint{Service: svc, Types: dynamicpb.NewTypes(files)}, cfg, nil
}

func findDescriptor[D protoreflect.Descriptor](files *protoregistry.Files, name string) (D, error) {
	var zero D
	desc, err := files.FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		return zero, fmt.Errorf("describe: %s: %w", name, err)
	}
	d, ok := desc.(D)
	if !ok {
		return zero, fmt.Errorf("describe: %s is a %T", name, desc)
	}
	return d, nil
}

// start starts the Component with cfg, or else with its stored config.
func (r *Runtime) start(ctx context.Context, lc componentpb.LifecycleServiceClient, name string, cfgDesc protoreflect.MessageDescriptor, cfg proto.Message) error {
	var b []byte
	if cfgDesc != nil {
		if cfg == nil {
			var err error
			if cfg, err = r.opts.Config.Load(ctx, name, cfgDesc); err != nil {
				return err
			}
		}
		// A stored config a newer Release's rules refuse, e.g. one
		// without a field that became required, never reaches Start.
		if err := config.Validate(cfg.ProtoReflect()).Err(); err != nil {
			return err
		}
		var err error
		if b, err = proto.Marshal(cfg); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	_, err := lc.Start(ctx, &componentpb.StartRequest{Config: b, PublicUrl: r.opts.PublicURL})
	return err
}

// pollHealth asks one Component for its health and records the answer.
func (r *Runtime) pollHealth(ctx context.Context, name string) {
	r.mu.Lock()
	c := r.components[name]
	if c == nil || c.proc == nil {
		r.mu.Unlock()
		return
	}
	proc := c.proc
	lc := componentpb.NewLifecycleServiceClient(proc.Conn())
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	resp, err := lc.Health(ctx, &componentpb.HealthRequest{})
	if ctx.Err() != nil && err != nil {
		return // stopping
	}
	var st Status
	switch {
	case status.Code(err) == codes.FailedPrecondition:
		st = Status{State: Unconfigured, Message: status.Convert(err).Message()}
	case err != nil:
		st = Status{State: Down, Message: status.Convert(err).Message()}
	default:
		h := sdk.HealthFromProto(resp)
		st = Status{State: State(h.State.String()), Message: h.Message}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c := r.components[name]; c != nil && c.proc == proc {
		c.status = st // unless it crashed or restarted meanwhile
	}
}

func (r *Runtime) setStatus(name string, st Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.components[name]; ok {
		c.status = st
	}
}
