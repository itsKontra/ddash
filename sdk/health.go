package sdk

import componentpb "github.com/itsKontra/ddash/proto/ddash/component"

// State is how well a Component works. The host adds its own states
// (starting, unconfigured, crashed, stopped) on top of these.
type State int

const (
	// OK: the Component and its system work.
	OK State = iota + 1
	// Degraded: it works partly, e.g. one of several hosts is unreachable.
	Degraded
	// Down: it doesn't work.
	Down
)

func (s State) String() string {
	switch s {
	case OK:
		return "ok"
	case Degraded:
		return "degraded"
	case Down:
		return "down"
	}
	return "unknown"
}

// Health is a State with a one-line message, e.g.
// {Degraded, "media unreachable: connection refused"}.
type Health struct {
	State   State
	Message string
}

func (h Health) String() string {
	if h.Message == "" {
		return h.State.String()
	}
	return h.State.String() + ": " + h.Message
}

func (h Health) proto() *componentpb.HealthResponse {
	states := map[State]componentpb.HealthState{
		OK:       componentpb.HealthState_HEALTH_STATE_OK,
		Degraded: componentpb.HealthState_HEALTH_STATE_DEGRADED,
		Down:     componentpb.HealthState_HEALTH_STATE_DOWN,
	}
	return &componentpb.HealthResponse{State: states[h.State], Message: h.Message}
}

// HealthFromProto converts the host's view of a Health back.
func HealthFromProto(r *componentpb.HealthResponse) Health {
	states := map[componentpb.HealthState]State{
		componentpb.HealthState_HEALTH_STATE_OK:       OK,
		componentpb.HealthState_HEALTH_STATE_DEGRADED: Degraded,
		componentpb.HealthState_HEALTH_STATE_DOWN:     Down,
	}
	return Health{State: states[r.GetState()], Message: r.GetMessage()}
}
