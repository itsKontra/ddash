package main

import (
	"bufio"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/itsKontra/ddash/core/runtime"
)

// The reference deployment in deploy/ runs this binary in its image. These
// tests keep it in step with what the binary needs.

const (
	composeFile = "../../deploy/compose.yaml"
	envExample  = "../../deploy/ddash.env.example"
)

// placeholder is how deploy/ marks a value the Operator fills in.
var placeholder = regexp.MustCompile(`<[a-z][a-z0-9-]*>`)

func TestTheDdashEnvExampleHoldsEveryBootstrapVariableAsAPlaceholder(t *testing.T) {
	env := readEnvFile(t, envExample)

	keys := slices.Sorted(maps.Keys(env))
	want := []string{
		"DDASH_GITHUB_TOKEN", "DDASH_OIDC_CLIENT_ID", "DDASH_OIDC_CLIENT_SECRET",
		"DDASH_OIDC_ISSUER", "DDASH_PUBLIC_URL", "DDASH_TRUSTED_PROXY",
	}
	if !slices.Equal(keys, want) {
		t.Errorf("variables %v, want the bootstrap ones %v", keys, want)
	}
	for k, v := range env {
		if !placeholder.MatchString(v) {
			t.Errorf("%s=%s: want a <placeholder>, never a real value", k, v)
		}
	}
	if _, err := signInConfig(func(k string) string { return env[k] }); err != nil {
		t.Errorf("the example doesn't configure sign-in: %v", err)
	}
	if !strings.HasPrefix(env["DDASH_PUBLIC_URL"], "https://") {
		t.Errorf("DDASH_PUBLIC_URL=%s, want https (sign-in needs it)", env["DDASH_PUBLIC_URL"])
	}
}

// service is the part of a compose service the reference deployment sets.
type service struct {
	Image           string
	Restart         string
	User            string
	Ports           []string
	Sysctls         []string
	EnvFile         string `yaml:"env_file"`
	Volumes         []string
	Healthcheck     struct{ Test []string }
	StopGracePeriod string `yaml:"stop_grace_period"`
}

func TestTheReferenceComposeRunsOnePinnedUnprivilegedService(t *testing.T) {
	b, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatal(err)
	}
	var compose struct{ Services map[string]service }
	if err := yaml.Unmarshal(b, &compose); err != nil {
		t.Fatal(err)
	}
	if len(compose.Services) != 1 {
		t.Fatalf("services %v, want ddash alone", compose.Services)
	}
	s, ok := compose.Services["ddash"]
	if !ok {
		t.Fatalf("services %v, want ddash", compose.Services)
	}

	for _, c := range []struct{ field, got, want string }{
		{"image", s.Image, "ghcr.io/itskontra/ddash:<release>"},
		{"restart", s.Restart, "unless-stopped"},
		{"user", s.User, "65532:65532"},
		{"env_file", s.EnvFile, "ddash.env"},
	} {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.field, c.got, c.want)
		}
	}
	for _, c := range []struct {
		field     string
		got, want []string
	}{
		{"ports", s.Ports, []string{"<ddash-ip>:8080:8080"}},
		{"sysctls", s.Sysctls, []string{"net.ipv4.ping_group_range=0 2147483647"}},
		{"volumes", s.Volumes, []string{"/opt/ddash/data:/data"}},
		{"healthcheck.test", s.Healthcheck.Test, []string{"CMD", "/ddash", "healthcheck"}},
	} {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s: %q, want %q", c.field, c.got, c.want)
		}
	}
	if strings.Contains(string(b), "docker.sock") {
		t.Error("compose mounts the Docker socket; ddash never gets it")
	}
}

func TestComposeGivesTheCoreTimeToStopEveryComponent(t *testing.T) {
	b, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatal(err)
	}
	var compose struct{ Services map[string]service }
	if err := yaml.Unmarshal(b, &compose); err != nil {
		t.Fatal(err)
	}
	grace, err := time.ParseDuration(compose.Services["ddash"].StopGracePeriod)
	if err != nil {
		t.Fatalf("stop_grace_period: %v", err)
	}

	// On SIGTERM the core drains HTTP, then stops every Component.
	if grace != 20*time.Second || httpShutdownTimeout+runtime.StopTimeout >= grace {
		t.Errorf("stop_grace_period %v, want 20s, more than the core's %v + %v", grace, httpShutdownTimeout, runtime.StopTimeout)
	}
}

// readEnvFile reads a compose env_file: KEY=value lines, # comments.
func readEnvFile(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	env := map[string]string{}
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("%s: %q isn't KEY=value", path, line)
		}
		if _, dup := env[k]; dup {
			t.Fatalf("%s: %s set twice", path, k)
		}
		env[k] = v
	}
	if err := lines.Err(); err != nil {
		t.Fatal(err)
	}
	return env
}
