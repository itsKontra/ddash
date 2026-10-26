// Package scrub rewrites what a real homelab system answered into a fixture
// that is safe to commit. Recording (`make record`) runs every body through
// a Scrubber:
//
//   - each real subnet becomes its own 198.18.N.0/24, keeping host octets;
//   - any other IPv4 address becomes one in 203.0.113.0/24, and IPv6 one in
//     2001:db8::/32 (by keyed hash, so re-recording is stable);
//   - a MAC becomes 02:… by keyed hash, and a public key (WireGuard's)
//     another key of the same shape;
//   - the homelab's domain becomes home.example, and each literal from the
//     gitignored scrub.map its replacement;
//   - the values of secret-like fields become REDACTED;
//   - AdGuard Home's query log keeps its newest QueryLogEntries entries, and
//     queried domains (there and in the stats' top lists) become siteN.example.
//
// Fixture refuses a result that still holds a scrub.map literal, the real
// domain or an address the fixture IP guard (internal/ipguard) rejects.
package scrub

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/itsKontra/ddash/internal/ipguard"
)

// Domain is what the homelab's domain becomes.
const Domain = "home.example"

// Redacted replaces a secret-like field's value.
const Redacted = "REDACTED"

// Config says what to scrub.
type Config struct {
	// Key keys the hashes that pick MACs and addresses outside Subnets
	// (SCRUB_KEY in smoke.env). Required.
	Key []byte
	// Subnets are the homelab's own subnets, each with its 198.18.N.0/24.
	Subnets []Subnet
	// Domains are the homelab's domains, each becoming home.example.
	Domains []string
	// Literals are scrub.map's names and their replacements.
	Literals []Literal
}

// Subnet maps a real IPv4 subnet (/24 or narrower) to 198.18.N.0/24.
type Subnet struct {
	Prefix netip.Prefix
	N      int
}

// Literal is one scrub.map line: a real name and what it becomes.
type Literal struct {
	Real, Replacement string
}

// Scrubber scrubs fixtures. It is safe for concurrent use; the siteN
// numbering is shared by everything one Scrubber scrubs.
type Scrubber struct {
	cfg      Config
	literals []*regexp.Regexp
	domains  []*regexp.Regexp

	mu    sync.Mutex
	sites map[string]string
}

// New returns a Scrubber for cfg.
func New(cfg Config) (*Scrubber, error) {
	if len(cfg.Key) == 0 {
		return nil, errors.New("scrub: a key is required (SCRUB_KEY)")
	}
	seen := map[int]bool{}
	for _, s := range cfg.Subnets {
		if !s.Prefix.Addr().Is4() || s.Prefix.Bits() < 24 {
			return nil, fmt.Errorf("scrub: subnet %s: want an IPv4 subnet of /24 or narrower", s.Prefix)
		}
		if s.N < 0 || s.N > 255 || seen[s.N] {
			return nil, fmt.Errorf("scrub: subnet %s: 198.18.%d.0/24 is out of range or taken", s.Prefix, s.N)
		}
		seen[s.N] = true
	}
	s := &Scrubber{cfg: cfg, sites: map[string]string{}}
	for _, d := range cfg.Domains {
		d = strings.Trim(strings.TrimSpace(d), ".")
		if d == "" {
			continue
		}
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(d))
		if found(re, Domain, domainBoundary) {
			return nil, fmt.Errorf("scrub: domain %s: %s itself would hold it", d, Domain)
		}
		s.domains = append(s.domains, re)
	}
	lits := slices.Clone(cfg.Literals)
	slices.SortStableFunc(lits, func(a, b Literal) int { return len(b.Real) - len(a.Real) })
	s.cfg.Literals = lits
	for _, l := range lits {
		s.literals = append(s.literals, regexp.MustCompile(`(?i)`+regexp.QuoteMeta(l.Real)))
	}
	return s, nil
}

