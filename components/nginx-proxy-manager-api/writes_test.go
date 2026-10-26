package nginxproxymanagerapi_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	pb "github.com/itsKontra/ddash/components/nginx-proxy-manager-api/nginxproxymanagerapipb"
	"github.com/itsKontra/ddash/components/nginx-proxy-manager-api/npmfake"
	"github.com/itsKontra/ddash/sdk"
)

// wiki is a new Proxy Host's fields, its names out of order.
func wiki() *pb.ProxyHostFields {
	return &pb.ProxyHostFields{
		DomainNames:   []string{"wiki.home.example", "docs.home.example"},
		ForwardScheme: "http", ForwardHost: "198.18.1.201", ForwardPort: 3010,
		BlockExploits: true, Websockets: true, CertificateId: 3, ForceSsl: true, Http2: true, Hsts: true,
	}
}

// writes are the requests in log that write Proxy Hosts, or read one.
func writes(log []string) []string {
	var out []string
	for _, e := range log {
		if strings.HasPrefix(e, "POST /api/nginx/") || strings.HasPrefix(e, "PUT ") || strings.HasPrefix(e, "DELETE ") || strings.HasPrefix(e, "GET /api/nginx/proxy-hosts/") {
			out = append(out, e)
		}
	}
	return out
}

// reason is the ErrorInfo reason err carries; "" for none.
func reason(err error) (string, map[string]string) {
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			return info.GetReason(), info.GetMetadata()
		}
	}
	return "", nil
}

func TestCreatingAProxyHostSavesTheFieldsDdashManages(t *testing.T) {
	f := npm(t, "baseline")
	_, c := start(t, f.URL)

	got, err := c.CreateProxyHost(t.Context(), &pb.CreateProxyHostRequest{Fields: wiki()})
	if err != nil {
		t.Fatal(err)
	}

	want := &pb.ProxyHost{Id: 39, Fields: wiki(), Enabled: true}
	want.Fields.DomainNames = []string{"docs.home.example", "wiki.home.example"}
	if diff := cmp.Diff(want, got.GetProxyHost(), protocmp.Transform()); diff != "" {
		t.Errorf("created (-want +got):\n%s", diff)
	}
	stored := f.ProxyHost(39)
	if stored["forward_host"] != "198.18.1.201" || stored["advanced_config"] != "" || stored["owner_user_id"] != float64(2) {
		t.Errorf("stored %v, want it owned by ddash's user with the forward host", stored)
	}
	if diff := cmp.Diff([]string{"POST /api/nginx/proxy-hosts"}, writes(f.Log())); diff != "" {
		t.Errorf("writes (-want +got):\n%s", diff)
	}

	// The cache is dropped: the next read lists it.
	list, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(list.GetProxyHosts()); n != 24 {
		t.Errorf("%d Proxy Hosts after the create, want 24", n)
	}
}

func TestNginxProxyManagerForcesTheFlagsADependentToggleRules(t *testing.T) {
	f := npm(t, "baseline")
	_, c := start(t, f.URL)
	fields := wiki()
	fields.CertificateId = 0

	got, err := c.CreateProxyHost(t.Context(), &pb.CreateProxyHostRequest{Fields: fields})
	if err != nil {
		t.Fatal(err)
	}

	p := got.GetProxyHost().GetFields()
	if p.GetForceSsl() || p.GetHttp2() || p.GetHsts() || p.GetHstsSubdomains() {
		t.Errorf("saved %v, want Force SSL, HTTP/2 and HSTS off without a certificate", p)
	}
}

func TestASaveNginxRejectsIsAnErrorCarryingNginxsMessage(t *testing.T) {
	const nginxErr = "nginx: [emerg] host not found in upstream \"wiki.lan\" in /data/nginx/proxy_host/39.conf:20\nnginx: configuration file /etc/nginx/nginx.conf test failed\n"
	for _, tc := range []struct {
		name  string
		write func(c pb.NginxProxyManagerApiServiceClient) error
		id    string
	}{
		{"create", func(c pb.NginxProxyManagerApiServiceClient) error {
			_, err := c.CreateProxyHost(t.Context(), &pb.CreateProxyHostRequest{Fields: wiki()})
			return err
		}, "39"},
		{"update", func(c pb.NginxProxyManagerApiServiceClient) error {
			_, err := c.UpdateProxyHost(t.Context(), &pb.UpdateProxyHostRequest{Id: 3, Fields: &pb.ProxyHostFields{
				DomainNames: []string{"grafana.home.example"}, ForwardScheme: "http", ForwardHost: "wiki.lan", ForwardPort: 3000,
			}})
			return err
		}, "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := npm(t, "baseline")
			_, c := start(t, f.URL)
			f.RejectNextSave(nginxErr)

			err := tc.write(c)

			want := "Nginx Proxy Manager saved Proxy Host " + tc.id + ", but nginx rejected its config, so it is offline: " + nginxErr
			if status.Code(err) != codes.Aborted || status.Convert(err).Message() != want {
				t.Errorf("error = %v, want Aborted: %s", err, want)
			}
			if r, meta := reason(err); r != "ERROR_REASON_PROXY_HOST_OFFLINE" || meta["id"] != tc.id || meta["nginx_error"] != nginxErr {
				t.Errorf("reason %s %v, want ERROR_REASON_PROXY_HOST_OFFLINE with the id and nginx's error", r, meta)
			}
			// Saved all the same, and listed Offline.
			list, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{})
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range list.GetProxyHosts() {
				if p.GetId() == 39 || p.GetId() == 3 && tc.id == "3" {
					if !p.GetOffline() || p.GetNginxError() != nginxErr {
						t.Errorf("Proxy Host %d = %v, want Offline", p.GetId(), p)
					}
				}
			}
		})
	}
}

