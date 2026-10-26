package apps_test

import (
	"strings"
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/docker-api/dockerapipb"
	"github.com/itsKontra/ddash/components/proxmox-api/proxmoxapipb"
	"github.com/itsKontra/ddash/core/runtime"
)

// Each App shows its Workload's network info, read by address from the
// real network over pfSense's baseline (VPN_BYPASS holds 198.18.1.102 and
// 198.18.1.208) and AdGuard's (198.18.1.102 has filtering off): a Docker
// App its host's, a guest's App the guest's, with the Device holding the
// address.
func TestAppsShowTheirWorkloadsNetworkInfo(t *testing.T) {
	l := newLab(t, labOptions{
		noProxyHosts: true,
		proxmox: &proxmoxFake{guests: []*proxmoxapipb.Workload{
			guest(proxmoxapipb.Kind_KIND_VM, 300, "desktop", "198.18.1.102"),
			guest(proxmoxapipb.Kind_KIND_LXC, 120, "paperless", "198.18.2.30"),
		}},
		docker: &dockerFake{hosts: []*dockerapipb.HostContainers{{
			Host: "media", Address: "198.18.1.208",
			Containers: []*dockerapipb.Container{container("jellyfin", "8096")},
		}}},
	})
	l.npm.AddProxyHost(proxyHost("http://198.18.1.208:8096", "jellyfin.home.example"))
	l.npm.AddProxyHost(proxyHost("http://198.18.1.102:8080", "tv.home.example"))
	l.npm.AddProxyHost(proxyHost("http://198.18.2.30:8000", "paperless.home.example"))
	l.npm.AddProxyHost(proxyHost("http://198.18.9.9:80", "nowhere.home.example"))
	l.register("jellyfin.home.example", "tv.home.example", "paperless.home.example", "nowhere.home.example")
	l.refresh()

	resp := l.list()
	if resp.GetNetworkError() != "" || resp.GetAdguardError() != "" {
		t.Errorf("network error %q, AdGuard error %q, want none", resp.GetNetworkError(), resp.GetAdguardError())
	}
	got := map[string]*appspb.NetworkInfo{}
	for _, a := range resp.GetApps() {
		got[a.GetUrl()] = a.GetNetwork()
	}
	want := map[string]*appspb.NetworkInfo{
		"https://jellyfin.home.example":  {Address: "198.18.1.208", InternetPath: appspb.InternetPath_INTERNET_PATH_DIRECT},
		"https://tv.home.example":        {Address: "198.18.1.102", InternetPath: appspb.InternetPath_INTERNET_PATH_DIRECT, AdsAllowed: true, Device: &appspb.NetworkDevice{Mac: "02:e6:29:fa:fc:39", Name: "laptop-2"}},
		"https://paperless.home.example": {Address: "198.18.2.30", InternetPath: appspb.InternetPath_INTERNET_PATH_TUNNELLED},
		"https://nowhere.home.example":   nil,
	}
	if d := diff(want, got); d != "" {
		t.Errorf("network info by URL (-want +got):\n%s", d)
	}
}

// While network can't answer, the Apps are listed without network info,
// with network's error, and apps stays healthy.
func TestAppsGoWithoutNetworkInfoWhileNetworkCantAnswer(t *testing.T) {
	l := newLab(t, labOptions{
		noProxyHosts:        true,
		networkUnconfigured: true,
		docker: &dockerFake{hosts: []*dockerapipb.HostContainers{{
			Host: "media", Address: "198.18.1.208",
			Containers: []*dockerapipb.Container{container("jellyfin", "8096")},
		}}},
	})
	l.npm.AddProxyHost(proxyHost("http://198.18.1.208:8096", "jellyfin.home.example"))
	l.register("jellyfin.home.example")
	l.refresh()

	resp := l.list()
	jellyfin := l.byURL("https://jellyfin.home.example")
	if jellyfin.GetWorkload() == nil || jellyfin.GetNetwork() != nil {
		t.Errorf("Jellyfin = %v, want its Workload without network info", jellyfin)
	}
	if !strings.Contains(resp.GetNetworkError(), "not configured") {
		t.Errorf("network error %q, want network's", resp.GetNetworkError())
	}
	if st := l.h.Status("apps"); st.State != runtime.OK {
		t.Errorf("apps is %v, want OK", st)
	}
}
