// Package ipguard finds IPv4 addresses in fixtures that recording should
// have scrubbed. Fixtures live in testdata/ directories; the only addresses
// allowed there are the scrubbed ranges 198.18.0.0/16 (homelab subnets,
// host octets kept) and 203.0.113.0/24 (every other address), loopback and
// netmasks.
package ipguard

import (
	"bufio"
	"io/fs"
	"math/bits"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Finding is an address that must not be in a fixture.
type Finding struct {
	// File is the fixture's path relative to the root, with slashes.
	File string
	// Line is 1-based.
	Line int
	Addr string
}

var allowed = []netip.Prefix{
	netip.MustParsePrefix("198.18.0.0/16"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("127.0.0.0/8"),
}

// skipped directories are never the repo's own fixtures.
var skipped = []string{".git", "node_modules", "dist", ".dev"}

// candidate is a dotted quad not part of a longer run of digits and dots;
// a dot after it counts as punctuation unless a digit follows.
var candidate = regexp.MustCompile(`(?:^|[^0-9.])(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})(?:[^0-9.]|\.[^0-9]|\.$|$)`)

// Check returns, in file then line order, every disallowed IPv4 address in
// a file under a testdata/ directory below root.
func Check(root string) ([]Finding, error) {
	var findings []Finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if slices.Contains(skipped, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !slices.Contains(strings.Split(rel, "/"), "testdata") {
			return nil
		}
		found, err := check(path, rel)
		findings = append(findings, found...)
		return err
	})
	return findings, err
}

func check(path, rel string) ([]Finding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var findings []Finding
	lines := bufio.NewScanner(f)
	lines.Buffer(nil, 16<<20)
	for n := 1; lines.Scan(); n++ {
		line := lines.Text()
		// Matches may share a separator, so scan on from inside each one.
		for at := 0; at < len(line); {
			m := candidate.FindStringSubmatchIndex(line[at:])
			if m == nil {
				break
			}
			addr := line[at+m[2] : at+m[3]]
			if ip, err := netip.ParseAddr(addr); err == nil && !Allowed(ip) {
				findings = append(findings, Finding{File: rel, Line: n, Addr: addr})
			}
			at += m[3]
		}
	}
	return findings, lines.Err()
}

// Allowed reports whether a fixture may hold the IPv4 address ip: one of the
// scrubbed ranges, loopback or a netmask.
func Allowed(ip netip.Addr) bool {
	if !ip.Is4() {
		return false
	}
	for _, p := range allowed {
		if p.Contains(ip) {
			return true
		}
	}
	return netmask(ip)
}

// netmask reports whether ip is ones followed only by zeros, like
// 255.255.255.0 or 0.0.0.0.
func netmask(ip netip.Addr) bool {
	b := ip.As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return bits.LeadingZeros32(^v)+bits.TrailingZeros32(v) >= 32
}
