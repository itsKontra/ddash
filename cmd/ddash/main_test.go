package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHealthcheckPassesOnlyWhenHealthzIsOK(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer ok.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "core DB: closed", http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()

	if err := healthcheck(t.Context(), ok.URL); err != nil {
		t.Errorf("healthy: %v", err)
	}
	if err := healthcheck(t.Context(), failing.URL); err == nil {
		t.Error("503: passed, want an error")
	}
	if err := healthcheck(t.Context(), gone.URL); err == nil {
		t.Error("nothing listening: passed, want an error")
	}
}

func TestServeAnswersHealthzUntilStoppedThenStopsAcceptingHTTP(t *testing.T) {
	release := t.TempDir()
	if err := os.WriteFile(filepath.Join(release, "catalogue.json"), []byte(`{"release":"dev"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := signInEnv()
	env["DDASH_LISTEN"] = "127.0.0.1:0"
	env["DDASH_DATA_DIR"] = t.TempDir()
	env["DDASH_RELEASE_DIR"] = release
	ctx, sigterm := context.WithCancel(t.Context())
	addrs := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, func(k string) string { return env[k] }, func(addr string) { addrs <- addr })
	}()
	url := "http://" + <-addrs

	if err := healthcheck(t.Context(), url); err != nil {
		t.Errorf("while serving: %v", err)
	}
	sigterm()
	if err := <-done; err != nil {
		t.Errorf("serve: %v", err)
	}
	if err := healthcheck(t.Context(), url); err == nil {
		t.Error("after SIGTERM: healthz still answers")
	}
}

// signInEnv is ddash.env's sign-in config, with an issuer nothing answers
// at: ddash starts without reaching Keycloak.
func signInEnv() map[string]string {
	return map[string]string{
		"DDASH_PUBLIC_URL":         "https://ddash.home.example",
		"DDASH_OIDC_ISSUER":        "https://127.0.0.1:1/realms/home",
		"DDASH_OIDC_CLIENT_ID":     "ddash",
		"DDASH_OIDC_CLIENT_SECRET": "secret",
		"DDASH_TRUSTED_PROXY":      "198.18.0.10",
	}
}

func TestServeRefusesToStartWithoutItsSignInConfig(t *testing.T) {
	release := t.TempDir()
	if err := os.WriteFile(filepath.Join(release, "catalogue.json"), []byte(`{"release":"dev"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(env map[string]string) error {
		env["DDASH_LISTEN"] = "127.0.0.1:0"
		env["DDASH_DATA_DIR"] = t.TempDir()
		env["DDASH_RELEASE_DIR"] = release
		return serve(t.Context(), func(k string) string { return env[k] }, func(string) { t.Error("serving") })
	}

	for _, missing := range []string{"DDASH_PUBLIC_URL", "DDASH_OIDC_ISSUER", "DDASH_OIDC_CLIENT_ID", "DDASH_OIDC_CLIENT_SECRET"} {
		env := signInEnv()
		delete(env, missing)
		if err := run(env); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("without %s: %v, want an error naming it", missing, err)
		}
	}
	env := signInEnv()
	env["DDASH_TRUSTED_PROXY"] = "nginx"
	if err := run(env); err == nil || !strings.Contains(err.Error(), "nginx") {
		t.Errorf("a hostname as trusted proxy: %v, want an error", err)
	}
}
