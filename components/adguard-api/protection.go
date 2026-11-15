package adguardapi

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/adguard-api/adguardapipb"
)

// Protection, paused and resumed. AdGuard holds the pause and ends it
// itself, so a pause outlives ddash and one set in AdGuard Home reads the
// same (GetStatus's protection_paused_for).

// longestPause bounds PauseProtection: a pause, not protection off.
const longestPause = 24 * time.Hour

func (c *component) PauseProtection(ctx context.Context, req *adguardapipb.PauseProtectionRequest) (*adguardapipb.PauseProtectionResponse, error) {
	d := req.GetDuration().AsDuration()
	if !req.GetDuration().IsValid() || d < time.Millisecond || d > longestPause {
		return nil, status.Errorf(codes.InvalidArgument, "a pause lasts more than nothing and at most %v, not %v", longestPause, d)
	}
	if err := c.setProtection(ctx, map[string]any{"enabled": false, "duration": d.Milliseconds()}); err != nil {
		return nil, err
	}
	return &adguardapipb.PauseProtectionResponse{}, nil
}

func (c *component) ResumeProtection(ctx context.Context, _ *adguardapipb.ResumeProtectionRequest) (*adguardapipb.ResumeProtectionResponse, error) {
	if err := c.setProtection(ctx, map[string]any{"enabled": true}); err != nil {
		return nil, err
	}
	return &adguardapipb.ResumeProtectionResponse{}, nil
}

// setProtection posts body to /control/protection (SetProtectionRequest:
// enabled, and a pause's duration in milliseconds), so the next status
// read is AdGuard's answer to it.
func (c *component) setProtection(ctx context.Context, body map[string]any) error {
	defer c.status.Invalidate()
	return c.api.post(ctx, "/control/protection", body)
}
