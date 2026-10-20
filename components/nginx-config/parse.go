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

// parse parses the dump with crossplane, nginx's own config parser, from
// the dumped files only, and returns every server block in load order.
func (d dump) parse() ([]*nginxconfigpb.Server, error) {
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
		return nil, fmt.Errorf("parse nginx config: %w", err)
	}
	if len(payload.Errors) > 0 {
		return nil, fmt.Errorf("parse nginx config: %w", payload.Errors[0].Error)
	}
	var servers []*nginxconfigpb.Server
	for _, cfg := range payload.Config {
		for _, http := range blocks(cfg.Parsed, "http") {
			for _, s := range blocks(http.Block, "server") {
				servers = append(servers, server(s))
			}
		}
	}
	return servers, nil
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
			out.Locations = append(out.Locations, location(d))
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

func location(d *crossplane.Directive) *nginxconfigpb.Location {
	l := &nginxconfigpb.Location{Path: strings.Join(d.Args, " ")}
	for _, inner := range d.Block {
		if inner.Directive == "proxy_pass" && len(inner.Args) > 0 {
			l.ProxyPass = inner.Args[0]
		}
	}
	return l
}
