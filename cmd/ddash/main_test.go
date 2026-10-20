package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	env := map[string]string{
		"DDASH_LISTEN":      "127.0.0.1:0",
		"DDASH_DATA_DIR":    t.TempDir(),
		"DDASH_RELEASE_DIR": release,
	}
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
