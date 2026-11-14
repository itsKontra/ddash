package hostagentapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/agentfake"
	hostagentapi "github.com/itsKontra/ddash/components/host-agent-api"
	pb "github.com/itsKontra/ddash/components/host-agent-api/hostagentapipb"
	jobpb "github.com/itsKontra/ddash/proto/ddash/job"
	"github.com/itsKontra/ddash/sdk/sdktest"
)

const laptop = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGq0JZ6bC2b3m9Hcm6bE0u8bC4S0kq6yXH2b2Z4vX7aL laptop"

func fakeHost(t *testing.T, seed agentfake.HostSeed, release string) (*agentfake.Host, string) {
	t.Helper()
	fake, err := agentfake.NewHost(t.TempDir(), seed, agentfake.Options{StateDir: t.TempDir(), Release: release})
	if err != nil {
		t.Fatal(err)
	}
	u, stop, err := fake.Serve("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return fake, u
}

func start(t *testing.T) pb.HostAgentApiServiceClient {
	t.Helper()
	h := sdktest.New(t, hostagentapi.Definition())
	h.MustStart(&pb.HostAgentApiConfig{})
	return pb.NewHostAgentApiServiceClient(h.Conn)
}

func TestAddPairReadAndUpgrade(t *testing.T) {
	ctx := context.Background()
	fake, u := fakeHost(t, agentfake.HostSeed{
		Hostname: "docker-host", OS: "debian", SSHInstalled: true, SSHRunning: true, PasswordAuth: true,
		Pending: []agent.Package{{Name: "libc6", Current: "2.36-9", New: "2.36-9+deb12u9"}, {Name: "curl", Current: "7.88.1-10", New: "7.88.1-10+deb12u8"}},
	}, "dev")
	c := start(t)

	added, err := c.AddAgent(ctx, &pb.AddAgentRequest{Vmid: 201, Name: "docker-host", Url: u})
	if err != nil {
		t.Fatal(err)
	}
	s := added.GetSetup()
	want := "curl -fsSL " + sdktest.PublicURL + "/agent/host/install.sh | sudo sh -s -- --code " + s.GetEnrolmentCode()
	if s.GetEnrolmentCode() == "" || s.GetInstallCommand() != want || s.GetChecksumsUrl() != sdktest.PublicURL+"/agent/host/SHA256SUMS" {
		t.Fatalf("setup = %v", s)
	}
	if _, err := c.AddAgent(ctx, &pb.AddAgentRequest{Vmid: 201, Url: u}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("same VMID again: %v", err)
	}
	if _, err := c.AddAgent(ctx, &pb.AddAgentRequest{Vmid: 202, Url: "http://198.18.2.10:7444"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("http URL: %v", err)
	}

	paired, err := c.PairAgent(ctx, &pb.PairAgentRequest{Vmid: 201})
	if err != nil {
		t.Fatal(err)
	}
	a := paired.GetAgent()
	if a.GetState() != pb.AgentState_AGENT_STATE_PAIRED || a.GetFingerprint() != fake.Fingerprint() || a.GetOs().GetPackageManager() != "apt" || a.GetHostname() != "docker-host" {
		t.Fatalf("paired = %v", a)
	}
	if s, _ := c.GetAgentSetup(ctx, &pb.GetAgentSetupRequest{Vmid: 201}); s.GetSetup().GetInstallCommand() != "" {
		t.Fatal("a paired Agent's setup still shows its install command")
	}

	pkgs := waitPackages(t, c, 201, 2)
	if pkgs.GetPackages()[0].GetName() != "libc6" {
		t.Fatalf("packages = %v", pkgs)
	}

	up, err := c.UpgradePackages(ctx, &pb.UpgradePackagesRequest{Vmid: 201})
	if err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, c, 201, up.GetJob().GetId())
	if j.GetState() != jobpb.State_STATE_DONE || j.GetResult()["reboot_required"] != "true" || j.GetAgent() != "201" {
		t.Fatalf("upgrade job = %v", j)
	}
	if _, err := c.CollectJob(ctx, &pb.CollectJobRequest{Vmid: 201, Id: j.GetId()}); err != nil {
		t.Fatal(err)
	}
	list, _ := c.ListAgents(ctx, &pb.ListAgentsRequest{})
	if !list.GetAgents()[0].GetOs().GetRebootRequired() {
		t.Error("after a libc6 update the Agent doesn't say reboot required")
	}

	// A failed job's collect fails with its error, for the Audit Log.
	fake.FailNext("apt-get", "E: Could not get lock /var/lib/dpkg/lock-frontend")
	up, err = c.UpgradePackages(ctx, &pb.UpgradePackagesRequest{Vmid: 201})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, c, 201, up.GetJob().GetId())
	if _, err := c.CollectJob(ctx, &pb.CollectJobRequest{Vmid: 201, Id: up.GetJob().GetId()}); status.Code(err) != codes.Aborted {
		t.Fatalf("collecting a failed job: %v", err)
	}

	removed, err := c.RemoveAgent(ctx, &pb.RemoveAgentRequest{Vmid: 201})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(removed.GetUninstall()[0], "--uninstall") {
		t.Errorf("uninstall = %v", removed.GetUninstall())
	}
}

