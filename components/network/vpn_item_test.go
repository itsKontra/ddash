package network_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/network"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

// checksFake fakes checks' generated client: it records every report it
// accepts, and refuses them all while refusing is set.
type checksFake struct {
	checkspb.ChecksServiceClient

	mu        sync.Mutex
	reports   []*checkspb.ReportStateRequest
	reportErr error
}

func (f *checksFake) ReportState(_ context.Context, req *checkspb.ReportStateRequest, _ ...grpc.CallOption) (*checkspb.ReportStateResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reportErr != nil {
		return nil, f.reportErr
	}
	f.reports = append(f.reports, req)
	return &checkspb.ReportStateResponse{}, nil
}

// reported is every report checks accepted.
func (f *checksFake) reported() []*checkspb.ReportStateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*checkspb.ReportStateRequest(nil), f.reports...)
}

func (f *checksFake) refuseReports(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reportErr = err
}

// startReporting starts network on fw, reporting to ch.
func startReporting(t *testing.T, fw *firewall, ch *checksFake) {
	h := sdktest.New(t, network.Definition(),
		sdktest.WithClient[pfsenseapipb.PfsenseApiServiceClient](fw),
		sdktest.WithClient[adguardapipb.AdguardApiServiceClient](&adguard{}),
		sdktest.WithClient[checkspb.ChecksServiceClient](ch))
	h.MustStart(homeConfig())
}

// afterPoll lets the next read of the group happen: a minute plus at most
// 10% jitter.
func afterPoll() {
	time.Sleep(network.PollInterval + network.PollInterval/10 + time.Second)
	synctest.Wait()
}

func vpnUp() *checkspb.ReportStateRequest {
	return &checkspb.ReportStateRequest{Item: "vpn-tunnel", Name: "VPN Tunnel", State: checkspb.State_STATE_UP, Path: "/network"}
}

func vpnDown(reason string) *checkspb.ReportStateRequest {
	return &checkspb.ReportStateRequest{Item: "vpn-tunnel", Name: "VPN Tunnel", State: checkspb.State_STATE_DOWN, Reason: reason, Path: "/network"}
}

func reportDiff(want []*checkspb.ReportStateRequest, ch *checksFake) string {
	return cmp.Diff(want, ch.reported(), protocmp.Transform())
}

func TestNetworkReportsTheVPNTunnelItemAfterStartAndOnChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}}
		fw.set(online("WG_VPN", 24), online("OVPN_VPN", 33))
		ch := &checksFake{}
		startReporting(t, fw, ch)
		synctest.Wait()
		if d := reportDiff([]*checkspb.ReportStateRequest{vpnUp()}, ch); d != "" {
			t.Fatalf("after Start (-want +got):\n%s", d)
		}

		afterPoll()
		if got := len(ch.reported()); got != 1 {
			t.Fatalf("%d reports without a change, want only the one after Start", got)
		}

		fw.set(down("WG_VPN"), down("OVPN_VPN"))
		afterPoll()
		afterPoll()
		want := []*checkspb.ReportStateRequest{vpnUp(), vpnDown("WG_VPN down, OVPN_VPN down")}
		if d := reportDiff(want, ch); d != "" {
			t.Fatalf("after both tiers went down (-want +got):\n%s", d)
		}

		fw.set(online("WG_VPN", 24), online("OVPN_VPN", 33))
		afterPoll()
		want = append(want, vpnUp())
		if d := reportDiff(want, ch); d != "" {
			t.Errorf("after it recovered (-want +got):\n%s", d)
		}
	})
}

func TestFailoverToTheOpenVPNFallbackReportsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}}
		fw.set(online("WG_VPN", 24), online("OVPN_VPN", 33))
		ch := &checksFake{}
		startReporting(t, fw, ch)
		synctest.Wait()

		fw.set(down("WG_VPN"), online("OVPN_VPN", 33))
		afterPoll()
		fw.set(online("WG_VPN", 24), online("OVPN_VPN", 33))
		afterPoll()
		if d := reportDiff([]*checkspb.ReportStateRequest{vpnUp()}, ch); d != "" {
			t.Errorf("failing over and back (-want +got):\n%s", d)
		}
	})
}

func TestTheDownReasonSaysWhyEachTierIsOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}}
		lossy := online("OVPN_VPN", 40)
		lossy.Substatus = "highloss"
		fw.set(lossy) // WG_VPN isn't reported at all
		ch := &checksFake{}
		startReporting(t, fw, ch)
		synctest.Wait()

		want := []*checkspb.ReportStateRequest{vpnDown("WG_VPN not reported by pfSense, OVPN_VPN losing packets")}
		if d := reportDiff(want, ch); d != "" {
			t.Errorf("(-want +got):\n%s", d)
		}
	})
}

func TestNetworkReportsNothingWhileTheGroupCannotBeRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}, err: status.Error(codes.Unavailable, "pfSense unreachable")}
		fw.set(down("WG_VPN"), down("OVPN_VPN"))
		ch := &checksFake{}
		startReporting(t, fw, ch)
		synctest.Wait()
		if got := ch.reported(); len(got) != 0 {
			t.Fatalf("reported %v, want nothing: network's health isn't notified", got)
		}

		fw.fail(nil)
		afterPoll()
		fw.fail(status.Error(codes.Unavailable, "pfSense unreachable"))
		afterPoll()
		if d := reportDiff([]*checkspb.ReportStateRequest{vpnDown("WG_VPN down, OVPN_VPN down")}, ch); d != "" {
			t.Errorf("a failed read after a good one (-want +got):\n%s", d)
		}
	})
}

func TestARefusedReportIsTriedAgainOnTheNextPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fw := &firewall{groups: []*pfsenseapipb.GatewayGroup{failover()}}
		fw.set(online("WG_VPN", 24), online("OVPN_VPN", 33))
		ch := &checksFake{}
		ch.refuseReports(errors.New("checks unavailable"))
		startReporting(t, fw, ch)
		synctest.Wait()

		ch.refuseReports(nil)
		afterPoll()
		if d := reportDiff([]*checkspb.ReportStateRequest{vpnUp()}, ch); d != "" {
			t.Errorf("(-want +got):\n%s", d)
		}
	})
}