func TestUpdatingAProxyHostKeepsWhatDdashDoesntManage(t *testing.T) {
	f := npm(t, "hand-tuned")
	_, c := start(t, f.URL)
	before := f.ProxyHost(21)
	fields := &pb.ProxyHostFields{
		DomainNames:   []string{"n8n.home.example", "automation.home.example"},
		ForwardScheme: "https", ForwardHost: "198.18.1.207", ForwardPort: 5443,
		BlockExploits: true, Websockets: false, CertificateId: 3, ForceSsl: true, Http2: false, Hsts: true, HstsSubdomains: false,
	}

	got, err := c.UpdateProxyHost(t.Context(), &pb.UpdateProxyHostRequest{Id: 21, Fields: fields})
	if err != nil {
		t.Fatal(err)
	}

	want := &pb.ProxyHost{Id: 21, Fields: fields, Enabled: true}
	want.Fields.DomainNames = []string{"automation.home.example", "n8n.home.example"}
	if diff := cmp.Diff(want, got.GetProxyHost(), protocmp.Transform()); diff != "" {
		t.Errorf("updated (-want +got):\n%s", diff)
	}
	after := f.ProxyHost(21)
	for _, k := range []string{"advanced_config", "locations", "access_list_id", "caching_enabled", "enabled", "owner_user_id", "created_on"} {
		if diff := cmp.Diff(before[k], after[k]); diff != "" {
			t.Errorf("%s changed (-before +after):\n%s", k, diff)
		}
	}
	// Read first, then written; npmfake refuses any field v2.10 rejects.
	if diff := cmp.Diff([]string{"GET /api/nginx/proxy-hosts/21", "PUT /api/nginx/proxy-hosts/21"}, writes(f.Log())); diff != "" {
		t.Errorf("requests (-want +got):\n%s", diff)
	}
}

func TestUpdatingOrDeletingAProxyHostThatIsGoneIsNotFound(t *testing.T) {
	f := npm(t, "baseline")
	_, c := start(t, f.URL)

	_, err := c.UpdateProxyHost(t.Context(), &pb.UpdateProxyHostRequest{Id: 99, Fields: wiki()})
	if status.Code(err) != codes.NotFound {
		t.Errorf("UpdateProxyHost error = %v, want NotFound", err)
	}
	_, err = c.DeleteProxyHost(t.Context(), &pb.DeleteProxyHostRequest{Id: 99})
	if status.Code(err) != codes.NotFound {
		t.Errorf("DeleteProxyHost error = %v, want NotFound", err)
	}
	for _, w := range writes(f.Log()) {
		if strings.HasPrefix(w, "PUT ") {
			t.Errorf("sent %s for a Proxy Host that is gone", w)
		}
	}
}

func TestDeletingAProxyHostRemovesIt(t *testing.T) {
	f := npm(t, "baseline")
	_, c := start(t, f.URL)
	if _, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.DeleteProxyHost(t.Context(), &pb.DeleteProxyHostRequest{Id: 3}); err != nil {
		t.Fatal(err)
	}

	if f.ProxyHost(3) != nil {
		t.Error("Proxy Host 3 is still there")
	}
	list, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list.GetProxyHosts() {
		if p.GetId() == 3 {
			t.Error("ListProxyHosts still lists Proxy Host 3 after its delete")
		}
	}
}

