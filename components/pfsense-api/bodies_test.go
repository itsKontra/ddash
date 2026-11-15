package pfsenseapi

import (
	"encoding/json"
	"testing"
)

// The keys are pfSense's: a body must reach it exactly as its REST API
// names them, with every field present.
func TestWriteBodiesCarryPfSensesKeys(t *testing.T) {
	for name, tc := range map[string]struct {
		body any
		want string
	}{
		"apply":                 {applyBody{}, `{"async":false}`},
		"firewall apply":        {firewallApplyBody{}, `{}`},
		"alias patch":           {aliasPatch{ID: 4, Address: []string{}, Detail: []string{}}, `{"id":4,"address":[],"detail":[],"apply":false}`},
		"Host Override":         {hostOverrideCreate{Host: "nas", Domain: "home.example", IP: []string{"198.18.1.20"}}, `{"host":"nas","domain":"home.example","ip":["198.18.1.20"],"descr":""}`},
		"readdress":             {hostOverrideReaddress{ID: 2, IP: []string{"198.18.1.21"}}, `{"id":2,"ip":["198.18.1.21"]}`},
		"Host Override alias":   {hostOverrideAliasCreate{ParentID: 2, Host: "files", Domain: "home.example"}, `{"parent_id":2,"host":"files","domain":"home.example","descr":""}`},
		"static mapping":        {staticMappingCreate{ParentID: "lan", MAC: "02:00:00:00:00:01", IPAddr: "198.18.1.50", Apply: true}, `{"parent_id":"lan","mac":"02:00:00:00:00:01","ipaddr":"198.18.1.50","hostname":"","descr":"","apply":true}`},
		"static mapping rename": {staticMappingRename{ParentID: "lan", ID: 0, Descr: "printer", Apply: true}, `{"parent_id":"lan","id":0,"descr":"printer","apply":true}`},
		"WireGuard peer": {
			wireguardPeerCreate{Enabled: true, Tun: "tun_wg1", PublicKey: "k", AllowedIPs: []wireguardAllowedIP{{Address: "198.18.40.2", Mask: 32}}},
			`{"enabled":true,"tun":"tun_wg1","publickey":"k","descr":"","allowedips":[{"address":"198.18.40.2","mask":32,"descr":""}]}`,
		},
	} {
		got, err := json.Marshal(tc.body)
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: %s (%v), want %s", name, got, err, tc.want)
		}
	}
}
