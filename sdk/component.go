// Package sdk is what a Component is built on: the go-plugin plumbing, the
// lifecycle the host drives (Describe, Start, Health), migrations, typed
// clients for required Components, background loops, caching and logging.
//
// A Component is a typed proto plus domain code:
//
//	func Definition() sdk.Definition {
//		return sdk.Define(sdk.Spec[*mypb.MyConfig]{
//			Manifest:   manifest,   // //go:embed component.yaml
//			Service:    &mypb.MyService_ServiceDesc,
//			Migrations: migrations, // //go:embed migrations, optional
//			New:        func() sdk.Component[*mypb.MyConfig] { return &component{} },
//		})
//	}
//
// and its binary's main is sdk.Serve(Definition()).
package sdk

import (
	"context"
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	ddashpb "github.com/itsKontra/ddash/proto/ddash"
	configpb "github.com/itsKontra/ddash/proto/ddash/config"
)

// Release is the Release the binary was built for, set at build time with
// -ldflags "-X github.com/itsKontra/ddash/sdk.Release=v1.2.3".
var Release = "dev"

// Component is the domain code of a Component. The value returned by
// Spec.New must also implement the generated server interface of
// Spec.Service; its methods are reached only after Start succeeded.
type Component[C proto.Message] interface {
	// Start validates cfg, connects and launches background work. ctx is
	// cancelled when the Component stops, so background work keeps it.
	// Start fails only for local reasons: return InvalidConfig for a bad
	// config, and report an unreachable upstream through Health instead.
	Start(ctx context.Context, env *Env, cfg C) error
	// Health reports whether the Component's own system works. For an API
	// Component that means its system is reachable with the configured
	// credentials.
	Health(ctx context.Context) Health
}

// Spec is everything the SDK needs to run a Component.
type Spec[C proto.Message] struct {
	// Manifest is the contents of the Component's component.yaml.
	Manifest []byte
	// Service is the generated grpc.ServiceDesc of the Component's API.
	Service *grpc.ServiceDesc
	// Migrations holds the Component's migrations under migrations/, as
	// NNNN_description.sql. A Component without migrations gets no DB.
	Migrations fs.FS
	// New returns a fresh, unstarted Component.
	New func() Component[C]
}

// Definition is a Component ready to serve, made by Define.
type Definition struct {
	manifest   manifest
	service    *grpc.ServiceDesc
	migrations fs.FS
	newConfig  func() proto.Message
	newImpl    func() component
}

// component is Component with its config type erased.
type component interface {
	start(ctx context.Context, env *Env, cfg proto.Message) error
	Health(ctx context.Context) Health
	impl() any
}

type typed[C proto.Message] struct{ c Component[C] }

func (t typed[C]) start(ctx context.Context, env *Env, cfg proto.Message) error {
	return t.c.Start(ctx, env, cfg.(C))
}
func (t typed[C]) Health(ctx context.Context) Health { return t.c.Health(ctx) }
func (t typed[C]) impl() any                         { return t.c }

type manifest struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Requires    []string `yaml:"requires"`
}

// Define checks a Spec and turns it into a Definition. It panics on a
// broken Spec, since that is a programming error caught by any test.
func Define[C proto.Message](s Spec[C]) Definition {
	var m manifest
	if err := yaml.Unmarshal(s.Manifest, &m); err != nil {
		panic(fmt.Sprintf("sdk: component.yaml: %v", err))
	}
	if m.Name == "" {
		panic("sdk: component.yaml has no name")
	}
	if s.Service == nil || s.New == nil {
		panic(fmt.Sprintf("sdk: %s: Spec needs Service and New", m.Name))
	}
	serverType := reflect.TypeOf(s.Service.HandlerType).Elem()
	if implType := reflect.TypeOf(s.New()); !implType.Implements(serverType) {
		panic(fmt.Sprintf("sdk: %s: %v does not implement %v", m.Name, implType, serverType))
	}
	var zero C
	d := Definition{
		manifest:   m,
		service:    s.Service,
		migrations: s.Migrations,
		newConfig:  func() proto.Message { return zero.ProtoReflect().New().Interface() },
		newImpl:    func() component { return typed[C]{s.New()} },
	}
	if err := d.checkMethods(); err != nil {
		panic(err.Error())
	}
	if err := d.checkSetupHint(zero.ProtoReflect().Descriptor()); err != nil {
		panic(err.Error())
	}
	if err := d.checkChoices(zero.ProtoReflect().Descriptor()); err != nil {
		panic(err.Error())
	}
	return d
}

