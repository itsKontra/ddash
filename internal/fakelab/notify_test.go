package fakelab_test

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/itsKontra/ddash/components/checks"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	ntfyapi "github.com/itsKontra/ddash/components/ntfy-api"
	telegramapi "github.com/itsKontra/ddash/components/telegram-api"
	"github.com/itsKontra/ddash/components/telegram-api/telegramfake"
	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/runtime"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/sdk"
)

// bootNotifyLab boots the core with the Checks Plugin and both channel
// Plugins in-process against the lab, configured as `make dev` does.
func bootNotifyLab(t *testing.T) (*coretest.Harness, *fakelab.Lab) {
	lab, err := fakelab.Start(repoRoot, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lab.Close)
	h := coretest.New(t, coretest.Options{
		Components: []sdk.Definition{checks.Definition(), ntfyapi.Definition(), telegramapi.Definition()},
		Plugins: []coretest.Plugin{
			{Name: "checks", Requires: []string{"checks"}},
			{Name: "ntfy", Requires: []string{"ntfy-api"}, Headless: true},
			{Name: "telegram", Requires: []string{"telegram-api"}, Headless: true},
		},
	})
	for component, cfg := range lab.Config() {
		if component == "ntfy-api" || component == "telegram-api" {
			h.SeedConfig(component, cfg)
		}
	}
	for _, p := range []string{"checks", "ntfy", "telegram"} {
		h.Install(p)
	}
	for _, c := range []string{"ntfy-api", "telegram-api", "checks"} {
		h.WaitState(c, runtime.OK)
	}
	return h, lab
}

// In `make dev` both channels are installed: the Checks Page lists them
// with their destinations, and each config form's Send test reaches its
// fake, ntfy's fixtures and Telegram's bot, which keeps what reached the
// chat. A sender's Notification through them is proxmox's, in
// TestAnUpdateRunsInTheBackgroundInItsActionsChain.
func TestTheLabsChannelsAreListedAndTheirTestReachesThem(t *testing.T) {
	h, lab := bootNotifyLab(t)

	res := h.Call("checks", "checks", "ListChannels", `{}`)
	var channels checkspb.ListChannelsResponse
	if err := protojson.Unmarshal(res.Body.Bytes(), &channels); err != nil {
		t.Fatalf("ListChannels: %d %s: %v", res.Code, res.Body, err)
	}
	var dests []string
	for _, ch := range channels.GetChannels() {
		dests = append(dests, ch.GetTitle()+" "+ch.GetDestination()+" "+ch.GetProblem())
	}
	want := []string{"ntfy " + lab.Addrs["ntfy-api"] + "/ddash-alerts ", "Telegram chat " + telegramfake.DefaultChat + " "}
	if diff := cmp.Diff(want, dests); diff != "" {
		t.Errorf("channels (-want +got):\n%s", diff)
	}

	for _, owner := range []string{"ntfy-api", "telegram-api"} {
		if res := h.CoreCall("RunTestAction", `{"owner":"`+owner+`"}`); res.Code != http.StatusOK {
			t.Errorf("%s's Send test: %d %s", owner, res.Code, res.Body)
		}
	}
	msgs := lab.Handler("telegram-api").(*telegramfake.Bot).Messages()
	if len(msgs) != 1 || msgs[0].Plain != "✅ ddash test\nNotifications from ddash reach this chat." {
		t.Errorf("Telegram got %+v, want the test message", msgs)
	}
}
