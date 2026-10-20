package core

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	configpb "github.com/itsKontra/ddash/proto/ddash/config"
)

// setupHint asks a started Component for the setup hint its config
// message names in (ddash.config.form).setup_from. It is nil when there is
// none, or when the Component isn't up or can't answer: the form works
// without it.
func (h *Host) setupHint(ctx context.Context, owner string, desc protoreflect.MessageDescriptor) *configpb.SetupHint {
	form, _ := proto.GetExtension(desc.Options(), configpb.E_Form).(*configpb.Form)
	if form.GetSetupFrom() == "" {
		return nil
	}
	if _, err := h.runtime.Endpoint(owner); err != nil {
		return nil
	}
	hint := &configpb.SetupHint{}
	if err := h.formGetter(ctx, owner, form.GetSetupFrom(), hint); err != nil {
		if errors.As(err, &errNotAGetter{}) {
			h.log.Warn("setup_from names no getter", "component", owner, "getter", form.GetSetupFrom())
		} else {
			h.log.Warn("setup hint", "component", owner, "err", err)
		}
		return nil
	}
	return hint
}
