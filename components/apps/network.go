package apps

import (
	"context"
	"net/netip"
	"strconv"

	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/network/networkpb"
	workloadpb "github.com/itsKontra/ddash/proto/ddash/workload"
)

// networkAddress is the address a Workload's network info is read at:
// its Docker host's for a container, a guest's first IPv4 address (else
// its first), an external host's; empty when that is no IP address, or
// no source lists the Workload now.
func (s sources) networkAddress(w *appspb.Workload) string {
	ref := w.GetRef()
	var candidates []string
	switch ref.GetKind() {
	case workloadpb.Kind_KIND_DOCKER:
		for _, h := range s.docker {
			if h.GetHost() == ref.GetHost() {
				candidates = []string{h.GetAddress()}
			}
		}
	case workloadpb.Kind_KIND_LXC, workloadpb.Kind_KIND_VM:
		for _, g := range s.guests {
			if kindOf(g) == ref.GetKind() && strconv.FormatUint(uint64(g.GetVmid()), 10) == ref.GetId() {
				for _, nic := range g.GetNics() {
					candidates = append(candidates, nic.GetIps()...)
				}
			}
		}
	case workloadpb.Kind_KIND_EXTERNAL:
		candidates = []string{ref.GetHost()}
	}
	var first string
	for _, c := range candidates {
		a, err := netip.ParseAddr(canonHost(c))
		if err != nil {
			continue
		}
		if a.Is4() {
			return a.String()
		}
		if first == "" {
			first = a.String()
		}
	}
	return first
}

var paths = map[networkpb.InternetPath]appspb.InternetPath{
	networkpb.InternetPath_INTERNET_PATH_TUNNELLED:   appspb.InternetPath_INTERNET_PATH_TUNNELLED,
	networkpb.InternetPath_INTERNET_PATH_DIRECT:      appspb.InternetPath_INTERNET_PATH_DIRECT,
	networkpb.InternetPath_INTERNET_PATH_NO_INTERNET: appspb.InternetPath_INTERNET_PATH_NO_INTERNET,
}

// fillNetwork gives each App with a Workload at an IP address its network
// info, in one read of network. It returns why network couldn't answer,
// and why AdGuard couldn't be read; the Apps then go without.
func (c *component) fillNetwork(ctx context.Context, apps []*appspb.App, src sources) (networkErr, adguardErr string) {
	at := map[*appspb.App]string{}
	var addresses []string
	seen := map[string]bool{}
	for _, a := range apps {
		if a.GetWorkload() == nil {
			continue
		}
		addr := src.networkAddress(a.GetWorkload())
		if addr == "" {
			continue
		}
		at[a] = addr
		if !seen[addr] {
			seen[addr] = true
			addresses = append(addresses, addr)
		}
	}
	if len(addresses) == 0 {
		return "", ""
	}
	resp, err := c.network.GetNetworkInfo(ctx, &networkpb.GetNetworkInfoRequest{Addresses: addresses})
	if err != nil {
		return status.Convert(err).Message(), ""
	}
	byAddr := map[string]*appspb.NetworkInfo{}
	for _, info := range resp.GetInfos() {
		out := &appspb.NetworkInfo{
			Address:      info.GetAddress(),
			InternetPath: paths[info.GetInternetPath()],
			AdsAllowed:   info.GetAdsAllowed(),
		}
		if d := info.GetDevice(); d != nil {
			out.Device = &appspb.NetworkDevice{Mac: d.GetMac(), Name: d.GetName()}
		}
		byAddr[info.GetAddress()] = out
	}
	for a, addr := range at {
		a.Network = byAddr[addr]
	}
	return "", resp.GetAdguardError()
}
