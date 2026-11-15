// Package dockerread turns what Docker writes about a container into the
// shared Container's enums, for the Components that read Docker
// (docker-api and docker-agent-api).
package dockerread

import (
	"strings"

	containerpb "github.com/itsKontra/ddash/proto/ddash/container"
)

var states = map[string]containerpb.State{
	"created":    containerpb.State_STATE_CREATED,
	"running":    containerpb.State_STATE_RUNNING,
	"paused":     containerpb.State_STATE_PAUSED,
	"restarting": containerpb.State_STATE_RESTARTING,
	"removing":   containerpb.State_STATE_REMOVING,
	"exited":     containerpb.State_STATE_EXITED,
	"dead":       containerpb.State_STATE_DEAD,
}

// State is Docker's container state ("running", "exited", …);
// STATE_UNSPECIFIED for one this Release doesn't know.
func State(docker string) containerpb.State { return states[docker] }

// StateWord is s as Docker writes it and the Operator reads it, e.g.
// "running"; "unknown" for STATE_UNSPECIFIED.
func StateWord(s containerpb.State) string {
	for word, state := range states {
		if state == s {
			return word
		}
	}
	return "unknown"
}

// Health reads a container's health from Docker's human-readable status,
// the only place the list API carries it: "Up 3 hours (healthy)",
// "Up 2 minutes (unhealthy)", "Up 5 seconds (health: starting)".
func Health(status string) containerpb.Health {
	switch {
	case strings.Contains(status, "(unhealthy)"):
		return containerpb.Health_HEALTH_UNHEALTHY
	case strings.Contains(status, "(healthy)"):
		return containerpb.Health_HEALTH_HEALTHY
	case strings.Contains(status, "(health: starting)"):
		return containerpb.Health_HEALTH_STARTING
	}
	return containerpb.Health_HEALTH_UNSPECIFIED
}

var protocols = map[string]containerpb.Protocol{
	"tcp":  containerpb.Protocol_PROTOCOL_TCP,
	"udp":  containerpb.Protocol_PROTOCOL_UDP,
	"sctp": containerpb.Protocol_PROTOCOL_SCTP,
}

// Protocol is a published port's protocol ("tcp", "udp", "sctp").
func Protocol(docker string) containerpb.Protocol { return protocols[docker] }

// Protected reports whether a container's labels mark it Protected.
func Protected(labels map[string]string) bool { return labels[containerpb.LabelProtected] == "true" }
