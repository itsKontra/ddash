package core

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	configpb "github.com/itsKontra/ddash/proto/ddash/config"
	corepb "github.com/itsKontra/ddash/proto/ddash/core"
)

// fillChoices gives each form field whose config field names a getter in
// (ddash.config.field).choices_from the choices that getter offers, or a
// note saying why it is free text for now.
func (h *Host) fillChoices(ctx context.Context, desc protoreflect.MessageDescriptor, fields []*corepb.FormField) {
	for _, ff := range fields {
		fd := desc.Fields().ByName(protoreflect.Name(ff.GetName()))
		if fd == nil {
			continue
		}
		opts, _ := proto.GetExtension(fd.Options(), configpb.E_Field).(*configpb.Field)
		from := opts.GetChoicesFrom()
		if from == "" {
			continue
		}
		component, getter, _ := strings.Cut(from, ".")
		choices := &configpb.Choices{}
		if err := h.formGetter(ctx, component, getter, choices); err != nil {
			h.log.Warn("choices_from", "field", fd.FullName(), "from", from, "err", err)
			ff.ChoicesNote = fmt.Sprintf("%s can't offer choices (%s); type the value.", component, status.Convert(err).Message())
			continue
		}
		for _, c := range choices.GetChoices() {
			label := c.GetLabel()
			if label == "" {
				label = c.GetValue()
			}
			ff.Choices = append(ff.Choices, &corepb.EnumOption{Value: c.GetValue(), Label: label})
		}
	}
}
