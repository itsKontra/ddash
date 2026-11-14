// Package jobproto turns an Agent's jobs into ddash.job protos, for the
// Agent API Components.
package jobproto

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/agent"
	jobpb "github.com/itsKontra/ddash/proto/ddash/job"
)

var states = map[agent.JobState]jobpb.State{
	agent.Pending: jobpb.State_STATE_PENDING,
	agent.Running: jobpb.State_STATE_RUNNING,
	agent.Done:    jobpb.State_STATE_DONE,
	agent.Failed:  jobpb.State_STATE_FAILED,
	agent.Skipped: jobpb.State_STATE_SKIPPED,
}

// Of is j, run by the Agent named agentName.
func Of(j agent.Job, agentName string) *jobpb.Job {
	out := &jobpb.Job{
		Id: j.ID, Op: j.Op, Title: j.Title, Target: j.Target, State: states[j.State],
		Start: timestamppb.New(j.Start), Error: j.Error, Result: j.Result, Agent: agentName,
	}
	if !j.End.IsZero() {
		out.End = timestamppb.New(j.End)
	}
	for _, s := range j.Steps {
		out.Steps = append(out.Steps, &jobpb.Step{Title: s.Title, State: states[s.State], Detail: s.Detail, Log: s.Log})
	}
	return out
}
