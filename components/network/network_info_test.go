package network_test

import (
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/network/networkpb"
)

func TestNetworkInfoRefusesWhatIsNoAddress(t *testing.T) {
	_, c := start(t, &firewall{leases: homeLeases()})

	_, err := c.GetNetworkInfo(t.Context(), &networkpb.GetNetworkInfoRequest{Addresses: []string{"198.18.1.20", "media.home.example"}})

	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "media.home.example") {
		t.Errorf("GetNetworkInfo = %v, want InvalidArgument naming media.home.example", err)
	}
}

func TestNetworkInfoOfAServerInTheBypassAliasIsDirectWithNoDevice(t *testing.T) {
	_, c := start(t, &firewall{leases: homeLeases()})

	got, err := c.GetNetworkInfo(t.Context(), &networkpb.GetNetworkInfoRequest{Addresses: []string{"198.18.2.10", "198.18.1.20"}})
	if err != nil {
		t.Fatal(err)
	}

	infos := got.GetInfos()
	if len(infos) != 2 || infos[0].GetInternetPath() != direct || infos[0].GetDevice() != nil {
		t.Errorf("198.18.2.10 = %v, want Direct and no Device", infos)
	}
	if len(infos) == 2 && (infos[1].GetInternetPath() != tunnelled || infos[1].GetDevice().GetName() != "Living room TV") {
		t.Errorf("198.18.1.20 = %v, want the TV, Tunnelled", infos[1])
	}
}
