package fakelab_test

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/notify"
	"github.com/itsKontra/ddash/components/notify/notifypb"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	telegramapi "github.com/itsKontra/ddash/components/telegram-api"
	"github.com/itsKontra/ddash/components/telegram-api/telegramfake"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/sdk"
)

// bootNotifyLab boots the core with checks, notify and both channels
// in-process against the lab, configured as `make dev` does.
func bootNotifyLab(t *testing.T) (*coretest.Harness, *fakelab.Lab) {
	lab, err := fakelab.Start(repoRoot, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lab.Close)
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{checks.Definition(), notify.Definition(), ntfyapi.Definition(), telegramapi.Definition()},
		Plugins:    []coretest.Plugin{{Name: "checks", Requires: []string{"checks", "notify"}}},
	})
	for component, cfg := range lab.Config() {
		if component == "ntfy-api" || component == "telegram-api" {
			h.SeedConfig(component, cfg)
		}
	}
	h.Install("checks")
	for _, c := range []string{"ntfy-api", "telegram-api", "notify", "checks"} {
		h.WaitState(c, runtime.OK)
	}
	return h, lab
}

// In `make dev` a Notification reaches both channels: ntfy's fixtures
// and Telegram's fake, which keeps what reached the chat.
func TestTheLabsNotificationsReachNtfyAndTelegram(t *testing.T) {
	h, lab := bootNotifyLab(t)

	res := h.Call("checks", "notify", "Send", `{"notification":{"title":"NAS down","message":"connection refused","priority":4,"tags":["red_circle"],"category":"CATEGORY_CHECKS"}}`)
	if res.Code != http.StatusOK {
		t.Fatalf("Send: status %d: %s", res.Code, res.Body)
	}

	var sent notifypb.SendResponse
	if err := protojson.Unmarshal(res.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	want := []*notifypb.Delivery{
		{Channel: "ntfy", State: notifypb.DeliveryState_DELIVERY_STATE_SENT},
		{Channel: "telegram", State: notifypb.DeliveryState_DELIVERY_STATE_SENT},
	}
	if diff := cmp.Diff(want, sent.GetDeliveries(), protocmp.Transform()); diff != "" {
		t.Errorf("deliveries (-want +got):\n%s", diff)
	}
	msgs := lab.Handler("telegram-api").(*telegramfake.Bot).Messages()
	if len(msgs) != 1 || msgs[0].Plain != "\U0001F534 NAS down\nconnection refused" {
		t.Errorf("Telegram got %+v, want the Notification", msgs)
	}

	res = h.Call("checks", "notify", "ListChannels", `{}`)
	var channels notifypb.ListChannelsResponse
	if err := protojson.Unmarshal(res.Body.Bytes(), &channels); err != nil {
		t.Fatal(err)
	}
	var dests []string
	for _, ch := range channels.GetChannels() {
		if ch.GetSetUp() {
			dests = append(dests, ch.GetDestination())
		}
	}
	if diff := cmp.Diff([]string{lab.Addrs["ntfy-api"] + "/ddash-alerts", "chat " + telegramfake.DefaultChat}, dests); diff != "" {
		t.Errorf("set-up channels (-want +got):\n%s", diff)
	}
}