// checkChoices enforces that a config field's choices_from is on a
// top-level string and names "<component>.<Getter>" of a required
// Component. Whether that getter answers Choices only the host can tell.
func (d Definition) checkChoices(cfg protoreflect.MessageDescriptor) error {
	fields := cfg.Fields()
	for i := range fields.Len() {
		f := fields.Get(i)
		opts, _ := proto.GetExtension(f.Options(), configpb.E_Field).(*configpb.Field)
		from := opts.GetChoicesFrom()
		if from == "" {
			continue
		}
		if f.Kind() != protoreflect.StringKind || f.IsList() {
			return fmt.Errorf("sdk: %s: %s has choices_from but isn't a string", d.Name(), f.FullName())
		}
		component, getter, ok := strings.Cut(from, ".")
		if !ok || component == "" || getter == "" {
			return fmt.Errorf("sdk: %s: %s's choices_from %q isn't \"<component>.<Getter>\"", d.Name(), f.FullName(), from)
		}
		if !slices.Contains(d.manifest.Requires, component) {
			return fmt.Errorf("sdk: %s: %s's choices_from %q names %s, which %s doesn't require", d.Name(), f.FullName(), from, component, d.Name())
		}
	}
	return nil
}

// checkSetupHint enforces that a config message's setup_from names a
// getter of the Component's API whose response has a SetupHint field, so
// the host can ask it for the hint.
func (d Definition) checkSetupHint(cfg protoreflect.MessageDescriptor) error {
	form, _ := proto.GetExtension(cfg.Options(), configpb.E_Form).(*configpb.Form)
	name := form.GetSetupFrom()
	if name == "" {
		return nil
	}
	svc, err := d.serviceDescriptor()
	if err != nil {
		return err
	}
	m := svc.Methods().ByName(protoreflect.Name(name))
	if m == nil || !proto.HasExtension(m.Options(), ddashpb.E_Getter) {
		return fmt.Errorf("sdk: %s: %s's setup_from %q is not a getter of %s", d.Name(), cfg.FullName(), name, svc.FullName())
	}
	hint := (&configpb.SetupHint{}).ProtoReflect().Descriptor().FullName()
	fields := m.Output().Fields()
	for i := range fields.Len() {
		if f := fields.Get(i); f.Message() != nil && f.Message().FullName() == hint && !f.IsList() {
			return nil
		}
	}
	return fmt.Errorf("sdk: %s: %s's setup_from %s answers %s, which has no ddash.config.SetupHint field", d.Name(), cfg.FullName(), name, m.Output().FullName())
}

// checkMethods enforces that every API method is unary and exactly one of a
// getter or an Action, since the host derives routing, refresh, confirms
// and the Audit Log from that.
func (d Definition) checkMethods() error {
	svc, err := d.serviceDescriptor()
	if err != nil {
		return err
	}
	methods := svc.Methods()
	for i := range methods.Len() {
		m := methods.Get(i)
		if m.IsStreamingClient() || m.IsStreamingServer() {
			return fmt.Errorf("sdk: %s: %s streams; Component methods are unary only", d.Name(), m.FullName())
		}
		opts := m.Options()
		getter := proto.HasExtension(opts, ddashpb.E_Getter)
		action := proto.HasExtension(opts, ddashpb.E_Action)
		if getter == action {
			return fmt.Errorf("sdk: %s: %s must be exactly one of a getter or an Action: option (ddash.getter) or (ddash.action)", d.Name(), m.FullName())
		}
	}
	return nil
}

// Name is the Component's name from its manifest.
func (d Definition) Name() string { return d.manifest.Name }

// Description is the Component's description from its manifest.
func (d Definition) Description() string { return d.manifest.Description }

// Requires lists the bare names of the Components this one calls, from its
// manifest.
func (d Definition) Requires() []string { return d.manifest.Requires }
