package apps_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
)

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

// failRead makes nginx-config's next snapshot one whose `nginx -T` read
// failed with stderr, keeping the last good Proxy Hosts.
func (n *nginx) failRead(stderr string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.snap = &nginxconfigpb.Snapshot{Revision: n.snap.GetRevision() + 1, Servers: n.snap.GetServers(), Error: stderr}
}

func nginxItem(state checkspb.State, reason string) *checkspb.ReportStateRequest {
	return &checkspb.ReportStateRequest{Item: "nginx-config", Name: "nginx config", State: state, Reason: reason, Path: "/reverse-proxy"}
}

const testFailed = "nginx: [emerg] unknown directive \"proxy_passs\" in /etc/nginx/conf.d/jellyfin.conf:7\n" +
	"nginx: configuration file /etc/nginx/nginx.conf test failed"

func TestAppsReportsTheNginxConfigItemAfterStartAndOnChange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := newNginx(jellyfin)
		s := start(t, n)
		synctest.Wait()
		up := nginxItem(checkspb.State_STATE_UP, "")
		if d := diff([]*checkspb.ReportStateRequest{up}, s.checks.reported()); d != "" {
			t.Fatalf("after Start (-want +got):\n%s", d)
		}

		afterPoll()
		n.set(jellyfin)
		afterPoll()
		if got := len(s.checks.reported()); got != 1 {
			t.Fatalf("%d reports without a change, want only the one after Start", got)
		}

		n.failRead(testFailed)
		afterPoll()
		afterPoll()
		down := nginxItem(checkspb.State_STATE_DOWN, `nginx: [emerg] unknown directive "proxy_passs" in /etc/nginx/conf.d/jellyfin.conf:7`)
		if d := diff([]*checkspb.ReportStateRequest{up, down}, s.checks.reported()); d != "" {
			t.Fatalf("after the read failed (-want +got):\n%s", d)
		}

		n.set(jellyfin)
		afterPoll()
		if d := diff([]*checkspb.ReportStateRequest{up, down, up}, s.checks.reported()); d != "" {
			t.Errorf("after it recovered (-want +got):\n%s", d)
		}
	})
}

func TestAppsReportsNothingWhileNginxConfigIsUnavailable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := newNginx(jellyfin)
		n.fail(status.Error(codes.Unavailable, "nginx-config is down"))
		s := start(t, n)
		synctest.Wait()
		afterPoll()
		if got := s.checks.reported(); len(got) != 0 {
			t.Errorf("reported %v, want nothing: Component health isn't notified", got)
		}
	})
}

func TestARefusedReportIsTriedAgainOnTheNextPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &checksFake{}
		f.refuseReports(errors.New("checks unavailable"))
		n := newNginx(jellyfin)
		s := startWith(t, n, f)
		synctest.Wait()

		f.refuseReports(nil)
		afterPoll()
		if d := diff([]*checkspb.ReportStateRequest{nginxItem(checkspb.State_STATE_UP, "")}, s.checks.reported()); d != "" {
			t.Errorf("(-want +got):\n%s", d)
		}
	})
}
