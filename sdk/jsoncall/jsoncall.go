// Package jsoncall makes one JSON call to a system's HTTP API: the part
// every `-api` Component and the Agents' client wrote out for themselves.
// It marshals the body, sends it, and reads the answer up to a cap. What
// the system's status and body mean is the caller's: its auth, its error
// envelope and its wording.
//
// It needs nothing beyond the standard library, so the Agents' client can
// use it too.
package jsoncall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// MaxResponse is how much of an answer Do reads unless the Request says
// otherwise: 16 MiB, well above the largest list a home network's
// system answers (a few MiB), and small enough that a system gone wrong
// can't fill a Component's memory.
const MaxResponse = 16 << 20

// Request is one call.
type Request struct {
	Method string
	URL    string
	// Header is the system's own: its credentials, an Accept. Content-Type
	// is set to application/json when there is a Body.
	Header http.Header
	// Body is marshalled as JSON; nil sends none.
	Body any
	// MaxResponse replaces the package's MaxResponse when a system needs
	// another cap; say why where it is set.
	MaxResponse int64
}

// Reply is what the system answered, whatever its status.
type Reply struct {
	Status int
	Body   []byte
}

// Stage is how far a call that failed got.
type Stage int

const (
	// Build: the request couldn't be made (a body that isn't JSON, a bad
	// URL): a bug in the caller, never the system's doing.
	Build Stage = iota
	// Reach: the system didn't answer.
	Reach
	// Read: its answer broke off, or is larger than the cap.
	Read
)

// Error is why Do has no Reply.
type Error struct {
	Stage Stage
	Err   error
}

func (e *Error) Error() string { return e.Cause().Error() }
func (e *Error) Unwrap() error { return e.Err }

// Cause is Err without net/http's "Get "<url>": " around it, which
// repeats what the caller says anyway.
func (e *Error) Cause() error {
	var uerr *url.Error
	if errors.As(e.Err, &uerr) {
		return uerr.Err
	}
	return e.Err
}

// Do sends r with hc and returns the system's status and body. It fails
// only with an *Error; a status that isn't 2xx is a Reply.
func Do(ctx context.Context, hc *http.Client, r Request) (Reply, error) {
	var body io.Reader
	if r.Body != nil {
		b, err := json.Marshal(r.Body)
		if err != nil {
			return Reply{}, &Error{Build, err}
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		return Reply{}, &Error{Build, err}
	}
	for name, values := range r.Header {
		req.Header[http.CanonicalHeaderKey(name)] = values
	}
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := hc.Do(req)
	if err != nil {
		return Reply{}, &Error{Reach, err}
	}
	defer res.Body.Close()
	limit := r.MaxResponse
	if limit <= 0 {
		limit = MaxResponse
	}
	// One byte past the cap tells an answer too large from one at the cap.
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return Reply{}, &Error{Read, err}
	}
	if int64(len(raw)) > limit {
		return Reply{}, &Error{Read, fmt.Errorf("the answer is larger than %d bytes", limit)}
	}
	return Reply{Status: res.StatusCode, Body: raw}, nil
}
