package apps_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/itsKontra/ddash/components/apps/appspb"
	"github.com/itsKontra/ddash/components/pfsense-api/pfsensefake"
)

// fixAction is the App Action running fix on the App id.
func fixAction(id string, fix *appspb.Fix) *appspb.AppAction {
	return &appspb.AppAction{Action: &appspb.AppAction_Fix{Fix: &appspb.FixWarning{Id: id, Fix: fix}}}
}

// warningOf is the App's Warning of kind; nil without one.
func warningOf(a *appspb.App, kind appspb.WarningKind) *appspb.Warning {
	for _, w := range a.GetWarnings() {
		if w.GetKind() == kind {
			return w
		}
	}
	return nil
}

// runFix runs the fix of kind the App's Warning of kind offers (the first
// such), checking its Plan's steps, and returns the pfSense writes it made.
func (l *lab) runFix(id string, kind appspb.WarningKind, fix appspb.FixKind, steps ...string) []string {
	l.t.Helper()
	w := warningOf(l.app(id), kind)
	i := slices.IndexFunc(w.GetFixes(), func(f *appspb.Fix) bool { return f.GetKind() == fix })
	if i < 0 {
		l.t.Fatalf("%s's %v offers no %v: %v", id, kind, fix, w)
	}
	action := fixAction(id, w.GetFixes()[i])
	if got := titles(l.plan(action)); !slices.Equal(got, steps) {
		l.t.Errorf("steps = %q, want %q", got, steps)
	}
	fwLog := len(l.fw.Log())
	if resp := l.run(action); resp.GetDone() != int32(len(steps)) || resp.GetAppId() != id {
		l.t.Errorf("RunAction = %v, want %d steps done on %s", resp, len(steps), id)
	}
	return writes(l.fw.Log(), fwLog)
}

// Register adds a proxied App's hostname to the Reverse Proxy's Host
// Override, which clears Not registered.
func TestRegisterFixesNotRegistered(t *testing.T) {
	l := newLab(t, labOptions{})
	l.npm.AddProxyHost(proxyHost("http://198.18.2.11:3000", "wiki.home.example"))
	l.refresh()
	const id = "proxy:wiki.home.example"

	w := l.runFix(id, notRegisteredKind, appspb.FixKind_FIX_KIND_REGISTER, "SYSTEM_PFSENSE: Add alias wiki.home.example to nginx.home.example")
	if d := diff([]string{pfAddAlias, pfApply, pfsensefake.AppliedDNS}, w); d != "" {
		t.Errorf("pfSense writes (-want +got):\n%s", d)
	}
	if warningOf(l.app(id), notRegisteredKind) != nil {
		t.Error("Not registered is left")
	}
}

// Remove the name removes an alias at the Reverse Proxy's address no Proxy
// Host serves: its App goes with it.
func TestRemoveTheNameFixesNoProxyHost(t *testing.T) {
	l := newLab(t, labOptions{})
	l.register("ghost.home.example")
	l.refresh()
	id := l.byURL("https://ghost.home.example").GetId()

	w := l.runFix(id, noProxyHost, appspb.FixKind_FIX_KIND_REMOVE_NAME, "SYSTEM_PFSENSE: Remove alias ghost.home.example from nginx.home.example")
	if d := diff([]string{pfRemoveAlias, pfApply, pfsensefake.AppliedDNS}, w); d != "" {
		t.Errorf("pfSense writes (-want +got):\n%s", d)
	}
	if l.has(id) || len(l.dnsOf("ghost.home.example")) != 0 {
		t.Error("ghost.home.example is left")
	}
}

// offline makes Proxy Host id Offline with nginx's error, as a save nginx
// rejected leaves it.
func (l *lab) offline(id int, nginxErr string) {
	l.npm.EditProxyHost(id, func(row map[string]any) {
		row["meta"] = map[string]any{"letsencrypt_agree": false, "dns_challenge": false, "nginx_online": false, "nginx_err": nginxErr}
	})
	l.refresh()
}

