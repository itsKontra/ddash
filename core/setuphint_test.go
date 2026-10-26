package core_test

import (
	"testing"

	"github.com/itsKontra/ddash/core/coretest"
	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/core/runtime"
	configpb "github.com/itsKontra/ddash/proto/ddash/config"
	"github.com/itsKontra/ddash/sdk"
)

func TestTheConfigFormShowsTheSetupHintOnceTheComponentStarted(t *testing.T) {
	hinting := greeter(sample.Hooks{SetupHint: func(cfg *samplepb.SampleConfig) *configpb.SetupHint {
		return &configpb.SetupHint{Text: "Add this line to " + cfg.GetTarget() + "'s guest list.", Block: "greeter@ddash"}
	}})
	h := coretest.New(t, coretest.Options{Components: []sdk.Definition{hinting}, Plugins: []coretest.Plugin{hello}})
	h.Install("hello")
	h.WaitState("greeter", runtime.Unconfigured)

	if form := coreJSON(t, h, "GetConfigForm", `{"owner":"greeter"}`); form["setupHint"] != nil {
		t.Errorf("setup hint %v before greeter started, want none", form["setupHint"])
	}

	coreJSON(t, h, "SaveConfig", `{"owner":"greeter","values":{"target":"world"}}`)
	h.WaitState("greeter", runtime.OK)

	hint, _ := coreJSON(t, h, "GetConfigForm", `{"owner":"greeter"}`)["setupHint"].(map[string]any)
	if hint["text"] != "Add this line to world's guest list." || hint["block"] != "greeter@ddash" {
		t.Errorf("setup hint = %v, want the hook's for the saved config", hint)
	}
}
