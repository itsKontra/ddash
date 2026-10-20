package core

import (
	"context"
	"strings"
	"unicode"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/router"
	configpb "github.com/itsKontra/ddash/proto/ddash/config"
	corepb "github.com/itsKontra/ddash/proto/ddash/core"
	"github.com/itsKontra/ddash/sdk"
)

// testAction is the config form's test button for a config message that
// names a (ddash.config.form).test_action, else nil.
func testAction(desc protoreflect.MessageDescriptor) *corepb.TestAction {
	form, _ := proto.GetExtension(desc.Options(), configpb.E_Form).(*configpb.Form)
	method := form.GetTestAction()
	if method == "" {
		return nil
	}
	return &corepb.TestAction{Method: method, Label: words(method)}
}

// words turns "SendTest" into "Send test".
func words(name string) string {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte(' ')
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// RunTestAction runs the Action owner's config message names as its
// test_action, with an empty request, for the actor in ctx. It passes the
// gate as a call to the Component's Action, so the Audit Log records that
// Action, on a chain of its own.
func (h *Host) RunTestAction(ctx context.Context, req *corepb.RunTestActionRequest) (*corepb.RunTestActionResponse, error) {
	owner := req.GetOwner()
	desc, _, err := h.configOf(ctx, owner)
	if err != nil {
		return nil, err
	}
	test := testAction(desc)
	if test == nil || owner == coreOwner {
		return nil, status.Errorf(codes.FailedPrecondition, "%s's config has no test_action", owner)
	}
	ep, err := h.runtime.Endpoint(owner)
	if err != nil {
		return nil, err
	}
	md := ep.Service.Methods().ByName(protoreflect.Name(test.GetMethod()))
	if md == nil || !appstate.IsAction(md) {
		return nil, status.Errorf(codes.FailedPrecondition, "%s's test_action %s is no Action of it", owner, test.GetMethod())
	}
	actor, ok := auth.ActorFrom(ctx)
	if !ok {
		actor = auth.Actor{Kind: auth.Core}
	}
	chain := router.NewChain()
	in := dynamicpb.NewMessage(md.Input())
	done, err := h.gate.Admit(ctx, actor, appstate.Action{Chain: chain, Target: owner, Method: md, Request: in})
	if err != nil {
		return nil, err
	}
	kv := []string{sdk.ActorMetadata, actor.String(), sdk.ChainMetadata, chain}
	if actor.Sub != "" {
		kv = append(kv, sdk.SubMetadata, actor.Sub)
	}
	callCtx, cancel := context.WithTimeout(metadata.AppendToOutgoingContext(ctx, kv...), router.ActionDeadline)
	defer cancel()
	err = ep.Conn.Invoke(callCtx, "/"+string(ep.Service.FullName())+"/"+string(md.Name()), in, dynamicpb.NewMessage(md.Output()))
	done(err)
	if err != nil {
		return nil, err
	}
	return &corepb.RunTestActionResponse{}, nil
}
