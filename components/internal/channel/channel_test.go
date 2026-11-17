package channel_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/internal/channel"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
	"github.com/itsKontra/ddash/components/telegram-api/telegramapipb"
	configpb "github.com/itsKontra/ddash/proto/ddash/config"
	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"
)

// The channels' configs, whose filters must mean the same.
var configs = []proto.Message{&ntfyapipb.NtfyApiConfig{}, &telegramapipb.TelegramApiConfig{}}

func TestAnUnsetFilterTakesEveryCategory(t *testing.T) {
	for _, cfg := range configs {
		want := []notificationpb.Category{
			notificationpb.Category_CATEGORY_CHECKS,
			notificationpb.Category_CATEGORY_IMAGE_UPDATES,
			notificationpb.Category_CATEGORY_ACTION_OUTCOMES,
		}
		if diff := cmp.Diff(want, channel.Takes(cfg)); diff != "" {
			t.Errorf("%T takes (-want +got):\n%s", cfg, diff)
		}
	}
}

func TestASwitchedOffCategoryIsNotTaken(t *testing.T) {
	off, on := false, true
	got := channel.Takes(&telegramapipb.TelegramApiConfig{Checks: &on, ImageUpdates: &off})
	want := []notificationpb.Category{notificationpb.Category_CATEGORY_CHECKS, notificationpb.Category_CATEGORY_ACTION_OUTCOMES}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("takes (-want +got):\n%s", diff)
	}
}

// Each channel's config declares the filter's fields itself, so they are
// held to one shape: the same numbers, labels, help and default on, and
// so the same form, in every channel.
func TestEveryChannelsFilterHasTheSameFields(t *testing.T) {
	type field struct {
		Number  protoreflect.FieldNumber
		Options *configpb.Field
	}
	shape := func(cfg proto.Message) []field {
		var out []field
		for _, f := range channel.Filter {
			fd := cfg.ProtoReflect().Descriptor().Fields().ByName(f.Field)
			if fd == nil {
				t.Fatalf("%T has no %s", cfg, f.Field)
			}
			opts, _ := proto.GetExtension(fd.Options(), configpb.E_Field).(*configpb.Field)
			out = append(out, field{fd.Number(), opts})
		}
		return out
	}
	want := shape(configs[0])
	for _, f := range want {
		if f.Options.GetDefault() != "true" {
			t.Errorf("filter field %d defaults to %q, want on", f.Number, f.Options.GetDefault())
		}
	}
	for _, cfg := range configs[1:] {
		if diff := cmp.Diff(want, shape(cfg), protocmp.Transform()); diff != "" {
			t.Errorf("%T's filter differs from %T's (-want +got):\n%s", cfg, configs[0], diff)
		}
	}
}
