package ipguard_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/itsKontra/ddash/internal/ipguard"
)

func write(t *testing.T, root, path, body string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyAddressesOutsideTheScrubbedRangesInTestdataAreFound(t *testing.T) {
	root := t.TempDir()
	write(t, root, "components/x/testdata/baseline/hosts.json", `[
  {"ip": "198.18.2.10", "other": "203.0.113.7", "lo": "127.0.0.1"},
  {"mask": "255.255.255.0", "any": "0.0.0.0", "bcast": "255.255.255.255"},
  {"leaked": "192.168.1.20", "image": "nginx:1.25.3"},
  {"wan": "8.8.8.8"}
]`)
	write(t, root, "components/x/testdata/other/deep/more.txt", "gateway 10.0.0.1\n")
	write(t, root, "components/x/x.go", `const example = "192.168.1.1" // not a fixture`)
	write(t, root, "node_modules/pkg/testdata/a.json", `"10.1.2.3"`)

	got, err := ipguard.Check(root)
	if err != nil {
		t.Fatal(err)
	}

	want := []ipguard.Finding{
		{File: "components/x/testdata/baseline/hosts.json", Line: 4, Addr: "192.168.1.20"},
		{File: "components/x/testdata/baseline/hosts.json", Line: 5, Addr: "8.8.8.8"},
		{File: "components/x/testdata/other/deep/more.txt", Line: 1, Addr: "10.0.0.1"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("findings (-want +got):\n%s", diff)
	}
}

func TestANetmaskMustBeContiguous(t *testing.T) {
	root := t.TempDir()
	write(t, root, "testdata/masks.txt", "255.255.240.0 255.0.255.0\n")

	got, err := ipguard.Check(root)
	if err != nil {
		t.Fatal(err)
	}

	want := []ipguard.Finding{{File: "testdata/masks.txt", Line: 1, Addr: "255.0.255.0"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("findings (-want +got):\n%s", diff)
	}
}

// The guard itself: CI fails while any fixture in the repo leaks an
// address outside the scrubbed ranges.
func TestTheRepoFixturesHoldOnlyScrubbedAddresses(t *testing.T) {
	got, err := ipguard.Check("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		t.Errorf("%s:%d: %s is outside 198.18.0.0/16, 203.0.113.0/24, loopback and netmasks; scrub it", f.File, f.Line, f.Addr)
	}
}
