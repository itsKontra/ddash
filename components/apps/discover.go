package apps

import (
	"cmp"
	"net"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
)

// discovered is the App a Proxy Host yields, before its overlay.
type discovered struct {
	serverName string
	url        string
	https      bool
	aliases    []string
	// upstream is where the block proxies to; zero when ddash can't tell.
	upstream hostPort
}

// discover yields one App per server block that proxies (some location has
// a proxy_pass), skipping redirect-only and catch-all blocks. Blocks that
// share a first server_name (e.g. one per port) are one App, on https when
// any of them listens with ssl (and then proxying where that one does).
func discover(snap *nginxconfigpb.Snapshot) []discovered {
	var out []discovered
	at := map[string]int{}
	upstreams := upstreamsOf(snap)
	for _, s := range snap.GetServers() {
		d, ok := fromServer(s, upstreams)
		if !ok {
			continue
		}
		i, seen := at[d.serverName]
		if !seen {
			at[d.serverName] = len(out)
			out = append(out, d)
			continue
		}
		prev := &out[i]
		if d.https && !prev.https {
			prev.url, prev.https, prev.upstream = d.url, true, d.upstream
		}
		for _, a := range d.aliases {
			if !slices.Contains(prev.aliases, a) {
				prev.aliases = append(prev.aliases, a)
			}
		}
	}
	return out
}

func fromServer(s *nginxconfigpb.Server, upstreams map[string][]string) (discovered, bool) {
	var names []string
	for _, n := range s.GetServerNames() {
		if n != "" && n != "_" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	if len(names) == 0 || !literal(names[0]) || !proxies(s) {
		return discovered{}, false
	}
	for _, l := range s.GetListens() {
		if l.GetDefaultServer() {
			return discovered{}, false // the catch-all for its address
		}
	}
	d := discovered{serverName: names[0]}
	for _, n := range names[1:] {
		if literal(n) {
			d.aliases = append(d.aliases, n)
		}
	}
	d.url, d.https = appURL(names[0], s.GetListens())
	d.upstream = upstreamOf(proxyPass(s), upstreams)
	return d, true
}

// proxyPass is the block's proxy_pass for "/", else its first one.
func proxyPass(s *nginxconfigpb.Server) string {
	first := ""
	for _, l := range s.GetLocations() {
		if l.GetProxyPass() == "" {
			continue
		}
		if l.GetPath() == "/" {
			return l.GetProxyPass()
		}
		if first == "" {
			first = l.GetProxyPass()
		}
	}
	return first
}

// literal reports whether a server_name is one host, not a wildcard
// ("*.home.example", ".home.example") or a regex ("~^…").
func literal(name string) bool {
	return !strings.ContainsAny(name, "*~") && !strings.HasPrefix(name, ".")
}

func proxies(s *nginxconfigpb.Server) bool {
	for _, l := range s.GetLocations() {
		if l.GetProxyPass() != "" {
			return true
		}
	}
	return false
}

// appURL is https://name when the block listens with ssl, else http://name,
// with the listen port when none of the block's listens for that scheme is
// on its default port.
func appURL(name string, listens []*nginxconfigpb.Listen) (string, bool) {
	https := false
	for _, l := range listens {
		https = https || l.GetSsl()
	}
	scheme, std := "http", 80
	if https {
		scheme, std = "https", 443
	}
	port := 0
	for _, l := range listens {
		if l.GetSsl() != https {
			continue
		}
		p, ok := listenPort(l.GetAddress())
		if !ok {
			continue
		}
		if p == std {
			port = 0
			break
		}
		if port == 0 {
			port = p
		}
	}
	host := name
	if port != 0 {
		host = net.JoinHostPort(name, strconv.Itoa(port))
	}
	return scheme + "://" + host, https
}

// listenPort reads the port of a listen address as written: "443",
// "[::]:443", "198.18.2.5:8080", or a bare host meaning 80. Unix sockets
// have none.
func listenPort(addr string) (int, bool) {
	if strings.HasPrefix(addr, "unix:") {
		return 0, false
	}
	if p, err := strconv.Atoi(addr); err == nil {
		return p, true
	}
	if _, port, err := net.SplitHostPort(addr); err == nil {
		p, err := strconv.Atoi(port)
		return p, err == nil
	}
	return 80, true
}

func (d discovered) app(o *appspb.Overlay, src sources) *appspb.App {
	a := &appspb.App{
		Id:         "proxy:" + d.serverName,
		ServerName: d.serverName,
		Name:       defaultName(d.serverName),
		Url:        d.url,
		Aliases:    d.aliases,
	}
	if o != nil {
		a.Overlay = o
		a.Name = cmp.Or(o.GetName(), a.Name)
		a.Icon, a.Group, a.Hidden = o.GetIcon(), o.GetGroup(), o.GetHidden()
	}
	a.Workload = src.workloadFor(o.GetWorkload(), d.upstream)
	return a
}

// defaultName reads a display name off a server_name's first label:
// "grafana.home.example" → "Grafana", "home-assistant.…" → "Home Assistant".
func defaultName(serverName string) string {
	label, _, _ := strings.Cut(serverName, ".")
	words := strings.FieldsFunc(label, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		r, n := utf8.DecodeRuneInString(w)
		words[i] = string(unicode.ToUpper(r)) + w[n:]
	}
	return strings.Join(words, " ")
}
