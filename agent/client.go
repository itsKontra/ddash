package agent

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/itsKontra/ddash/sdk/jsoncall"
)

// Client reaches one paired Agent: it sends the token and refuses a
// certificate other than the pinned one.
type Client struct {
	base  string
	token string
	hc    *http.Client
}

// APIError is an Agent's refusal.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return e.Message }

// ErrFingerprint means the Agent's certificate isn't the one pinned at
// pairing: another program answers at its address, or the Agent's state
// was reset.
var ErrFingerprint = errors.New("the Agent's certificate changed since pairing")

// requestTimeout bounds a call to an Agent. Long operations are jobs, so
// no call waits on one.
const requestTimeout = 20 * time.Second

// Dial returns a client of the Agent at base (https://host:port), sending
// token and pinning fingerprint. Clients of one fingerprint share their
// connections, so dialling per call costs no handshake per call.
func Dial(base, token, fingerprint string) *Client {
	return dial(base, token, shared(fingerprint))
}

// transports holds one transport per pinned fingerprint: one per Agent
// ever paired, since an Agent keeps its certificate.
var transports sync.Map // fingerprint → http.RoundTripper

// shared is the transport pinning fp that every Dial of fp uses. The
// first contact ("" accepts any certificate) gets one of its own.
func shared(fp string) http.RoundTripper {
	if fp == "" {
		return pinned(fp)
	}
	if t, ok := transports.Load(fp); ok {
		return t.(http.RoundTripper)
	}
	t, _ := transports.LoadOrStore(fp, pinned(fp))
	return t.(http.RoundTripper)
}

func dial(base, token string, transport http.RoundTripper) *Client {
	return &Client{base: strings.TrimRight(base, "/"), token: token, hc: &http.Client{Timeout: requestTimeout, Transport: transport}}
}

// pinned is a TLS transport that accepts exactly the certificate whose
// fingerprint is fp ("" accepts any, for the first contact).
func pinned(fp string) http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Verified by fingerprint in VerifyConnection, not by a CA.
		InsecureSkipVerify: true, //nolint:gosec // pinned below
		VerifyConnection: func(cs tls.ConnectionState) error {
			if fp == "" {
				return nil
			}
			if len(cs.PeerCertificates) == 0 {
				return ErrFingerprint
			}
			got := sha256.Sum256(cs.PeerCertificates[0].Raw)
			want, err := hex.DecodeString(fp)
			if err != nil || subtle.ConstantTimeCompare(got[:], want) != 1 {
				return ErrFingerprint
			}
			return nil
		},
	}
	return t
}

// Probe says hello to the Agent at base before pairing, returning what it
// said and its certificate's fingerprint.
func Probe(ctx context.Context, base string) (Hello, string, error) {
	var fp string
	t := pinned("").(*http.Transport)
	t.TLSClientConfig.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) > 0 {
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			fp = hex.EncodeToString(sum[:])
		}
		return nil
	}
	t.DisableKeepAlives = true
	var h Hello
	err := dial(base, "", t).Do(ctx, http.MethodGet, PathHello, nil, nil, &h)
	return h, fp, err
}

// Pair pairs with the unpaired Agent at base whose certificate has
// fingerprint fp, presenting code; it returns the token both now keep.
func Pair(ctx context.Context, base, fp, code string) (string, Hello, error) {
	token := NewToken()
	var h Hello
	err := Dial(base, "", fp).Do(ctx, http.MethodPost, PathPair, nil, PairRequest{Code: code, Token: token}, &h)
	return token, h, err
}

// Get reads path (with query) into out.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.Do(ctx, http.MethodGet, path, query, nil, out)
}

// Post sends in to path and reads the answer into out (nil: ignored).
func (c *Client) Post(ctx context.Context, path string, in, out any) error {
	return c.Do(ctx, http.MethodPost, path, nil, in, out)
}

// Delete sends in (may be nil) to path with DELETE.
func (c *Client) Delete(ctx context.Context, path string, in, out any) error {
	return c.Do(ctx, http.MethodDelete, path, nil, in, out)
}

// Do makes one call.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	header := http.Header{}
	if c.token != "" {
		header.Set(TokenHeader, "Bearer "+c.token)
	}
	reply, err := jsoncall.Do(ctx, c.hc, jsoncall.Request{Method: method, URL: u, Header: header, Body: in})
	if err != nil {
		if errors.Is(err, ErrFingerprint) {
			return ErrFingerprint
		}
		// As net/http and encoding/json report it: callers match on it.
		var call *jsoncall.Error
		if errors.As(err, &call) {
			return call.Err
		}
		return err
	}
	b := reply.Body
	if reply.Status/100 != 2 {
		var e Error
		if json.Unmarshal(b, &e) != nil || e.Message == "" {
			e = Error{Code: CodeFailed, Message: fmt.Sprintf("the Agent answered %d %s", reply.Status, http.StatusText(reply.Status))}
		}
		return &APIError{Status: reply.Status, Code: e.Code, Message: e.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("the Agent's answer: %w", err)
	}
	return nil
}

// CodeOf is err's Agent error code, or "" when it isn't one.
func CodeOf(err error) string {
	var e *APIError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}
