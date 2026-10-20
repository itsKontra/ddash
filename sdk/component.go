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

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"go.yaml.in/yaml/v3"
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
	return Definition{
		manifest:   m,
		service:    s.Service,
		migrations: s.Migrations,
		newConfig:  func() proto.Message { return zero.ProtoReflect().New().Interface() },
		newImpl:    func() component { return typed[C]{s.New()} },
	}
}

// Name is the Component's name from its manifest.
func (d Definition) Name() string { return d.manifest.Name }
