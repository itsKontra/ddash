package auth

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Forwarded trusts X-Forwarded-For and X-Forwarded-Proto only on requests
// from the reverse proxy at trusted, an address or a CIDR. From it, the
// request's RemoteAddr becomes the client the proxy saw (port 0). From
// anyone else, both headers are dropped, so nothing behind it is fooled.
// An empty trusted trusts no one.
func Forwarded(trusted string) (func(http.Handler) http.Handler, error) {
	var proxy netip.Prefix
	if trusted != "" {
		var err error
		if strings.Contains(trusted, "/") {
			proxy, err = netip.ParsePrefix(trusted)
		} else {
			var addr netip.Addr
			addr, err = netip.ParseAddr(trusted)
			proxy = netip.PrefixFrom(addr, addr.BitLen())
		}
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: want an IP address or CIDR", trusted)
		}
		proxy = proxy.Masked()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !proxy.IsValid() || !proxy.Contains(peer(r)) {
				r.Header.Del("X-Forwarded-For")
				r.Header.Del("X-Forwarded-Proto")
			} else if client, ok := lastForwardedFor(r); ok {
				r.RemoteAddr = netip.AddrPortFrom(client, 0).String()
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// peer is the address the connection came from.
func peer(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, _ := netip.ParseAddr(host)
	return addr.Unmap()
}

// lastForwardedFor is the client the proxy itself saw: the last address in
// X-Forwarded-For. Earlier ones are whatever the client claimed.
func lastForwardedFor(r *http.Request) (netip.Addr, bool) {
	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return netip.Addr{}, false
	}
	list := strings.Split(values[len(values)-1], ",")
	addr, err := netip.ParseAddr(strings.TrimSpace(list[len(list)-1]))
	return addr.Unmap(), err == nil
}
