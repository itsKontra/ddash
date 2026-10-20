package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/itsKontra/ddash/sdk"
)

// Validate checks cfg, with its defaults applied, against its fields'
// required, format and min/max options: the rules the form checks too
// (web/src/config-rules.ts). Errors are keyed by path, e.g. "peers[1].port".
func Validate(cfg protoreflect.Message) sdk.FieldErrors {
	var errs sdk.FieldErrors
	validate(cfg, "", &errs)
	return errs
}

func validate(m protoreflect.Message, prefix string, errs *sdk.FieldErrors) {
	fds := m.Descriptor().Fields()
	for i := range fds.Len() {
		fd := fds.Get(i)
		opts := options(fd)
		path := prefix + string(fd.Name())
		if fd.IsList() {
			list := m.Get(fd).List()
			if opts.GetRequired() && list.Len() == 0 {
				errs.Add(path, "is required")
			}
			for j := range list.Len() {
				elem := fmt.Sprintf("%s[%d]", path, j)
				if fd.Message() != nil {
					validate(list.Get(j).Message(), elem+".", errs)
				} else if msg := checkScalar(fd, list.Get(j)); msg != "" {
					errs.Add(elem, "%s", msg)
				}
			}
			continue
		}
		if fd.Message() != nil || fd.IsMap() {
			continue // not in a config form
		}
		if !m.Has(fd) || empty(fd, m.Get(fd)) {
			if opts.GetRequired() {
				errs.Add(path, "is required")
			}
			continue
		}
		if msg := checkScalar(fd, m.Get(fd)); msg != "" {
			errs.Add(path, "%s", msg)
		}
	}
}

// empty reports whether v is an empty string or an enum's zero value.
func empty(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return v.String() == ""
	case protoreflect.EnumKind:
		return v.Enum() == 0
	}
	return false
}

// checkScalar checks one value of fd against its format and min/max.
func checkScalar(fd protoreflect.FieldDescriptor, v protoreflect.Value) string {
	opts := options(fd)
	switch fd.Kind() {
	case protoreflect.StringKind:
		if f := opts.GetFormat(); f != 0 {
			return CheckFormat(FormatName(f), v.String())
		}
	case protoreflect.Int64Kind:
		n := v.Int()
		if opts.Min != nil && n < opts.GetMin() {
			return fmt.Sprintf("must be at least %d", opts.GetMin())
		}
		if opts.Max != nil && n > opts.GetMax() {
			return fmt.Sprintf("must be at most %d", opts.GetMax())
		}
	}
	return ""
}

// formats check a value of each format, saying what is wrong with it.
var formats = map[string]func(string) string{
	"url": func(s string) string {
		u, err := url.Parse(s)
		if err != nil || u.Scheme == "" || u.Host == "" || strings.ContainsAny(s, " \t\n") {
			return "must be a URL like https://host.example"
		}
		return ""
	},
	"hostname": func(s string) string {
		if len(s) > 253 || !hostname.MatchString(s) {
			return "must be a hostname like pve.home.example"
		}
		return ""
	},
	"ip": func(s string) string {
		if _, ok := parseIP(s); !ok {
			return "must be an IP address"
		}
		return ""
	},
	"cidr": func(s string) string {
		addr, bits, ok := strings.Cut(s, "/")
		if _, okIP := parseIP(addr); !ok || !okIP || !prefixLen.MatchString(bits) {
			return "must be an address with a prefix length, like 198.18.1.0/24"
		}
		if _, err := netip.ParsePrefix(s); err != nil {
			return "must be an address with a prefix length, like 198.18.1.0/24"
		}
		return ""
	},
	"ip_range": func(s string) string {
		lo, hi, ok := strings.Cut(s, "-")
		a, okA := parseIP(lo)
		b, okB := parseIP(hi)
		if !ok || !okA || !okB || a.Is4() != b.Is4() || a.Compare(b) > 0 {
			return "must be a range of addresses, low to high, like 198.18.1.100-198.18.1.199"
		}
		return ""
	},
}

var (
	hostname  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*\.?$`)
	prefixLen = regexp.MustCompile(`^(0|[1-9][0-9]{0,2})$`)
)

// parseIP parses an IPv4 or IPv6 address without a zone.
func parseIP(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(s)
	return a, err == nil && a.Zone() == ""
}

// CheckFormat checks value against the format named name ("url", "ip", ...)
// and says what is wrong with it; "" when nothing is.
func CheckFormat(name, value string) string {
	check, ok := formats[name]
	if !ok {
		return "has an unknown format " + name
	}
	return check(value)
}