func TestADomainNameAnotherHostHoldsIsTheTypedError(t *testing.T) {
	for _, tc := range []struct {
		name, domain string
		edit         func(*npmfake.NPM)
	}{
		{"a Proxy Host", "Grafana.home.example", func(*npmfake.NPM) {}},
		{"a Redirection Host", "old-wiki.home.example", func(f *npmfake.NPM) { f.AddRedirectionHost("old-wiki.home.example") }},
		{"a 404 Host", "gone.home.example", func(f *npmfake.NPM) { f.AddDeadHost("gone.home.example") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := npm(t, "baseline")
			tc.edit(f.NPM)
			_, c := start(t, f.URL)
			fields := wiki()
			fields.DomainNames = []string{tc.domain}

			_, createErr := c.CreateProxyHost(t.Context(), &pb.CreateProxyHostRequest{Fields: fields})
			_, updateErr := c.UpdateProxyHost(t.Context(), &pb.UpdateProxyHostRequest{Id: 13, Fields: fields})

			for _, err := range []error{createErr, updateErr} {
				want := "Nginx Proxy Manager refused it: " + tc.domain + " is already in use"
				if status.Code(err) != codes.AlreadyExists || status.Convert(err).Message() != want {
					t.Errorf("error = %v, want AlreadyExists: %s", err, want)
				}
				if r, meta := reason(err); r != "ERROR_REASON_DOMAIN_IN_USE" || meta["domain_name"] != tc.domain {
					t.Errorf("reason %s %v, want ERROR_REASON_DOMAIN_IN_USE naming %s", r, meta, tc.domain)
				}
			}
			if f.ProxyHost(39) != nil {
				t.Error("a Proxy Host was created")
			}
		})
	}
}

func TestProxyHostsOnViewAreReadOnly(t *testing.T) {
	f := npm(t, "baseline")
	f.EditUser(func(u *npmfake.User) { u.ProxyHosts = "view" })
	h, c := start(t, f.URL)

	if _, err := c.ListProxyHosts(t.Context(), &pb.ListProxyHostsRequest{}); err != nil {
		t.Errorf("ListProxyHosts: %v", err)
	}
	st, err := c.GetStatus(t.Context(), &pb.GetStatusRequest{})
	if err != nil || st.GetStatus() != pb.Status_STATUS_UP || !st.GetProxyHostsReadOnly() {
		t.Errorf("GetStatus = %v (%v), want Up and read-only", st, err)
	}
	if got := h.Health(); got.State != sdk.OK {
		t.Errorf("Health = %v, want ok", got)
	}

	_, createErr := c.CreateProxyHost(t.Context(), &pb.CreateProxyHostRequest{Fields: wiki()})
	_, updateErr := c.UpdateProxyHost(t.Context(), &pb.UpdateProxyHostRequest{Id: 3, Fields: wiki()})
	_, deleteErr := c.DeleteProxyHost(t.Context(), &pb.DeleteProxyHostRequest{Id: 3})

	for _, err := range []error{createErr, updateErr, deleteErr} {
		want := "ddash's user can only view Proxy Hosts: set them to Manage to change them"
		if status.Code(err) != codes.PermissionDenied || status.Convert(err).Message() != want {
			t.Errorf("error = %v, want PermissionDenied: %s", err, want)
		}
		if r, _ := reason(err); r != "ERROR_REASON_PROXY_HOSTS_READ_ONLY" {
			t.Errorf("reason %q, want ERROR_REASON_PROXY_HOSTS_READ_ONLY", r)
		}
	}
	if w := writes(f.Log()); len(w) != 0 {
		t.Errorf("sent %v while read-only", w)
	}
}

func TestAProxyHostDdashCantSaveIsInvalidArgument(t *testing.T) {
	f := npm(t, "baseline")
	_, c := start(t, f.URL)
	for _, tc := range []struct {
		name string
		edit func(*pb.ProxyHostFields)
	}{
		{"no domain name", func(p *pb.ProxyHostFields) { p.DomainNames = nil }},
		{"an empty domain name", func(p *pb.ProxyHostFields) { p.DomainNames = []string{""} }},
		{"a scheme other than http or https", func(p *pb.ProxyHostFields) { p.ForwardScheme = "ftp" }},
		{"no forward host", func(p *pb.ProxyHostFields) { p.ForwardHost = "" }},
		{"port 0", func(p *pb.ProxyHostFields) { p.ForwardPort = 0 }},
		{"port 65536", func(p *pb.ProxyHostFields) { p.ForwardPort = 65536 }},
		{"a negative certificate id", func(p *pb.ProxyHostFields) { p.CertificateId = -1 }},
	} {
		fields := wiki()
		tc.edit(fields)
		if _, err := c.CreateProxyHost(t.Context(), &pb.CreateProxyHostRequest{Fields: fields}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: CreateProxyHost error = %v, want InvalidArgument", tc.name, err)
		}
	}
	if w := writes(f.Log()); len(w) != 0 {
		t.Errorf("sent %v", w)
	}
}