// ParseMap reads scrub.map: one "real = replacement" per line; blank lines
// and lines starting with # are skipped. Matching ignores case and needs
// the literal to stand alone: "nas" is not replaced inside "nasty".
func ParseMap(r io.Reader) ([]Literal, error) {
	var out []Literal
	lines := bufio.NewScanner(r)
	for n := 1; lines.Scan(); n++ {
		line := strings.TrimSpace(lines.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		real, repl, ok := strings.Cut(line, "=")
		real, repl = strings.TrimSpace(real), strings.TrimSpace(repl)
		if !ok || real == "" || repl == "" {
			return nil, fmt.Errorf("scrub.map line %d: want \"real = replacement\"", n)
		}
		out = append(out, Literal{Real: real, Replacement: repl})
	}
	return out, lines.Err()
}

// ParseSubnets reads SCRUB_SUBNETS: subnets separated by spaces or commas,
// each "CIDR=N" or a bare "CIDR" taking the next free N from 1.
func ParseSubnets(text string) ([]Subnet, error) {
	var out []Subnet
	taken := map[int]bool{}
	var bare []netip.Prefix
	for field := range strings.FieldsFuncSeq(text, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		cidr, n, numbered := strings.Cut(field, "=")
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("scrub: subnet %q: %w", field, err)
		}
		if !numbered {
			bare = append(bare, p.Masked())
			continue
		}
		v, err := strconv.Atoi(n)
		if err != nil {
			return nil, fmt.Errorf("scrub: subnet %q: N must be a number", field)
		}
		taken[v] = true
		out = append(out, Subnet{Prefix: p.Masked(), N: v})
	}
	next := 1
	for _, p := range bare {
		for taken[next] {
			next++
		}
		taken[next] = true
		out = append(out, Subnet{Prefix: p, N: next})
	}
	return out, nil
}

// Fixture scrubs one recorded response: its file name (relative to the
// scenario, slashes) and its body. It refuses a result that still holds
// something real.
func (s *Scrubber) Fixture(file string, body []byte) (string, []byte, error) {
	name := s.text(file)
	var out []byte
	if v, ok := decodeJSON(body); ok {
		out = encodeJSON(walkStrings(s.adguard(file, s.publicKeys(redact(v))), s.text))
	} else {
		out = []byte(s.text(string(body)))
	}
	if err := s.check(name, out); err != nil {
		return "", nil, err
	}
	return name, out, nil
}

// check refuses a scrubbed fixture that still holds a scrub.map literal,
// the real domain or an address a fixture may not hold, also behind
// \uXXXX escapes (JSON nested in a JSON string, or in text).
func (s *Scrubber) check(file string, body []byte) error {
	for _, t := range []string{file, string(body), unescape(string(body)), unescape(unescape(string(body)))} {
		for i, re := range s.literals {
			if found(re, t, wordBoundary) {
				return fmt.Errorf("scrub: %s still holds the scrub.map literal %q", file, s.cfg.Literals[i].Real)
			}
		}
		for i, re := range s.domains {
			if found(re, t, domainBoundary) {
				return fmt.Errorf("scrub: %s still holds the domain %q", file, s.cfg.Domains[i])
			}
		}
		for _, m := range ipv4Token.FindAllStringIndex(t, -1) {
			if ipv4Boundary(t, m[0]-1, m[1]) {
				continue
			}
			if ip, err := netip.ParseAddr(t[m[0]:m[1]]); err == nil && !ipguard.Allowed(ip) {
				return fmt.Errorf("scrub: %s still holds the address %s", file, ip)
			}
		}
	}
	return nil
}

var escape = regexp.MustCompile(`\\u([0-9A-Fa-f]{4})`)

