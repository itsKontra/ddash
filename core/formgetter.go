package core

import (
	"context"
	"fmt"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/router"
	"github.com/itsKontra/ddash/sdk"
)

// errNotAGetter is formGetter's answer for a name that is no getter of
// the Component: a mistake in its config message, not an outage.
type errNotAGetter struct{ component, getter string }

func (e errNotAGetter) Error() string {
	return fmt.Sprintf("%s has no getter %s", e.component, e.getter)
}

// formGetter calls a started Component's getter as the core, on behalf of
// a config form, and copies the response's non-repeated field of out's
// type into out. It fails when the Component isn't up, isn't reachable,
// has no such getter, or the response has no such field set.
func (h *Host) formGetter(ctx context.Context, component, getter string, out proto.Message) error {
	ep, err := h.runtime.Endpoint(component)
	if err != nil {
		return err
	}
	md := ep.Service.Methods().ByName(protoreflect.Name(getter))
	if md == nil || appstate.IsAction(md) {
		return errNotAGetter{component, getter}
	}
	ctx, cancel := context.WithTimeout(ctx, router.GetterDeadline)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx,
		sdk.ActorMetadata, auth.Actor{Kind: auth.Core}.String(), sdk.ChainMetadata, router.NewChain())
	resp := dynamicpb.NewMessage(md.Output())
	if err := ep.Conn.Invoke(ctx, router.FullMethod(ep, md), dynamicpb.NewMessage(md.Input()), resp); err != nil {
		return err
	}
	want := out.ProtoReflect().Descriptor().FullName()
	fields := md.Output().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if fd.Message() == nil || fd.Message().FullName() != want || fd.IsList() || !resp.Has(fd) {
			continue
		}
		b, err := proto.Marshal(resp.Get(fd).Message().Interface())
		if err != nil {
			return err
		}
		return proto.Unmarshal(b, out)
	}
	return fmt.Errorf("%s.%s answered no %s", component, getter, want)
}