// Save again sends an Offline Proxy Host again: once nginx takes it, the
// Warning clears; while nginx still rejects it, the Warning stays with
// nginx's new error.
func TestSaveAgainFixesAnOfflineProxyHost(t *testing.T) {
	const id = "proxy:grafana.home.example"
	const offline = appspb.WarningKind_WARNING_KIND_PROXY_HOST_OFFLINE

	t.Run("nginx takes it", func(t *testing.T) {
		l := newLab(t, labOptions{})
		l.offline(3, "nginx: [emerg] host not found in upstream")
		npmLog := len(l.npm.Log())
		l.runFix(id, offline, appspb.FixKind_FIX_KIND_SAVE_AGAIN, "SYSTEM_NGINX_PROXY_MANAGER: Save Proxy Host grafana.home.example again")
		if d := diff([]string{"PUT /api/nginx/proxy-hosts/3"}, writes(l.npm.Log(), npmLog)); d != "" {
			t.Errorf("Nginx Proxy Manager writes (-want +got):\n%s", d)
		}
		if warningOf(l.app(id), offline) != nil {
			t.Error("Proxy Host offline is left")
		}
	})
	t.Run("nginx still rejects it", func(t *testing.T) {
		l := newLab(t, labOptions{})
		l.offline(3, "nginx: [emerg] host not found in upstream")
		action := fixAction(id, warningOf(l.app(id), offline).GetFixes()[0])
		p := l.plan(action)
		l.npm.RejectNextSave("nginx: [emerg] no resolver defined")
		_, err := l.tryRun(action, p)
		if err == nil || !strings.HasPrefix(err.message, "Stopped at step 1 of 1, Save Proxy Host grafana.home.example again: ") || !strings.HasSuffix(err.message, "Nothing was rolled back.") {
			t.Errorf("RunAction = %v, want it stopped at its step", err)
		}
		if w := warningOf(l.app(id), offline); w.GetDetail() != "nginx: [emerg] no resolver defined" {
			t.Errorf("Proxy host offline = %v, want it with nginx's new error", w)
		}
	})
}

// dnsBypass is a lab where a Proxy Host serves nas.home.example, whose
// Host Override points at the NAS: the DNS bypasses the Reverse Proxy.
func dnsBypass(t *testing.T) (*lab, string) {
	l := newLab(t, labOptions{})
	l.npm.AddProxyHost(proxyHost("http://198.18.2.10:5000", "nas.home.example"))
	l.refresh()
	return l, l.byURL("https://nas.home.example").GetId()
}

// Point at Reverse Proxy replaces the hostname's Host Override with an
// alias of the Reverse Proxy's Host Override; Delete Proxy Host deletes the
// Proxy Host. Either clears "DNS bypasses the Reverse Proxy".
func TestTheDNSBypassFixes(t *testing.T) {
	t.Run("point at the Reverse Proxy", func(t *testing.T) {
		l, nas := dnsBypass(t)
		w := l.runFix(nas, dnsBypasses, appspb.FixKind_FIX_KIND_POINT_AT_REVERSE_PROXY,
			"SYSTEM_PFSENSE: Delete Host Override nas.home.example",
			"SYSTEM_PFSENSE: Add alias nas.home.example to nginx.home.example",
		)
		if d := diff([]string{pfDelete, pfAddAlias, pfApply, pfsensefake.AppliedDNS}, w); d != "" {
			t.Errorf("pfSense writes (-want +got):\n%s", d)
		}
		if a := l.app(nas); a.GetReach() != appspb.Reach_REACH_PROXIED || warningOf(a, dnsBypasses) != nil {
			t.Errorf("NAS = %v, want it proxied, the Warning gone", a)
		}
	})
	t.Run("delete the Proxy Host", func(t *testing.T) {
		l, nas := dnsBypass(t)
		host := l.app(nas).GetLinks().GetProxyHostId()
		npmLog := len(l.npm.Log())
		l.runFix(nas, dnsBypasses, appspb.FixKind_FIX_KIND_DELETE_PROXY_HOST, "SYSTEM_NGINX_PROXY_MANAGER: Delete Proxy Host nas.home.example")
		if w := writes(l.npm.Log(), npmLog); len(w) != 1 || !strings.HasPrefix(w[0], "DELETE /api/nginx/proxy-hosts/") || l.npm.ProxyHost(int(host)) != nil {
			t.Errorf("Nginx Proxy Manager writes = %q, want Proxy Host %d deleted", w, host)
		}
		if a := l.app(nas); a.GetReach() != appspb.Reach_REACH_NOT_PROXIED || len(a.GetWarnings()) != 0 {
			t.Errorf("NAS = %v, want it Not proxied without Warnings", a)
		}
	})
}

// Registered twice offers to delete either entry; deleting one clears it.
func TestDeleteAnEntryFixesRegisteredTwice(t *testing.T) {
	setUp := func(t *testing.T) *lab {
		l := newLab(t, labOptions{})
		l.addOverride("grafana", "198.18.1.204")
		l.refresh()
		return l
	}
	const id = "proxy:grafana.home.example"
	t.Run("the Host Override", func(t *testing.T) {
		l := setUp(t)
		w := warningOf(l.app(id), registeredTwice)
		i := slices.IndexFunc(w.GetFixes(), func(f *appspb.Fix) bool { return f.GetHostOverride().GetParent() == nil })
		action := fixAction(id, w.GetFixes()[i])
		if got := titles(l.plan(action)); !slices.Equal(got, []string{"SYSTEM_PFSENSE: Delete Host Override grafana.home.example"}) {
			t.Errorf("steps = %q", got)
		}
		l.run(action)
		if a := l.app(id); a.GetReach() != appspb.Reach_REACH_PROXIED || warningOf(a, registeredTwice) != nil {
			t.Errorf("Grafana = %v, want it proxied, the Warning gone", a)
		}
	})
	t.Run("the alias", func(t *testing.T) {
		l := setUp(t)
		w := warningOf(l.app(id), registeredTwice)
		i := slices.IndexFunc(w.GetFixes(), func(f *appspb.Fix) bool { return f.GetHostOverride().GetParent() != nil })
		action := fixAction(id, w.GetFixes()[i])
		if got := titles(l.plan(action)); !slices.Equal(got, []string{"SYSTEM_PFSENSE: Remove alias grafana.home.example from nginx.home.example"}) {
			t.Errorf("steps = %q", got)
		}
		l.run(action)
		if a := l.app(id); warningOf(a, registeredTwice) != nil {
			t.Errorf("Grafana = %v, want Registered twice gone", a)
		}
	})
}