func unescape(t string) string {
	t = escape.ReplaceAllStringFunc(t, func(m string) string {
		r, _ := strconv.ParseUint(m[2:], 16, 32)
		return string(rune(r))
	})
	return strings.ReplaceAll(t, `\\`, `\`)
}

func found(re *regexp.Regexp, t string, bad func(t string, before, after int) bool) bool {
	for _, m := range re.FindAllStringIndex(t, -1) {
		if !bad(t, m[0]-1, m[1]) {
			return true
		}
	}
	return false
}

func wordBoundary(t string, before, after int) bool {
	return alnum(at(t, before)) || alnum(at(t, after))
}

func domainBoundary(t string, before, after int) bool {
	b, a := at(t, before), at(t, after)
	return alnum(b) || b == '-' || alnum(a) || a == '-'
}

// text scrubs addresses, MACs, domains and literals in free text.
func (s *Scrubber) text(t string) string {
	t = redactText(t)
	for _, re := range s.domains {
		t = replace(re, t, domainBoundary, func(string) string { return Domain })
	}
	for i, re := range s.literals {
		repl := s.cfg.Literals[i].Replacement
		t = replace(re, t, wordBoundary, func(string) string { return repl })
	}
	t = replace(ipv6Token, t, ipv6Boundary, s.ipv6)
	t = replace(macColon, t, macBoundary(':'), s.mac)
	t = replace(macDash, t, macBoundary(0), s.mac)
	t = replace(ipv4Token, t, ipv4Boundary, s.ipv4)
	return t
}

// replace replaces each match of re in t that sits on a boundary (bad says
// whether the byte before or after it is part of a longer token) with fn of it.
func replace(re *regexp.Regexp, t string, bad func(t string, before, after int) bool, fn func(string) string) string {
	var b strings.Builder
	last := 0
	for _, m := range re.FindAllStringIndex(t, -1) {
		if bad(t, m[0]-1, m[1]) {
			continue
		}
		b.WriteString(t[last:m[0]])
		b.WriteString(fn(t[m[0]:m[1]]))
		last = m[1]
	}
	if last == 0 {
		return t
	}
	b.WriteString(t[last:])
	return b.String()
}

func digit(c byte) bool { return c >= '0' && c <= '9' }
func alnum(c byte) bool { return digit(c) || c|0x20 >= 'a' && c|0x20 <= 'z' || c == '_' }
func at(t string, i int) byte {
	if i < 0 || i >= len(t) {
		return ' '
	}
	return t[i]
}

// ipv4Token is a dotted quad. On a boundary it is not inside a longer run
// of digits and dots (a version like 1.2.3.4.5), but a sentence's full
// stop may follow it.
var ipv4Token = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)

func ipv4Boundary(t string, before, after int) bool {
	return digit(at(t, before)) || at(t, before) == '.' ||
		digit(at(t, after)) || at(t, after) == '.' && digit(at(t, after+1))
}

// ipv6Token is a run of hex digits, colons and dots holding a colon; it is
// an address only if it parses as one.
var ipv6Token = regexp.MustCompile(`[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*[0-9A-Fa-f:]`)

func ipv6Boundary(t string, before, after int) bool {
	return alnum(at(t, before)) || alnum(at(t, after))
}

var (
	macColon = regexp.MustCompile(`[0-9A-Fa-f]{2}(?::[0-9A-Fa-f]{2}){5}`)
	macDash  = regexp.MustCompile(`[0-9A-Fa-f]{2}(?:-[0-9A-Fa-f]{2}){5}`)
)

func macBoundary(sep byte) func(t string, before, after int) bool {
	return func(t string, before, after int) bool {
		b, a := at(t, before), at(t, after)
		return alnum(b) || alnum(a) || sep != 0 && (b == sep || a == sep)
	}
}

func (s *Scrubber) ipv4(text string) string {
	ip, err := netip.ParseAddr(text)
	if err != nil {
		return text
	}
	return s.ipv4Addr(ip).String()
}

func (s *Scrubber) ipv4Addr(ip netip.Addr) netip.Addr {
	b := ip.As4()
	for _, sn := range s.cfg.Subnets {
		if sn.Prefix.Contains(ip) {
			return netip.AddrFrom4([4]byte{198, 18, byte(sn.N), b[3]})
		}
	}
	if ipguard.Allowed(ip) {
		return ip
	}
	h := s.hash([]byte("ipv4"), b[:])
	return netip.AddrFrom4([4]byte{203, 0, 113, 1 + h[0]%254})
}

var documentation = netip.MustParsePrefix("2001:db8::/32")

func (s *Scrubber) ipv6(text string) string {
	ip, err := netip.ParseAddr(text)
	if err != nil || !ip.Is6() {
		return text
	}
	if ip.Is4In6() {
		return "::ffff:" + s.ipv4Addr(ip.Unmap()).String()
	}
	if ip.IsLoopback() || ip.IsUnspecified() || documentation.Contains(ip) {
		return text
	}
	b := ip.As16()
	var out [16]byte
	copy(out[:4], []byte{0x20, 0x01, 0x0d, 0xb8})
	copy(out[4:8], s.hash([]byte("ipv6 /64"), b[:8]))
	// A small host part (::1, ::53) says nothing; a large one may hold a MAC.
	if binary.BigEndian.Uint64(b[8:]) < 1<<16 {
		copy(out[8:], b[8:])
	} else {
		copy(out[8:], s.hash([]byte("ipv6"), b[:]))
	}
	return netip.AddrFrom16(out).String()
}

func (s *Scrubber) mac(text string) string {
	sep := text[2:3]
	raw, err := hex.DecodeString(strings.ReplaceAll(text, sep, ""))
	if err != nil {
		return text
	}
	if allBytes(raw, 0) || allBytes(raw, 0xff) {
		return text
	}
	h := s.hash([]byte("mac"), raw)
	parts := []string{"02"}
	for _, c := range h[:5] {
		parts = append(parts, fmt.Sprintf("%02x", c))
	}
	out := strings.Join(parts, sep)
	if text != strings.ToLower(text) {
		out = strings.ToUpper(out)
	}
	return out
}

func allBytes(b []byte, v byte) bool {
	for _, c := range b {
		if c != v {
			return false
		}
	}
	return true
}

func (s *Scrubber) hash(parts ...[]byte) []byte {
	m := hmac.New(sha256.New, s.cfg.Key)
	for _, p := range parts {
		m.Write(p)
	}
	return m.Sum(nil)
}
