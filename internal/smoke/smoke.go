// Package smoke runs every system Component's getters against real systems
// (`make smoke`) and, with a Record, also writes what they answered as
// scrubbed fixtures (`make record`). It is run by hand from a dev machine on
// the LAN, never in CI or on a schedule: cmd/smoke reads the gitignored
// smoke.env and scrub.map.
//
// Each Component runs in-process with its config from smoke.env (variable
// EnvVar(name), the config as protojson) and its DB in DataDir, so
// nginx-config's key survives between runs and is authorized once. Every
// getter must answer, its invariants must hold and the Component must be
// healthy. Recording puts a fixture.Recorder between an HTTP Component and
// its system and captures `nginx -T` with nginx-config's key afterwards;
// every file goes through the Scrubber, and a file it refuses fails the run.
package smoke

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/itsKontra/ddash/core/config"
	"github.com/itsKontra/ddash/internal/scrub"
	ddashpb "github.com/itsKontra/ddash/proto/ddash"
	componentpb "github.com/itsKontra/ddash/proto/ddash/component"
	configpb "github.com/itsKontra/ddash/proto/ddash/config"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/fixture"
	"github.com/itsKontra/ddash/sdk/inmem"
)

// CallTimeout bounds one getter call, as the host's getter deadline would.
const CallTimeout = 30 * time.Second

// Options shape a run.
type Options struct {
	// Env is smoke.env: each system Component's config under EnvVar(name).
	Env map[string]string
	// DataDir holds the Components' DBs between runs.
	DataDir string
	// Record, when set, also writes fixtures.
	Record *Record
	// Log hears one line per getter, health and recording. Nil discards.
	Log io.Writer
}

// Record says where recorded fixtures go and how they are scrubbed.
type Record struct {
	// Root is the repo: a Component's fixtures go to
	// <Root>/components/<name>/testdata/<Scenario>/.
	Root     string
	Scenario string
	Scrubber *scrub.Scrubber
}

// Components lists the system Components smoke runs, in order.
func Components() []string {
	var out []string
	for _, s := range systems {
		out = append(out, s.def.Name())
	}
	return out
}

// EnvVar is the smoke.env variable holding component's config:
// SMOKE_PFSENSE_API for pfsense-api.
func EnvVar(component string) string {
	return "SMOKE_" + strings.ToUpper(strings.ReplaceAll(component, "-", "_"))
}

// Run runs every system Component and returns every failure, joined. A
// failing Component doesn't stop the others.
func Run(ctx context.Context, opts Options) error {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return err
	}
	var errs []error
	for _, s := range systems {
		errs = append(errs, runSystem(ctx, s, opts)...)
	}
	if len(errs) > 0 {
		fmt.Fprintf(opts.Log, "FAILED: %d problem(s)\n", len(errs))
	}
	return errors.Join(errs...)
}

func runSystem(ctx context.Context, s system, opts Options) []error {
	name := s.def.Name()
	fail := func(format string, args ...any) error {
		err := fmt.Errorf(name+" "+format, args...)
		fmt.Fprintf(opts.Log, "FAIL %s\n", err)
		return err
	}
	text, ok := opts.Env[EnvVar(name)]
	if !ok || strings.TrimSpace(text) == "" {
		return []error{fail("is not configured: set %s in smoke.env", EnvVar(name))}
	}
	cfg := s.config()
	if err := protojson.Unmarshal([]byte(text), cfg); err != nil {
		return []error{fail("config %s: %v", EnvVar(name), err)}
	}
	if err := config.ApplyDefaults(cfg); err != nil {
		return []error{fail("config: %v", err)}
	}
	if fe := config.Validate(cfg.ProtoReflect()); len(fe) > 0 {
		return []error{fail("config %s: %v", EnvVar(name), fe.Err())}
	}

	var errs []error
	var dir string
	if opts.Record != nil {
		dir = filepath.Join(opts.Record.Root, "components", name, "testdata", opts.Record.Scenario)
	}
	startCfg := cfg
	if opts.Record != nil && s.proxy != nil {
		startCfg = proto.Clone(cfg)
		stop, err := recordingProxy(s, startCfg, dir, opts, func(file string, err error) {
			errs = append(errs, fail("%s not recorded: %v", file, err))
		})
		if err != nil {
			return []error{fail("recording: %v", err)}
		}
		defer stop()
	}

	dbPath := filepath.Join(opts.DataDir, name+".db")
	errs = append(errs, exercise(ctx, s, startCfg, dbPath, opts.Log, fail)...)

	if opts.Record != nil && s.capture != nil {
		command, out, err := s.capture(ctx, dbPath, cfg)
		if err == nil {
			err = fixture.WriteCommand(dir, command, out, opts.Record.Scrubber.Fixture)
		}
		if err != nil {
			errs = append(errs, fail("recording %s: %v", command, err))
		} else {
			fmt.Fprintf(opts.Log, "recorded %s %s\n", name, command)
		}
	}
	return errs
}

