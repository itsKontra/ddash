package fakelab_test

import (
	"testing"

	energyapi "github.com/itsKontra/ddash/components/energy-api"
	"github.com/itsKontra/ddash/components/energy-api/energyapipb"
	"github.com/itsKontra/ddash/internal/fakelab"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// The lab's house is simulated up to the clock, so `make dev` always
// shows a live flow and today's curve.
func TestTheLabsHouseHasALiveFlowAndToday(t *testing.T) {
	lab, err := fakelab.Start(repoRoot, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lab.Close)
	h := sdktest.New(t, energyapi.Definition())
	h.MustStart(lab.Config()["energy-api"])
	c := energyapipb.NewEnergyApiServiceClient(h.Conn)

	flow, err := c.GetFlow(t.Context(), &energyapipb.GetFlowRequest{})
	if err != nil {
		t.Fatal(err)
	}
	today, err := c.GetPeriod(t.Context(), &energyapipb.GetPeriodRequest{Span: energyapipb.Span_SPAN_DAY})
	if err != nil {
		t.Fatal(err)
	}

	if flow.GetAt() == nil || flow.GetPower().Consumption == nil || flow.GetBattery().SocPct == nil {
		t.Errorf("flow = %v, want a reading with consumption and state of charge", flow)
	}
	if len(today.GetSamples()) == 0 || today.GetPrices() == nil {
		t.Errorf("today: %d samples, prices %v; want a curve and prices", len(today.GetSamples()), today.GetPrices())
	}
}
