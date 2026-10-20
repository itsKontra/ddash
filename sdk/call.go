package sdk

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// The gRPC metadata that carries who a call runs for along a chain of
// calls. The host sets it on every call it routes to a Component, and the
// SDK carries it onto the calls a Component makes through Require.
const (
	// ActorMetadata is the actor, e.g. "operator:alice" or "component:checks".
	ActorMetadata = "ddash-actor"
	// SubMetadata is the Operator's OIDC subject, when the actor is one.
	SubMetadata = "ddash-actor-sub"
	// ChainMetadata links every hop of one Action in the Audit Log.
	ChainMetadata = "ddash-chain-id"
)

// call is who a call runs for, as the host said.
type call struct {
	actor, sub, chain string
}

type callKey struct{}

// Actor returns who the call handled under ctx runs for, e.g.
// "operator:alice". Work the Component started itself runs for
// "component:<name>".
func Actor(ctx context.Context) string {
	c, _ := ctx.Value(callKey{}).(call)
	return c.actor
}

// fromHost returns ctx carrying who the incoming call runs for: the server
// side of the SDK interceptor.
func fromHost(ctx context.Context) context.Context {
	md, _ := metadata.FromIncomingContext(ctx)
	first := func(key string) string {
		if v := md.Get(key); len(v) > 0 {
			return v[0]
		}
		return ""
	}
	return context.WithValue(ctx, callKey{}, call{
		actor: first(ActorMetadata),
		sub:   first(SubMetadata),
		chain: first(ChainMetadata),
	})
}

// onward wraps the connection to the host so every call through it carries
// the actor and chain id of the call it is made for: the client side of the
// SDK interceptor. Work the Component started itself is stamped
// "component:<name>" and gets a new chain from the host.
type onward struct {
	grpc.ClientConnInterface
	name string
}

func (o onward) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	return o.ClientConnInterface.Invoke(o.carry(ctx), method, args, reply, opts...)
}

func (o onward) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	return o.ClientConnInterface.NewStream(o.carry(ctx), desc, method, opts...)
}

func (o onward) carry(ctx context.Context) context.Context {
	c, _ := ctx.Value(callKey{}).(call)
	if c.actor == "" {
		c = call{actor: "component:" + o.name}
	}
	kv := []string{ActorMetadata, c.actor}
	if c.sub != "" {
		kv = append(kv, SubMetadata, c.sub)
	}
	if c.chain != "" {
		kv = append(kv, ChainMetadata, c.chain)
	}
	return metadata.AppendToOutgoingContext(ctx, kv...)
}
