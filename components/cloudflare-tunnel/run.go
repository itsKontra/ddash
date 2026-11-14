package cloudflaretunnel

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/itsKontra/ddash/components/cloudflare-tunnel/cloudflaretunnelpb"
)

func (c *component) PlanAction(ctx context.Context, req *pb.PlanActionRequest) (*pb.PlanActionResponse, error) {
	s, err := c.read(ctx, false)
	if err != nil {
		return nil, err
	}
	return &pb.PlanActionResponse{Plan: s.build(req.GetAction()).plan}, nil
}

func (c *component) RunAction(req *pb.RunActionRequest, stream grpc.ServerStreamingServer[pb.RunActionResponse]) error {
	ctx := stream.Context()
	c.turn.Lock()
	defer c.turn.Unlock()

	// The fresh read: the Plan is built again from what Cloudflare holds
	// now.
	s, err := c.read(ctx, true)
	if err != nil {
		return err
	}
	pl := s.build(req.GetAction())
	if !proto.Equal(pl.plan, req.GetPlan()) {
		st, derr := status.New(codes.Aborted, "changed since you looked: confirm the new Plan").WithDetails(pl.plan)
		if derr != nil {
			return derr
		}
		return st.Err()
	}
	if blocked := pl.plan.GetBlocked(); len(blocked) > 0 {
		return status.Error(codes.FailedPrecondition, strings.Join(blocked, " "))
	}

	steps := make([]pb.StepState, len(pl.runs))
	for i := range steps {
		steps[i] = pb.StepState_STEP_STATE_PENDING
	}
	// A message that can't be sent is lost: the Action runs to its end
	// all the same (ADR-0004).
	send := func() { _ = stream.Send(&pb.RunActionResponse{Steps: slices.Clone(steps)}) }
	for i, run := range pl.runs {
		steps[i] = pb.StepState_STEP_STATE_RUNNING
		send()
		if err := run(ctx, c); err != nil {
			steps[i] = pb.StepState_STEP_STATE_FAILED
			send()
			return stopped(pl.plan, i, err)
		}
		steps[i] = pb.StepState_STEP_STATE_DONE
		send()
	}
	return stream.Send(&pb.RunActionResponse{Done: int32(len(pl.runs)), Steps: steps})
}

// stopped is the error of a Tunnel Action that stopped at its step at
// (counting from 0) on err: it names the step and what was done before,
// and carries a RunActionResponse.
func stopped(plan *pb.Plan, at int, err error) error {
	steps := plan.GetSteps()
	msg := fmt.Sprintf("Stopped at step %d of %d, %s: %s.", at+1, len(steps), steps[at].GetTitle(), strings.TrimSuffix(status.Convert(err).Message(), "."))
	if at > 0 {
		var titles []string
		for _, s := range steps[:at] {
			titles = append(titles, s.GetTitle())
		}
		msg += " Done: " + strings.Join(titles, "; ") + "."
	}
	msg += " Nothing was rolled back."
	code := status.Code(err)
	if code == codes.OK || code == codes.Unknown {
		code = codes.Internal
	}
	st, derr := status.New(code, msg).WithDetails(&pb.RunActionResponse{Done: int32(at)})
	if derr != nil {
		return status.Error(code, msg)
	}
	return st.Err()
}