// exercise starts the Component with cfg, calls every getter and checks
// its health, then stops it.
func exercise(ctx context.Context, s system, cfg proto.Message, dbPath string, log io.Writer, fail func(string, ...any) error) []error {
	name := s.def.Name()
	inst := sdk.NewInstance(s.def, sdk.InstanceOptions{
		DBPath: dbPath,
		Logger: slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelWarn})).With("component", name),
	})
	defer inst.Stop(context.Background())
	conn, stop, err := inmem.Serve(name, inst.Register)
	if err != nil {
		return []error{fail("serve: %v", err)}
	}
	defer stop()
	b, err := proto.Marshal(cfg)
	if err != nil {
		return []error{fail("config: %v", err)}
	}
	lifecycle := componentpb.NewLifecycleServiceClient(conn)
	if _, err := lifecycle.Start(ctx, &componentpb.StartRequest{Config: b}); err != nil {
		return []error{fail("Start: %v", err)}
	}

	svc, err := service(s.service)
	if err != nil {
		return []error{fail("%v", err)}
	}
	var errs []error
	answers := map[string]proto.Message{}
	methods := svc.Methods()
	for i := range methods.Len() {
		m := methods.Get(i)
		if !proto.HasExtension(m.Options(), ddashpb.E_Getter) {
			continue
		}
		req, resp, err := messages(m)
		if build := s.requests[string(m.Name())]; err == nil && build != nil {
			req, err = build(answers)
		}
		if err != nil {
			errs = append(errs, fail("%s: %v", m.Name(), err))
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, CallTimeout)
		err = conn.Invoke(callCtx, fmt.Sprintf("/%s/%s", svc.FullName(), m.Name()), req, resp)
		cancel()
		if err == nil && s.checks[string(m.Name())] != nil {
			err = s.checks[string(m.Name())](resp)
		}
		if err != nil {
			errs = append(errs, fail("%s: %v", m.Name(), err))
			continue
		}
		answers[string(m.Name())] = resp
		fmt.Fprintf(log, "ok %s %s\n", name, m.Name())
	}

	h, err := lifecycle.Health(ctx, &componentpb.HealthRequest{})
	switch health := sdk.HealthFromProto(h); {
	case err != nil:
		errs = append(errs, fail("health: %v", err))
	case health.State != sdk.OK:
		msg := fmt.Sprintf("health: %s: %s", health.State, health.Message)
		if hint := setupHint(cfg, answers); hint != "" {
			msg += "\n  " + hint
		}
		errs = append(errs, fail("%s", msg))
	default:
		fmt.Fprintf(log, "ok %s health\n", name)
	}
	return errs
}

// recordingProxy serves a fixture.Recorder to s's real system on a free
// local port and points cfg at it. stop ends it.
func recordingProxy(s system, cfg proto.Message, dir string, opts Options, refused func(string, error)) (stop func(), err error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	upstream, tlsName, err := s.proxy(cfg, ln.Addr().String())
	if err != nil {
		ln.Close()
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if tlsName != "" {
		transport.TLSClientConfig.ServerName = tlsName
	}
	var recorded atomic.Int64
	srv := &http.Server{
		Handler: fixture.Recorder(upstream, dir, fixture.RecordOptions{
			Scrub:     opts.Record.Scrubber.Fixture,
			Transport: transport,
			Refused:   refused,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	srv.Handler = countRequests(srv.Handler, &recorded)
	go srv.Serve(ln) //nolint:errcheck // ends with Close
	return func() {
		srv.Close()
		fmt.Fprintf(opts.Log, "recorded %s: %d response(s) into %s\n", s.def.Name(), recorded.Load(), dir)
	}, nil
}

func countRequests(h http.Handler, n *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h.ServeHTTP(w, r)
	})
}

// service is the Component's API.
func service(name string) (protoreflect.ServiceDescriptor, error) {
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(name))
	if err != nil {
		return nil, fmt.Errorf("service %s: %w", name, err)
	}
	return d.(protoreflect.ServiceDescriptor), nil
}

// messages makes an empty request and response for m.
func messages(m protoreflect.MethodDescriptor) (req, resp proto.Message, err error) {
	in, err := protoregistry.GlobalTypes.FindMessageByName(m.Input().FullName())
	if err != nil {
		return nil, nil, err
	}
	out, err := protoregistry.GlobalTypes.FindMessageByName(m.Output().FullName())
	if err != nil {
		return nil, nil, err
	}
	return in.New().Interface(), out.New().Interface(), nil
}

// setupHint is the block of the setup hint the Component offers (its
// config's setup_from getter), such as nginx-config's authorized_keys line.
func setupHint(cfg proto.Message, answers map[string]proto.Message) string {
	form, _ := proto.GetExtension(cfg.ProtoReflect().Descriptor().Options(), configpb.E_Form).(*configpb.Form)
	answer := answers[form.GetSetupFrom()]
	if answer == nil {
		return ""
	}
	var block string
	answer.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if hint, ok := v.Interface().(protoreflect.Message); ok && fd.Message().FullName() == "ddash.config.SetupHint" {
			block = hint.Interface().(*configpb.SetupHint).GetBlock()
			return false
		}
		return true
	})
	if block == "" {
		return ""
	}
	return "setup hint: " + block
}
