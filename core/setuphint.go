package core

import (
	"context"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/router"
	configpb "github.com/itsKontra/ddash/proto/ddash/config"
	"github.com/itsKontra/ddash/sdk"
)

var setupHintName = (&configpb.SetupHint{}).ProtoReflect().Descriptor().FullName()

// setupHint asks a started Component for the setup hint its config
// message names in (ddash.config.form).setup_from. It is nil when there is
// none, or when the Component isn't up or can't answer: the form works
// without it.
func (h *Host) setupHint(ctx context.Context, owner string, desc protoreflect.MessageDescriptor) *configpb.SetupHint {
	form, _ := proto.GetExtension(desc.Options(), configpb.E_Form).(*configpb.Form)
	if form.GetSetupFrom() == "" {
		return nil
	}
	ep, err := h.runtime.Endpoint(owner)
	if err != nil {
		return nil
	}
	md := ep.Service.Methods().ByName(protoreflect.Name(form.GetSetupFrom()))
	if md == nil || appstate.IsAction(md) {
		h.log.Warn("setup_from names no getter", "component", owner, "getter", form.GetSetupFrom())
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, router.GetterDeadline)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx,
		sdk.ActorMetadata, auth.Actor{Kind: auth.Core}.String(), sdk.ChainMetadata, router.NewChain())
	resp := dynamicpb.NewMessage(md.Output())
	method := "/" + string(ep.Service.FullName()) + "/" + string(md.Name())
	if err := ep.Conn.Invoke(ctx, method, dynamicpb.NewMessage(md.Input()), resp); err != nil {
		h.log.Warn("setup hint", "component", owner, "err", err)
		return nil
	}
	fields := md.Output().Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		if fd.Message() == nil || fd.Message().FullName() != setupHintName || fd.IsList() || !resp.Has(fd) {
			continue
		}
		hint := &configpb.SetupHint{}
		b, err := proto.Marshal(resp.Get(fd).Message().Interface())
		if err == nil {
			err = proto.Unmarshal(b, hint)
		}
		if err != nil {
			h.log.Warn("setup hint", "component", owner, "err", err)
			return nil
		}
		return hint
	}
	return nil
}
