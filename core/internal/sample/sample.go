// Package sample is a Component the host's own tests run under any name,
// with hooks for its Start and Health. It is not shipped.
package sample

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/sdk"
)

// Hooks change how a sample behaves. Nil hooks do the default: Start checks
// the target, and Health is ok.
type Hooks struct {
	// Start runs after the target check.
	Start func(ctx context.Context, env *sdk.Env, cfg *samplepb.SampleConfig) error
	// Health replaces the sample's Health.
	Health func(ctx context.Context) sdk.Health
	// Act replaces the sample's Act, which answers with the actor it ran
	// for. env is the Env the sample started with.
	Act func(ctx context.Context, env *sdk.Env, req *samplepb.ActRequest) (*samplepb.ActResponse, error)
}

// migrations give every sample a DB file of its own.
//
//go:embed migrations/*.sql
var migrations embed.FS

// Define returns a sample named name that requires requires.
func Define(name string, requires []string, hooks Hooks) sdk.Definition {
	manifest := fmt.Sprintf("name: %s\ndescription: A sample for the host's tests.\nrequires: [%s]\n",
		name, strings.Join(requires, ", "))
	return sdk.Define(sdk.Spec[*samplepb.SampleConfig]{
		Manifest:   []byte(manifest),
		Service:    &samplepb.SampleService_ServiceDesc,
		Migrations: migrations,
		New:        func() sdk.Component[*samplepb.SampleConfig] { return &component{hooks: hooks} },
	})
}

type component struct {
	samplepb.UnimplementedSampleServiceServer
	hooks Hooks
	cfg   *samplepb.SampleConfig
	env   *sdk.Env
}

func (c *component) Start(ctx context.Context, env *sdk.Env, cfg *samplepb.SampleConfig) error {
	if cfg.GetTarget() == "" {
		var errs sdk.FieldErrors
		errs.Add("target", "is required")
		return errs.Err()
	}
	c.cfg = cfg
	c.env = env
	if c.hooks.Start != nil {
		return c.hooks.Start(ctx, env, cfg)
	}
	return nil
}

func (c *component) Health(ctx context.Context) sdk.Health {
	if c.hooks.Health != nil {
		return c.hooks.Health(ctx)
	}
	return sdk.Health{State: sdk.OK}
}

func (c *component) Greet(_ context.Context, req *samplepb.GreetRequest) (*samplepb.GreetResponse, error) {
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	return &samplepb.GreetResponse{
		Greeting: c.cfg.GetGreeting() + " " + req.GetName(),
		Config:   proto.CloneOf(c.cfg),
	}, nil
}

func (c *component) Wait(ctx context.Context, _ *samplepb.WaitRequest) (*samplepb.WaitResponse, error) {
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}

func (c *component) Act(ctx context.Context, req *samplepb.ActRequest) (*samplepb.ActResponse, error) {
	if c.hooks.Act != nil {
		return c.hooks.Act(ctx, c.env, req)
	}
	return &samplepb.ActResponse{Actor: sdk.Actor(ctx)}, nil
}

func (c *component) WaitAction(ctx context.Context, _ *samplepb.WaitActionRequest) (*samplepb.WaitActionResponse, error) {
	<-ctx.Done()
	return nil, status.FromContextError(ctx.Err()).Err()
}
