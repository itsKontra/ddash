package core

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/itsKontra/ddash/core/config"
	"github.com/itsKontra/ddash/core/runtime"
	corepb "github.com/itsKontra/ddash/proto/ddash/core"
	"github.com/itsKontra/ddash/sdk"
)

// coreOwner is the config owner of the core's own settings, CoreSettings.
const coreOwner = "core"

// configOf returns owner's config message and why its stored config
// doesn't start. The owner is a wanted Component or coreOwner.
func (h *Host) configOf(ctx context.Context, owner string) (protoreflect.MessageDescriptor, sdk.FieldErrors, error) {
	if owner == coreOwner {
		desc := (&corepb.CoreSettings{}).ProtoReflect().Descriptor()
		cfg, err := h.config.Load(ctx, owner, desc)
		if err != nil {
			return nil, nil, err
		}
		return desc, config.Validate(cfg.ProtoReflect()), nil
	}
	desc, errs, err := h.runtime.Config(owner)
	if err != nil {
		return nil, nil, err
	}
	if desc == nil {
		return nil, nil, status.Errorf(codes.FailedPrecondition, "%s takes no config", owner)
	}
	return desc, errs, nil
}

// CoreSettings returns the core's own settings, defaults applied.
func (h *Host) CoreSettings(ctx context.Context) (*corepb.CoreSettings, error) {
	settings := &corepb.CoreSettings{}
	msg, err := h.config.Load(ctx, coreOwner, settings.ProtoReflect().Descriptor())
	if err != nil {
		return nil, err
	}
	b, err := proto.Marshal(msg)
	if err != nil {
		return nil, err
	}
	return settings, proto.Unmarshal(b, settings)
}

// ConfigForm returns owner's config form.
func (h *Host) ConfigForm(ctx context.Context, owner string) (*corepb.GetConfigFormResponse, error) {
	desc, errs, err := h.configOf(ctx, owner)
	if err != nil {
		return nil, err
	}
	form, err := h.config.Form(ctx, owner, desc)
	if err != nil {
		return nil, err
	}
	form.Errors = configErrors(errs)
	if owner != coreOwner {
		form.SetupHint = h.setupHint(ctx, owner, desc)
		form.TestAction = testAction(desc)
	}
	return form, nil
}

// SaveConfig is the core Action that saves owner's config from its form:
// it validates the config, merges the secrets the form leaves unchanged,
// restarts the Component with it and stores it only when Start accepts
// it. The Audit Log records the changed fields. It runs for the actor in
// ctx.
func (h *Host) SaveConfig(ctx context.Context, req *corepb.SaveConfigRequest) (*corepb.SaveConfigResponse, error) {
	owner := req.GetOwner()
	desc, _, err := h.configOf(ctx, owner)
	var edit *config.Edit
	if err == nil {
		edit, err = h.config.Edit(ctx, owner, desc, config.Input{Values: req.GetValues(), ClearSecrets: req.GetClearSecrets()})
	}
	record := &corepb.ConfigSave{Owner: owner}
	if edit != nil {
		record.Changes = edit.Changes
	}
	resp := &corepb.SaveConfigResponse{}
	err = h.coreAction(ctx, "SaveConfig", record, func() error {
		if err != nil {
			return err
		}
		if owner == coreOwner {
			return h.config.Save(ctx, edit, func(context.Context, proto.Message) error { return nil })
		}
		if err := h.config.Save(ctx, edit, func(ctx context.Context, cfg proto.Message) error {
			return h.runtime.Reconfigure(ctx, owner, cfg)
		}); err != nil {
			return err
		}
		resp.Status, err = h.componentStatus(ctx, owner, h.runtime.Status()[owner])
		return err
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// componentStatus is how the core API shows a Component's status.
func (h *Host) componentStatus(ctx context.Context, name string, st runtime.Status) (*corepb.ComponentStatus, error) {
	cs := &corepb.ComponentStatus{
		Name: name, State: string(st.State), Message: st.Message, Held: st.Held, Configurable: st.Configurable,
	}
	if st.State == runtime.Unconfigured {
		saved, err := h.config.Saved(ctx, name)
		if err != nil {
			return nil, err
		}
		cs.NeedsConfig = saved
	}
	return cs, nil
}

func configErrors(errs sdk.FieldErrors) []*corepb.ConfigError {
	var out []*corepb.ConfigError
	for _, e := range errs {
		out = append(out, &corepb.ConfigError{Path: e.Path, Message: e.Message})
	}
	return out
}
