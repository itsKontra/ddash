package scrub_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/internal/scrub"
)

// The "real" homelab in these tests uses documentation ranges only:
// 192.0.2.0/24 and 198.51.100.0/24 stand for the LAN subnets, 100.64.7.9
// for some other address, 3fff::/20 for IPv6 and realm.test for the domain.

func scrubber(t *testing.T, cfg scrub.Config) *scrub.Scrubber {
	t.Helper()
	if cfg.Key == nil {
		cfg.Key = []byte("test key")
	}
	s, err := scrub.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func subnets(t *testing.T, text string) []scrub.Subnet {
	t.Helper()
	s, err := scrub.ParseSubnets(text)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func fixture(t *testing.T, s *scrub.Scrubber, file, body string) (string, string) {
	t.Helper()
	name, out, err := s.Fixture(file, []byte(body))
	if err != nil {
		t.Fatalf("Fixture(%s): %v", file, err)
	}
	return name, string(out)
}

func TestEachRealSubnetBecomesItsOwnScrubbedSubnetKeepingHostOctets(t *testing.T) {
	s := scrubber(t, scrub.Config{Subnets: subnets(t, "192.0.2.0/24=1 198.51.100.0/24=2")})

	_, got := fixture(t, s, "hosts.txt", "lan 192.0.2.20 servers 198.51.100.10/24 gw 192.0.2.1:443\n")

	if want := "lan 198.18.1.20 servers 198.18.2.10/24 gw 198.18.1.1:443\n"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestOtherIPv4BecomesAStableAddressIn203_0_113(t *testing.T) {
	s := scrubber(t, scrub.Config{})

	_, a := fixture(t, s, "a.txt", "wan 100.64.7.9 dns 100.64.7.10")
	_, again := fixture(t, scrubber(t, scrub.Config{}), "a.txt", "wan 100.64.7.9")
	_, otherKey := fixture(t, scrubber(t, scrub.Config{Key: []byte("other")}), "a.txt", "wan 100.64.7.9")

	f := strings.Fields(a)
	if !strings.HasPrefix(f[1], "203.0.113.") || !strings.HasPrefix(f[3], "203.0.113.") || f[1] == f[3] {
		t.Errorf("got %q, want two different 203.0.113.x", a)
	}
	if again != "wan "+f[1] {
		t.Errorf("same key gave %q, want %q", again, "wan "+f[1])
	}
	if otherKey == "wan "+f[1] {
		t.Errorf("another key gave the same %q", otherKey)
	}
}

func TestAddressesAFixtureMayHoldStayAsTheyAre(t *testing.T) {
	s := scrubber(t, scrub.Config{})
	in := "lo 127.0.0.1 any 0.0.0.0 mask 255.255.255.0 bcast 255.255.255.255 done 198.18.4.4 doc 203.0.113.9 version 1.2.3.4.5 v6 ::1 2001:db8::7 ::"

	if _, got := fixture(t, s, "a.txt", in); got != in {
		t.Errorf("got  %q\nwant %q", got, in)
	}
}

func TestIPv6BecomesAnAddressIn2001Db8KeepingSubnetsTogether(t *testing.T) {
	s := scrubber(t, scrub.Config{})

	_, got := fixture(t, s, "a.txt", "a 3fff:12:34:56::1 b 3fff:12:34:56:abcd:ef01:2345:6789/64 c 3fff:99::1 mapped ::ffff:100.64.7.9 at 12:34:56")

	f := strings.Fields(got)
	for _, i := range []int{1, 3, 5} {
		if !strings.HasPrefix(f[i], "2001:db8:") {
			t.Errorf("%s = %s, want it in 2001:db8::/32", f[i-1], f[i])
		}
	}
	prefix := func(a string) string { return strings.Join(strings.Split(a, ":")[:4], ":") }
	if f[1] != prefix(f[1])+"::1" {
		t.Errorf("a = %s: a small host part is kept", f[1])
	}
	if prefix(f[1]) != prefix(f[3]) || prefix(f[1]) == prefix(f[5]) {
		t.Errorf("a %s and b %s must share their /64, c %s not", f[1], f[3], f[5])
	}
	if !strings.HasSuffix(f[3], "/64") || strings.Contains(f[3], "abcd") {
		t.Errorf("b = %s, want its host part hashed and the prefix length kept", f[3])
	}
	if !strings.HasPrefix(f[7], "::ffff:203.0.113.") {
		t.Errorf("mapped = %s, want ::ffff:203.0.113.x", f[7])
	}
	if f[9] != "12:34:56" {
		t.Errorf("a time %s changed", f[9])
	}
}

func TestMACsBecomeLocallyAdministeredByKeyedHash(t *testing.T) {
	s := scrubber(t, scrub.Config{})

	_, got := fixture(t, s, "a.txt", "lo aa:bb:cc:dd:ee:01 up AA:BB:CC:DD:EE:01 dash aa-bb-cc-dd-ee-02 zero 00:00:00:00:00:00 bcast ff:ff:ff:ff:ff:ff")
	_, otherKey := fixture(t, scrubber(t, scrub.Config{Key: []byte("other")}), "a.txt", "aa:bb:cc:dd:ee:01")

	f := strings.Fields(got)
	if !strings.HasPrefix(f[1], "02:") || strings.Contains(f[1], "aa:bb") || len(f[1]) != 17 {
		t.Errorf("lo = %s, want 02:… of the same shape", f[1])
	}
	if f[3] != strings.ToUpper(f[1]) {
		t.Errorf("upper case %s, want %s in upper case: the same MAC", f[3], f[1])
	}
	if !strings.HasPrefix(f[5], "02-") || f[5] == strings.ReplaceAll(f[1], ":", "-") {
		t.Errorf("dash = %s, want another 02-… MAC", f[5])
	}
	if f[7] != "00:00:00:00:00:00" || f[9] != "ff:ff:ff:ff:ff:ff" {
		t.Errorf("zero/broadcast changed: %q", got)
	}
	if otherKey == f[1] {
		t.Errorf("another key gave the same %s", otherKey)
	}
}

func literals(t *testing.T, text string) []scrub.Literal {
	t.Helper()
	l, err := scrub.ParseMap(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestTheDomainBecomesHomeExample(t *testing.T) {
	s := scrubber(t, scrub.Config{Domains: []string{"realm.test"}})

	_, got := fixture(t, s, "a.txt", "grafana.realm.test GRAFANA.Realm.Test realm.test. surrealm.test realm.testing")

	if want := "grafana.home.example GRAFANA.home.example home.example. surrealm.test realm.testing"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestScrubMapLiteralsBecomeTheirReplacementInBodiesAndFileNames(t *testing.T) {
	s := scrubber(t, scrub.Config{Literals: literals(t, `
# hosts
secretbox = node1
secretbox-7 = node7
Smith Family = Household
`)})

	name, got := fixture(t, s, "api2/json/nodes/secretbox-7/lxc.json",
		`{"node": "secretbox-7", "other": "SecretBox", "keep": "secretboxes", "owner": "the smith family"}`)

	if name != "api2/json/nodes/node7/lxc.json" {
		t.Errorf("file = %s", name)
	}
	if want := `"node": "node7"`; !strings.Contains(got, want) {
		t.Errorf("got %s, want %s (the longest literal first)", got, want)
	}
	if !strings.Contains(got, `"other": "node1"`) || !strings.Contains(got, `"owner": "the Household"`) {
		t.Errorf("got %s, want literals replaced whatever their case", got)
	}
	if !strings.Contains(got, `"keep": "secretboxes"`) {
		t.Errorf("got %s, want a literal inside a longer word kept", got)
	}
}

func TestSecretLikeFieldsBecomeRedacted(t *testing.T) {
	s := scrubber(t, scrub.Config{})

	_, got := fixture(t, s, "a.json", `{"api_key": "k1", "Password": "p1", "privateKey": "pk", "token": "", "token_set": true,
"peers": [{"presharedkey": "psk1", "publickey": "pub1"}], "client_secrets": ["s1", "s2"], "name": "n1",
"Env": ["API_TOKEN=t1", "TZ=UTC"]}`)

	want := `{
  "api_key": "REDACTED",
  "Password": "REDACTED",
  "privateKey": "REDACTED",
  "token": "",
  "token_set": true,
  "peers": [
    {
      "presharedkey": "REDACTED",
      "publickey": "pub1"
    }
  ],
  "client_secrets": [
    "REDACTED",
    "REDACTED"
  ],
  "name": "n1",
  "Env": [
    "API_TOKEN=REDACTED",
    "TZ=UTC"
  ]
}
`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSecretsInTextBecomeRedacted(t *testing.T) {
	s := scrubber(t, scrub.Config{})

	_, got := fixture(t, s, "nginx_-T.stdout", "proxy_set_header Authorization \"Basic dXNlcjpwYXNz\";\nenv DB_PASSWORD=hunter2;\nssl_session_tickets off;\n")

	if want := "proxy_set_header Authorization \"Basic REDACTED\";\nenv DB_PASSWORD=REDACTED;\nssl_session_tickets off;\n"; got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// queryLog is AdGuard Home's /control/querylog with n entries, newest
// first: entry i asked for d<i%7>.ads.test at minute 59-i.
func queryLog(n int) string {
	var entries []string
	for i := range n {
		entries = append(entries, fmt.Sprintf(`{"answer": [{"type": "CNAME", "value": "edge%d.cdn.test.", "ttl": 30}, {"type": "A", "value": "100.64.7.%d", "ttl": 30}],
"client": "192.0.2.150", "question": {"class": "IN", "name": "d%d.ads.test", "type": "A"},
"reason": "FilteredBlackList", "rules": [{"text": "||d%d.ads.test^", "filter_list_id": 1}], "time": "2026-10-20T09:%02d:00Z"}`, i%7, i, i%7, i%7, 59-i))
	}
	return `{"data": [` + strings.Join(entries, ",") + `], "oldest": "2026-10-20T09:00:00Z"}`
}

func TestTheAdGuardQueryLogKeepsItsNewestEntriesWithSiteNDomains(t *testing.T) {
	s := scrubber(t, scrub.Config{Subnets: subnets(t, "192.0.2.0/24=1"), Domains: []string{"realm.test"}})

	_, got := fixture(t, s, "control/querylog.json", queryLog(25))

	var log struct {
		Data []struct {
			Answer   []struct{ Type, Value string }
			Client   string
			Question struct{ Name string }
			Rules    []struct{ Text string }
			Time     string
		}
		Oldest string
	}
	if err := json.Unmarshal([]byte(got), &log); err != nil {
		t.Fatal(err)
	}
	if len(log.Data) != scrub.QueryLogEntries || log.Oldest != log.Data[len(log.Data)-1].Time {
		t.Fatalf("%d entries, oldest %s; want %d, oldest the last kept", len(log.Data), log.Oldest, scrub.QueryLogEntries)
	}
	first, eighth := log.Data[0], log.Data[7] // both asked d0.ads.test
	if first.Question.Name != "site1.example" || eighth.Question.Name != "site1.example" || log.Data[1].Question.Name != "site3.example" {
		t.Errorf("names %s %s %s, want site1 site1 site3 (each domain numbered once, in order, CNAME targets too)",
			first.Question.Name, eighth.Question.Name, log.Data[1].Question.Name)
	}
	if first.Rules[0].Text != "||site1.example^" || first.Answer[0].Value != "site2.example." {
		t.Errorf("rule %q, CNAME %q: want the domains replaced there too", first.Rules[0].Text, first.Answer[0].Value)
	}
	if first.Client != "198.18.1.150" || !strings.HasPrefix(first.Answer[1].Value, "203.0.113.") {
		t.Errorf("client %s, answer %s: want addresses scrubbed", first.Client, first.Answer[1].Value)
	}
	if strings.Contains(got, "ads.test") || strings.Contains(got, "cdn.test") {
		t.Errorf("a queried domain survived:\n%s", got)
	}
}

func TestOwnNamesInTheQueryLogBecomeHomeExampleNotSites(t *testing.T) {
	s := scrubber(t, scrub.Config{Domains: []string{"realm.test"}})

	_, got := fixture(t, s, "control/querylog.json",
		`{"data": [{"question": {"name": "nas.realm.test"}, "time": "t1"}, {"question": {"name": "site4.example"}, "time": "t2"}], "oldest": "t2"}`)

	if !strings.Contains(got, `"nas.home.example"`) || !strings.Contains(got, `"site4.example"`) {
		t.Errorf("got %s, want the homelab's own names under home.example and scrubbed ones kept", got)
	}
}

func TestAdGuardStatsTopDomainsShareTheQueryLogsSiteNumbers(t *testing.T) {
	s := scrubber(t, scrub.Config{})
	fixture(t, s, "control/querylog.json", queryLog(1))

	_, got := fixture(t, s, "control/stats.json",
		`{"top_queried_domains": [{"d0.ads.test": 10}, {"other.test": 5}], "top_blocked_domains": [{"d0.ads.test": 3}], "top_clients": [{"100.64.7.9": 9}]}`)

	var stats struct {
		Queried, Blocked, Clients []map[string]int
	}
	var raw map[string][]map[string]int
	if err := json.Unmarshal([]byte(got), &raw); err != nil {
		t.Fatal(err)
	}
	stats.Queried, stats.Blocked, stats.Clients = raw["top_queried_domains"], raw["top_blocked_domains"], raw["top_clients"]
	if stats.Queried[0]["site1.example"] != 10 || stats.Queried[1]["site3.example"] != 5 || stats.Blocked[0]["site1.example"] != 3 {
		t.Errorf("top domains %v %v, want site1 (from the query log) then site3", stats.Queried, stats.Blocked)
	}
	for client := range stats.Clients[0] {
		if !strings.HasPrefix(client, "203.0.113.") {
			t.Errorf("top client %s, want it scrubbed", client)
		}
	}
}

func TestBareSubnetsTakeTheNextFreeNumber(t *testing.T) {
	s := scrubber(t, scrub.Config{Subnets: subnets(t, "192.0.2.0/24, 198.51.100.0/24=1")})

	if _, got := fixture(t, s, "a.txt", "192.0.2.5 198.51.100.5"); got != "198.18.2.5 198.18.1.5" {
		t.Errorf("got %q", got)
	}
}

func TestNewRefusesWhatCantBeScrubbed(t *testing.T) {
	for name, cfg := range map[string]scrub.Config{
		"no key":                     {},
		"a wide subnet":              {Key: []byte("k"), Subnets: subnets(t, "192.0.0.0/16")},
		"a taken number":             {Key: []byte("k"), Subnets: subnets(t, "192.0.2.0/24=3 198.51.100.0/24=3")},
		"an IPv6 subnet":             {Key: []byte("k"), Subnets: subnets(t, "3fff::/64")},
		"a number too big":           {Key: []byte("k"), Subnets: subnets(t, "192.0.2.0/24=256")},
		"the scrubbed domain itself": {Key: []byte("k"), Domains: []string{"home.example"}},
	} {
		if _, err := scrub.New(cfg); err == nil {
			t.Errorf("%s: New succeeded", name)
		}
	}
}

func TestParseMapRefusesALineWithoutAReplacement(t *testing.T) {
	if _, err := scrub.ParseMap(strings.NewReader("secretbox\n")); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("err = %v, want line 1 refused", err)
	}
}

func TestAResultStillHoldingSomethingRealIsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  scrub.Config
		body string
		want string
	}{
		// JSON's own escapes are decoded and scrubbed; these are not.
		"an escaped literal": {
			scrub.Config{Literals: []scrub.Literal{{Real: "secretbox", Replacement: "node1"}}},
			`labels {"node": "\u0073ecretbox"}`, "secretbox",
		},
		"a literal escaped twice in JSON": {
			scrub.Config{Literals: []scrub.Literal{{Real: "secretbox", Replacement: "node1"}}},
			`{"label": "{\"node\": \"\\u0073ecretbox\"}"}`, "secretbox",
		},
		"a replacement holding its literal": {
			scrub.Config{Literals: []scrub.Literal{{Real: "box", Replacement: "the box"}}},
			`{"node": "box"}`, "box",
		},
		"an escaped domain": {
			scrub.Config{Domains: []string{"realm.test"}},
			`host nas.realm\u002etest`, "realm.test",
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := scrubber(t, tc.cfg)
			_, _, err := s.Fixture("a.json", []byte(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want a refusal naming %q", err, tc.want)
			}
		})
	}
}
