// Package agentcall is what docker-agent-api and host-agent-api do alike
// when they talk to their Agents: the pairing handshake, what state a
// failed call leaves an Agent in, a failed call as a gRPC error, and
// reading and collecting a job. Each Component keeps its own Agents (by
// name, by VMID), its own operations and its own API; this is the part
// where the two must never drift, since it decides whom ddash trusts.
package agentcall

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/agent"
)

// State is where a failed call leaves an Agent.
type State int

const (
	// Unreachable: it didn't answer, or answered something else.
	Unreachable State = iota
	// FingerprintChanged: its certificate isn't the one pinned at pairing.
	FingerprintChanged
	// Refused: it refuses ddash's token.
	Refused
)

// StateOf is the state an Agent is in when a call to it failed with err,
// and what to tell the Operator.
func StateOf(err error) (State, string) {
	switch {
	case errors.Is(err, agent.ErrFingerprint):
		return FingerprintChanged, "its certificate isn't the one pinned at pairing: if you reinstalled it, pair it again"
	case agent.CodeOf(err) == agent.CodeBadToken || agent.CodeOf(err) == agent.CodeUnpaired:
		return Refused, "it refuses ddash's token: its state was reset, so pair it again"
	}
	return Unreachable, "unreachable: " + err.Error()
}

// Status is a failed call to an Agent as a gRPC error. agentName is "the
// Docker Agent" or "the Host Agent". conflict is the code of an Agent's
// 409, which each kind of Agent means differently: a file that changed
// since it was read (Aborted), a change that would lock the Operator out
// (FailedPrecondition).
func Status(err error, agentName string, conflict codes.Code) error {
	var e *agent.APIError
	if !errors.As(err, &e) {
		if errors.Is(err, agent.ErrFingerprint) {
			return status.Error(codes.FailedPrecondition, err.Error())
		}
		return status.Error(codes.Unavailable, agentName+": "+err.Error())
	}
	code := codes.Internal
	switch e.Code {
	case agent.CodeConflict:
		code = conflict
	case agent.CodeInvalid:
		code = codes.InvalidArgument
	case agent.CodeNotFound:
		code = codes.NotFound
	case agent.CodeOutOfRoots:
		code = codes.PermissionDenied
	case agent.CodeBusy:
		code = codes.FailedPrecondition
	case agent.CodeBadToken, agent.CodeUnpaired:
		code = codes.Unauthenticated
	}
	return status.Error(code, e.Message)
}

// Pairing is one attempt to pair with an enrolling Agent.
type Pairing struct {
	// URL is where the Agent listens, Code its one-time enrolment code.
	URL, Code string
	// Kind is the kind of Agent expected there, and KindName how the
	// Operator reads it: "Docker Agent", "Host Agent".
	Kind     agent.Kind
	KindName string
	// IfPaired tells the Operator what to do about an Agent that is paired
	// already, e.g. "install it again with the command above".
	IfPaired string
}

// Pair says hello to the Agent, checks it is the expected kind and not
// paired yet, and pairs with it on first use of its certificate: it
// returns the token both now keep and the certificate's fingerprint to
// pin. Nothing is kept when it fails.
func (p Pairing) Pair(ctx context.Context) (token, fingerprint string, err error) {
	hello, fp, err := agent.Probe(ctx, p.URL)
	switch {
	case err != nil:
		return "", "", fmt.Errorf("waiting for the Agent at %s: %v", p.URL, err)
	case hello.Kind != p.Kind:
		return "", "", fmt.Errorf("%s answers as a %s Agent, not a %s", p.URL, hello.Kind, p.KindName)
	case hello.Paired:
		return "", "", fmt.Errorf("the Agent is already paired (with another ddash, or before a reset): %s", p.IfPaired)
	}
	token, _, err = agent.Pair(ctx, p.URL, fp, p.Code)
	if err != nil {
		return "", "", fmt.Errorf("pairing refused: %v", err)
	}
	return token, fp, nil
}

// Job reads the job id as the Agent has it now.
func Job(ctx context.Context, c *agent.Client, id string) (agent.Job, error) {
	var j agent.Job
	err := c.Get(ctx, agent.PathJobs+"/"+url.PathEscape(id), nil, &j)
	return j, err
}

// Collect forgets the ended job id on its Agent and returns it. failed is
// the job's error as a gRPC error (Aborted) when the job failed, so the
// Audit Log entry of the collect records how the job ended; err is why
// the Agent couldn't be asked.
func Collect(ctx context.Context, c *agent.Client, id string) (j agent.Job, failed, err error) {
	if err := c.Delete(ctx, agent.PathJobs+"/"+url.PathEscape(id), nil, &j); err != nil {
		return j, nil, err
	}
	if j.State == agent.Failed {
		return j, status.Errorf(codes.Aborted, "%s failed: %s", j.Title, j.Error), nil
	}
	return j, nil, nil
}
