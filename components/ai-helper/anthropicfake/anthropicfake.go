// Package anthropicfake is a fake of Anthropic's Messages API for
// ai-helper's tests and fakelab: POST /v1/messages answers every request
// with a canned draft (draft.json in a scenario's directory) as the
// structured-output text block, records each request, and refuses a key
// other than its own with 401 as Anthropic does.
package anthropicfake

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// DefaultKey is the API key the fake accepts when Options leave it empty.
const DefaultKey = "sk-ant-fakelab-0000000000000000"

// Options shape an API.
type Options struct {
	// Key is the API key accepted; empty for DefaultKey.
	Key string
}

// Request is one Messages API request the fake got.
type Request struct {
	// Header holds the request's headers (x-api-key, anthropic-beta…).
	Header http.Header
	// Body is the request body, decoded.
	Body map[string]any
}

// API is the fake Messages API.
type API struct {
	key   string
	draft []byte

	mu       sync.Mutex
	requests []Request
	// fails are the status codes the next requests are answered with.
	fails []int
	stop  string
}

// New serves the draft in dir/draft.json.
func New(dir string, o Options) (*API, error) {
	b, err := os.ReadFile(filepath.Join(dir, "draft.json"))
	if err != nil {
		return nil, err
	}
	var check map[string]any
	if err := json.Unmarshal(b, &check); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, "draft.json"), err)
	}
	key := o.Key
	if key == "" {
		key = DefaultKey
	}
	return &API{key: key, draft: b}, nil
}

// Requests returns every request received, in order.
func (a *API) Requests() []Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Request(nil), a.requests...)
}

// FailNext answers the next request not yet failing with an API error
// of status code.
func (a *API) FailNext(code int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fails = append(a.fails, code)
}

// StopNext ends the next answer with stop_reason, e.g. "refusal".
func (a *API) StopNext(reason string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stop = reason
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
		apiError(w, http.StatusNotFound, "not_found_error", "Not found")
		return
	}
	var body map[string]any
	b, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(b, &body); err != nil {
		apiError(w, http.StatusBadRequest, "invalid_request_error", "body: "+err.Error())
		return
	}
	a.mu.Lock()
	a.requests = append(a.requests, Request{Header: r.Header.Clone(), Body: body})
	code, stop := 0, a.stop
	if len(a.fails) > 0 {
		code, a.fails = a.fails[0], a.fails[1:]
	}
	a.stop = ""
	a.mu.Unlock()

	switch {
	case r.Header.Get("x-api-key") != a.key && r.Header.Get("Authorization") != "Bearer "+a.key:
		apiError(w, http.StatusUnauthorized, "authentication_error", "invalid x-api-key")
		return
	case code != 0:
		apiError(w, code, "api_error", http.StatusText(code))
		return
	}
	text := string(a.draft)
	if stop == "" {
		stop = "end_turn"
	}
	if stop == "refusal" {
		text = ""
	}
	model, _ := body["model"].(string)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "msg_fake", "type": "message", "role": "assistant", "model": model,
		"content":     []map[string]any{{"type": "text", "text": text}},
		"stop_reason": stop, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 900, "output_tokens": 700},
	})
}

func apiError(w http.ResponseWriter, code int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": msg}})
}
