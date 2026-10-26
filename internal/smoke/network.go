package smoke

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
	"github.com/itsKontra/ddash/components/checks/checkspb"
	"github.com/itsKontra/ddash/components/network"
	"github.com/itsKontra/ddash/components/network/networkpb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// network reaches no system itself: it reads pfSense and AdGuard through
// pfsense-api and adguard-api. Smoke runs it over those two, started on
// their smoke.env configs, with its own config under SMOKE_NETWORK (the
// aliases and range it names), and reads network info by address for
// every Device, read-only.
const networkName = "network"

// noChecks takes the VPN Tunnel item network reports to checks, which
// smoke doesn't run.
type noChecks struct{ checkspb.ChecksServiceClient }

func (noChecks) ReportState(context.Context, *checkspb.ReportStateRequest, ...grpc.CallOption) (*checkspb.ReportStateResponse, error) {
	return &checkspb.ReportStateResponse{}, nil
}

// runNetwork runs network over pfsense-api and adguard-api and checks
// GetNetworkInfo for every Device's address.
func runNetwork(ctx context.Context, opts Options) []error {
	fail := func(format string, args ...any) []error {
		err := fmt.Errorf(networkName+" "+format, args...)
		fmt.Fprintf(opts.Log, "FAIL %s\n", err)
		return []error{err}
	}
	self := system{def: network.Definition(), config: func() proto.Message { return &networkpb.NetworkConfig{} }}
	cfg, err := configOf(self, opts.Env)
	if err != nil {
		return fail("%v", err)
	}
	var clients []any
	for _, name := range []string{"pfsense-api", "adguard-api"} {
		s := systemNamed(name)
		depCfg, err := configOf(s, opts.Env)
		if err != nil {
			return fail("needs %s, which %v", name, err)
		}
		conn, stop, err := launch(ctx, s.def, depCfg, filepath.Join(opts.DataDir, name+".db"), opts.Log)
		if err != nil {
			return fail("needs %s: %v", name, err)
		}
		defer stop()
		switch name {
		case "pfsense-api":
			clients = append(clients, pfsenseapipb.NewPfsenseApiServiceClient(conn))
		case "adguard-api":
			clients = append(clients, adguardapipb.NewAdguardApiServiceClient(conn))
		}
	}
	conn, stop, err := launch(ctx, self.def, cfg, filepath.Join(opts.DataDir, networkName+".db"), opts.Log, append(clients, noChecks{})...)
	if err != nil {
		return fail("%v", err)
	}
	defer stop()
	c := networkpb.NewNetworkServiceClient(conn)

	callCtx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	devices, err := c.ListDevices(callCtx, &networkpb.ListDevicesRequest{})
	if err != nil {
		return fail("ListDevices: %v", err)
	}
	var addresses []string
	byAddress := map[string]string{}
	for _, d := range devices.GetDevices() {
		if d.GetIp() != "" {
			addresses = append(addresses, d.GetIp())
			byAddress[d.GetIp()] = d.GetMac()
		}
	}
	if len(addresses) == 0 {
		return fail("GetNetworkInfo: ListDevices listed no Device with an address to read")
	}
	info, err := c.GetNetworkInfo(callCtx, &networkpb.GetNetworkInfoRequest{Addresses: addresses})
	if err == nil {
		err = checkNetworkInfo(info, addresses, byAddress)
	}
	if err != nil {
		return fail("GetNetworkInfo: %v", err)
	}
	fmt.Fprintf(opts.Log, "ok %s GetNetworkInfo\n", networkName)
	return nil
}

// checkNetworkInfo holds GetNetworkInfo's answer for addresses to its
// invariants: one info per address, in order, each with an internet
// path, naming the Device holding it.
func checkNetworkInfo(r *networkpb.GetNetworkInfoResponse, addresses []string, macs map[string]string) error {
	if r.GetAdguardError() != "" {
		return fmt.Errorf("AdGuard: %s", r.GetAdguardError())
	}
	if len(r.GetInfos()) != len(addresses) {
		return fmt.Errorf("%d infos for %d addresses", len(r.GetInfos()), len(addresses))
	}
	var errs []error
	for i, info := range r.GetInfos() {
		a := addresses[i]
		switch {
		case info.GetAddress() != a:
			errs = append(errs, fmt.Errorf("info %d is for %s, want %s", i, info.GetAddress(), a))
		case info.GetInternetPath() == networkpb.InternetPath_INTERNET_PATH_UNSPECIFIED:
			errs = append(errs, fmt.Errorf("%s has no internet path", a))
		case info.GetDevice().GetMac() != macs[a]:
			errs = append(errs, fmt.Errorf("%s names Device %q, want %q", a, info.GetDevice().GetMac(), macs[a]))
		}
	}
	return errors.Join(errs...)
}