func TestSSHKeysAndSettingsAreRefusedByTheAgent(t *testing.T) {
	ctx := context.Background()
	_, u := fakeHost(t, agentfake.HostSeed{OS: "ubuntu", SSHInstalled: true, SSHRunning: true, PasswordAuth: true}, "dev")
	c := start(t)
	if _, err := c.AddAgent(ctx, &pb.AddAgentRequest{Vmid: 112, Url: u}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PairAgent(ctx, &pb.PairAgentRequest{Vmid: 112}); err != nil {
		t.Fatal(err)
	}

	// No key yet: turning password login off would lock everyone out.
	_, err := c.SetSSHSettings(ctx, &pb.SetSSHSettingsRequest{Vmid: 112, PasswordAuthentication: false, PermitRootLogin: "prohibit-password"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("password off without a key: %v", err)
	}

	if _, err := c.InstallKey(ctx, &pb.InstallKeyRequest{Vmid: 112, User: "ops", Name: "laptop", PublicKey: laptop}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InstallKey(ctx, &pb.InstallKeyRequest{Vmid: 112, User: "ops", Name: "laptop", PublicKey: laptop}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("the same key again: %v", err)
	}
	set, err := c.SetSSHSettings(ctx, &pb.SetSSHSettingsRequest{Vmid: 112, PasswordAuthentication: false, PermitRootLogin: "no"})
	if err != nil {
		t.Fatal(err)
	}
	if set.GetSsh().GetPasswordAuthentication() || set.GetSsh().GetPermitRootLogin() != "no" {
		t.Fatalf("settings = %v", set.GetSsh())
	}
	got, err := c.GetSSH(ctx, &pb.GetSSHRequest{Vmid: 112})
	if err != nil {
		t.Fatal(err)
	}
	if k := got.GetSsh().GetKeys(); len(k) != 1 || k[0].GetName() != "laptop" || k[0].GetUser() != "ops" {
		t.Fatalf("keys = %v", k)
	}
	// The last key with password login off: refused.
	if _, err := c.RemoveKey(ctx, &pb.RemoveKeyRequest{Vmid: 112, User: "ops", Name: "laptop"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("removing the last way in: %v", err)
	}
}

func TestOutdatedAgentAllowsOnlyUpdate(t *testing.T) {
	ctx := context.Background()
	_, u := fakeHost(t, agentfake.HostSeed{OS: "alpine"}, "v0.0.1")
	sum := strings.Repeat("ab", 32)
	hostagentapi.ServeDownloads(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != sdktest.PublicURL+"/agent/host/SHA256SUMS" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("cd", 32) + "  ddash-host-agent-linux-arm64\n" + sum + "  ddash-host-agent-linux-amd64\n"))
	}))
	c := start(t)
	if _, err := c.AddAgent(ctx, &pb.AddAgentRequest{Vmid: 203, Url: u}); err != nil {
		t.Fatal(err)
	}
	a, err := c.PairAgent(ctx, &pb.PairAgentRequest{Vmid: 203})
	if err != nil {
		t.Fatal(err)
	}
	if a.GetAgent().GetState() != pb.AgentState_AGENT_STATE_OUTDATED {
		t.Fatalf("agent = %v", a)
	}
	if _, err := c.UpgradePackages(ctx, &pb.UpgradePackagesRequest{Vmid: 203}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("an outdated Agent's upgrade: %v", err)
	}
	up, err := c.UpdateAgent(ctx, &pb.UpdateAgentRequest{Vmid: 203})
	if err != nil {
		t.Fatal(err)
	}
	if up.GetJob().GetOp() != "agent.update" {
		t.Fatalf("update job = %v", up.GetJob())
	}
	b, _ := json.Marshal(up.GetJob().GetTitle())
	if !strings.Contains(string(b), "dev") {
		t.Errorf("the update job doesn't name the Release: %s", b)
	}
}

func waitPackages(t *testing.T, c pb.HostAgentApiServiceClient, vmid uint32, n int) *pb.HostPackages {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		res, err := c.ListPackages(context.Background(), &pb.ListPackagesRequest{})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range res.GetHosts() {
			if h.GetVmid() == vmid && len(h.GetPackages()) == n {
				return h
			}
		}
	}
	t.Fatalf("%d never listed %d packages", vmid, n)
	return nil
}

func waitJob(t *testing.T, c pb.HostAgentApiServiceClient, vmid uint32, id string) *jobpb.Job {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		res, err := c.ListJobs(context.Background(), &pb.ListJobsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range res.GetHosts() {
			for _, j := range h.GetJobs() {
				if h.GetVmid() == vmid && j.GetId() == id && j.GetEnd() != nil {
					return j
				}
			}
		}
	}
	t.Fatalf("job %s didn't end", id)
	return nil
}
