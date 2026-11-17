// Package channel is what every channel Component (one serving
// ddash.notification.ChannelService, such as ntfy-api and telegram-api)
// shares: what the filter in its config means. A config form can't hold a
// nested message, so each channel's config declares the filter's three
// fields itself, and this package reads them by name.
package channel

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"
)

// Filter is the filter's fields, each a proto3 optional bool that is on
// unless switched off, with the category it lets through, in Category
// order.
var Filter = []struct {
	Field    protoreflect.Name
	Category notificationpb.Category
}{
	{"checks", notificationpb.Category_CATEGORY_CHECKS},
	{"image_updates", notificationpb.Category_CATEGORY_IMAGE_UPDATES},
	{"action_outcomes", notificationpb.Category_CATEGORY_ACTION_OUTCOMES},
}

// Takes returns the categories the filter in cfg, a channel's config,
// takes: each whose field is on or unset. It panics when cfg lacks one of
// the fields, a mistake any test of the Component catches.
func Takes(cfg proto.Message) []notificationpb.Category {
	m := cfg.ProtoReflect()
	var takes []notificationpb.Category
	for _, f := range Filter {
		fd := m.Descriptor().Fields().ByName(f.Field)
		if fd == nil || fd.Kind() != protoreflect.BoolKind || !fd.HasPresence() {
			panic(fmt.Sprintf("channel: %s has no optional bool %s", m.Descriptor().FullName(), f.Field))
		}
		if !m.Has(fd) || m.Get(fd).Bool() {
			takes = append(takes, f.Category)
		}
	}
	return takes
}
