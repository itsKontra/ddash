package nginxconfig

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	crossplane "github.com/nginxinc/nginx-go-crossplane"

	"github.com/itsKontra/ddash/components/nginx-config/nginxconfigpb"
)

// marker starts each file in `nginx -T`'s dump: "# configuration file <path>:".
const marker = "# configuration file "

// dump is `nginx -T`'s stdout split into its files, the main config first.
type dump struct {
	main  string
	files map[string]string
}

// split cuts `nginx -T`'s stdout at its "# configuration file" markers.
// nginx writes each file it loaded as the marker line, the file, and a
// newline, in the order it opened them, so the first is the main config.
func split(out string) (dump, error) {
	d := dump{files: map[string]string{}}
	var name string
	var body strings.Builder
	flush := func() {
		if name != "" {
			d.files[name] = body.String()
		}
		body.Reset()
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, marker) && strings.HasSuffix(line, ":") {
			flush()
			name = strings.TrimSuffix(strings.TrimPrefix(line, marker), ":")
			if d.main == "" {
				d.main = name
			}
			continue
		}
		if name != "" {
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		return dump{}, err
	}
	if d.main == "" {
		return dump{}, errors.New("nginx -T printed no configuration file")
	}
	return d, nil
}

// parsed is what a dump holds that ddash reads.
type parsed struct {
	servers   []*nginxconfigpb.Server
	upstreams []*nginxconfigpb.Upstream
}

// parse parses the dump with crossplane, nginx's own config parser, from
// the dumped files only, and returns every http server and upstream block
// in load order.
func (d dump) parse() (parsed, error) {
	payload, err := crossplane.Parse(d.main, &crossplane.ParseOptions{
		Open: func(p string) (io.ReadCloser, error) {
			body, ok := d.files[p]
			if !ok {
				return nil, fmt.Errorf("%s isn't in nginx -T's dump", p)
			}
			return io.NopCloser(strings.NewReader(body)), nil
		},
		Glob: func(pattern string) ([]string, error) {
			var out []string
			for name := range d.files {
				if ok, err := path.Match(pattern, name); err != nil {
					return nil, err
				} else if ok {
					out = append(out, name)
				}
			}
			sort.Strings(out)
			return out, nil
		},
		// nginx has just loaded this config, so it is valid as written,
		// third-party directives included.
		SkipDirectiveContextCheck: true,
		SkipDirectiveArgsCheck:    true,
		CombineConfigs:            true,
	})
	if err != nil {
		return parsed{}, fmt.Errorf("parse nginx config: %w", err)
	}
	if len(payload.Errors) > 0 {
		return parsed{}, fmt.Errorf("parse nginx config: %w", payload.Errors[0].Error)
	}
	var out parsed
	for _, cfg := range payload.Config {
		for _, http := range blocks(cfg.Parsed, "http") {
			for _, d := range http.Block {
				switch d.Directive {
				case "server":
					out.servers = append(out.servers, server(d))
				case "upstream":
					out.upstreams = append(out.upstreams, upstream(d))
				}
			}
		}
	}
	return out, nil
}

func blocks(ds crossplane.Directives, name string) []*crossplane.Directive {
	var out []*crossplane.Directive
	for _, d := range ds {
		if d.Directive == name {
			out = append(out, d)
		}
	}
	return out
}

func server(s *crossplane.Directive) *nginxconfigpb.Server {
	out := &nginxconfigpb.Server{File: s.File, Line: int64(s.Line)}
	vars := sets(s.Block)
	for _, d := range s.Block {
		switch d.Directive {
		case "server_name":
			out.ServerNames = append(out.ServerNames, d.Args...)
		case "listen":
			out.Listens = append(out.Listens, listen(d.Args))
		case "ssl_certificate":
			if len(d.Args) > 0 && out.SslCertificate == "" {
				out.SslCertificate = d.Args[0]
			}
		case "return":
			out.Return = strings.Join(d.Args, " ")
		case "location":
			out.Locations = append(out.Locations, location(d, vars))
		}
	}
	return out
}

func upstream(u *crossplane.Directive) *nginxconfigpb.Upstream {
	out := &nginxconfigpb.Upstream{}
	if len(u.Args) > 0 {
		out.Name = u.Args[0]
	}
	for _, d := range u.Block {
		if d.Directive == "server" && len(d.Args) > 0 {
			out.Servers = append(out.Servers, d.Args[0])
		}
	}
	return out
}

func listen(args []string) *nginxconfigpb.Listen {
	l := &nginxconfigpb.Listen{}
	if len(args) > 0 {
		l.Address = args[0]
	}
	for _, a := range args[1:] {
		switch a {
		case "ssl":
			l.Ssl = true
		case "default_server", "default":
			l.DefaultServer = true
		}
	}
	return l
}

func location(d *crossplane.Directive, vars map[string]string) *nginxconfigpb.Location {
	l := &nginxconfigpb.Location{Path: strings.Join(d.Args, " ")}
	for _, inner := range d.Block {
		if inner.Directive == "proxy_pass" && len(inner.Args) > 0 {
			l.ProxyPass = expand(inner.Args[0], vars)
		}
	}
	return l
}

// sets is the variables a server block itself sets, in order, each value
// expanded with those set before it. Nginx Proxy Manager names each Proxy
// Host's target this way (`set $server "198.18.1.204";`) and proxies to
// "$forward_scheme://$server:$port$request_uri".
func sets(block crossplane.Directives) map[string]string {
	vars := map[string]string{}
	for _, d := range block {
		if d.Directive == "set" && len(d.Args) == 2 && strings.HasPrefix(d.Args[0], "$") {
			vars[strings.TrimPrefix(d.Args[0], "$")] = expand(d.Args[1], vars)
		}
	}
	return vars
}

// expand replaces each $name in s that vars holds with its
// value; any other variable (nginx's own, like $request_uri) stays.
func expand(s string, vars map[string]string) string {
	var out strings.Builder
	for {
		i := strings.IndexByte(s, '$')
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:i])
		s = s[i:]
		name, n := varName(s)
		if v, ok := vars[name]; ok && name != "" {
			out.WriteString(v)
		} else {
			out.WriteString(s[:n])
		}
		s = s[n:]
	}
}

// varName reads the variable s starts with ("$port…"): its name and how
// many bytes it takes; "" for a lone "$" (or "${…}", left as written).
func varName(s string) (string, int) {
	n := 1
	for n < len(s) && (s[n] == '_' || 'a' <= s[n] && s[n] <= 'z' || 'A' <= s[n] && s[n] <= 'Z' || '0' <= s[n] && s[n] <= '9') {
		n++
	}
	return s[1:n], n
}
