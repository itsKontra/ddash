package nginxproxymanagerapi

import (
	"bytes"
	"fmt"
	"strconv"

	pb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
)

// proxyHostRow is one Proxy Host as Nginx Proxy Manager sends it. Only
// what ddash reads is decoded: a write is built from scratch, never
// echoed from a read, since request bodies are strict.
type proxyHostRow struct {
	ID                    int64    `json:"id"`
	DomainNames           []string `json:"domain_names"`
	ForwardScheme         string   `json:"forward_scheme"`
	ForwardHost           string   `json:"forward_host"`
	ForwardPort           int32    `json:"forward_port"`
	BlockExploits         flag     `json:"block_exploits"`
	AllowWebsocketUpgrade flag     `json:"allow_websocket_upgrade"`
	CertificateID         id       `json:"certificate_id"`
	SslForced             flag     `json:"ssl_forced"`
	Http2Support          flag     `json:"http2_support"`
	HstsEnabled           flag     `json:"hsts_enabled"`
	HstsSubdomains        flag     `json:"hsts_subdomains"`
	Enabled               flag     `json:"enabled"`
	Meta                  struct {
		NginxOnline *flag   `json:"nginx_online"`
		NginxErr    *string `json:"nginx_err"`
	} `json:"meta"`
}

func (r proxyHostRow) offline() bool {
	return r.Meta.NginxOnline != nil && !bool(*r.Meta.NginxOnline)
}

func (r proxyHostRow) proto() *pb.ProxyHost {
	p := &pb.ProxyHost{
		Id: r.ID,
		Fields: &pb.ProxyHostFields{
			DomainNames:    r.DomainNames,
			ForwardScheme:  r.ForwardScheme,
			ForwardHost:    r.ForwardHost,
			ForwardPort:    r.ForwardPort,
			BlockExploits:  bool(r.BlockExploits),
			Websockets:     bool(r.AllowWebsocketUpgrade),
			CertificateId:  int64(r.CertificateID),
			ForceSsl:       bool(r.SslForced),
			Http2:          bool(r.Http2Support),
			Hsts:           bool(r.HstsEnabled),
			HstsSubdomains: bool(r.HstsSubdomains),
		},
		Enabled: bool(r.Enabled),
	}
	if r.offline() {
		p.Offline = true
		if r.Meta.NginxErr != nil {
			p.NginxError = *r.Meta.NginxErr
		}
	}
	return p
}

// flag is a boolean as v2.10 sends it (0 or 1) or as later versions do
// (true or false).
type flag bool

func (f *flag) UnmarshalJSON(b []byte) error {
	switch string(bytes.Trim(b, `"`)) {
	case "true", "1":
		*f = true
	case "false", "0", "null", "":
		*f = false
	default:
		return fmt.Errorf("%s is no flag", b)
	}
	return nil
}

// id is an integer id, which Nginx Proxy Manager may send as a string; a
// certificate_id of "new" (requested inline, not issued yet) reads 0.
type id int64

func (i *id) UnmarshalJSON(b []byte) error {
	s := string(bytes.Trim(b, `"`))
	if s == "null" || s == "" || s == "new" {
		*i = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("%s is no id", b)
	}
	*i = id(n)
	return nil
}