// A fix the App's Warnings don't offer, or one that changes ddash alone,
// doesn't plan.
func TestOnlyAnOfferedExternalFixPlans(t *testing.T) {
	l := newLab(t, labOptions{})
	const id = "proxy:grafana.home.example"
	if p := l.plan(fixAction(id, &appspb.Fix{Kind: appspb.FixKind_FIX_KIND_REGISTER, Hostname: "grafana.home.example"})); !blockedBy(p, "Grafana's Warnings don't offer that fix now.") || len(p.GetSteps()) != 0 {
		t.Errorf("Plan = %v, want it blocked", p)
	}
}

// Point at Reverse Proxy moves only the App's names: a bypassing alias
// goes alone, and a Host Override with an alias another App has too isn't
// deleted from under it.
func TestPointAtReverseProxyMovesOnlyTheAppsNames(t *testing.T) {
	t.Run("an alias", func(t *testing.T) {
		l := newLab(t, labOptions{})
		l.addOverride("storage", "198.18.2.12", "files", "media")
		l.npm.AddProxyHost(proxyHost("http://198.18.2.12:8080", "files.home.example"))
		l.refresh()
		files := l.byURL("https://files.home.example").GetId()

		l.runFix(files, dnsBypasses, appspb.FixKind_FIX_KIND_POINT_AT_REVERSE_PROXY,
			"SYSTEM_PFSENSE: Remove alias files.home.example from storage.home.example",
			"SYSTEM_PFSENSE: Add alias files.home.example to nginx.home.example",
		)
		if got := l.dnsOf("media.home.example"); len(got) != 1 {
			t.Errorf("media.home.example = %q, want it left as storage.home.example's alias", got)
		}
	})
	t.Run("a Host Override with another App's name", func(t *testing.T) {
		l, nas := dnsBypass(t)
		l.editDNS(func(all []pfsensefake.HostOverride) []pfsensefake.HostOverride {
			for i := range all {
				if all[i].Host == "nas" {
					all[i].Aliases = append(all[i].Aliases, pfsensefake.HostOverrideAlias{Host: "files", Domain: "home.example"})
				}
			}
			return all
		})
		l.npm.AddProxyHost(proxyHost("http://198.18.2.10:8080", "files.home.example"))
		l.refresh()
		w := warningOf(l.app(nas), dnsBypasses)
		i := slices.IndexFunc(w.GetFixes(), func(f *appspb.Fix) bool { return f.GetKind() == appspb.FixKind_FIX_KIND_POINT_AT_REVERSE_PROXY })

		p := l.plan(fixAction(nas, w.GetFixes()[i]))
		want := "Pointing nas.home.example at the Reverse Proxy deletes its Host Override, and with it files.home.example, which isn't only " + l.app(nas).GetName() + "'s: remove or move it in pfSense first."
		if !blockedBy(p, want) {
			t.Errorf("Plan = %v, want it blocked: %q", p, want)
		}
	})
}

// Delete Proxy Host leaves the Proxy Host's names that are aliases at the
// Reverse Proxy's address registered: its Plan says so.
func TestDeleteProxyHostSaysWhichNamesStayRegistered(t *testing.T) {
	l := newLab(t, labOptions{})
	l.npm.AddProxyHost(proxyHost("http://198.18.2.10:5000", "nas.home.example", "nasui.home.example"))
	l.register("nasui.home.example")
	l.refresh()
	nas := l.byURL("https://nas.home.example").GetId()
	w := warningOf(l.app(nas), dnsBypasses)
	i := slices.IndexFunc(w.GetFixes(), func(f *appspb.Fix) bool { return f.GetKind() == appspb.FixKind_FIX_KIND_DELETE_PROXY_HOST })

	p := l.plan(fixAction(nas, w.GetFixes()[i]))
	want := []string{"nasui.home.example stays an alias of the Reverse Proxy's Host Override, with no Proxy Host to serve it: remove it there, or add a reverse proxy again."}
	if d := diff(want, p.GetNotes()); d != "" || len(p.GetBlocked()) != 0 {
		t.Errorf("Plan's notes (-want +got):\n%s\nblocked: %q", d, p.GetBlocked())
	}
}
