package core

import (
	"context"
	"time"

	"golang.org/x/mod/semver"
)

// ReleaseCheckInterval is how often the core asks its release source for a
// newer Release, to show it in the Catalogue. It never updates itself.
const ReleaseCheckInterval = 24 * time.Hour

// A latestReleaser names the newest published Release, as
// runtime.GitHubSource does. A local Release dir has none.
type latestReleaser interface {
	Latest(ctx context.Context) (string, error)
}

// checkReleaseDaily records the newest published Release at once and then
// every ReleaseCheckInterval, until ctx ends.
func (h *Host) checkReleaseDaily(ctx context.Context) {
	src, ok := h.release.(latestReleaser)
	if !ok {
		return
	}
	ticker := time.NewTicker(ReleaseCheckInterval)
	defer ticker.Stop()
	for {
		latest, err := src.Latest(ctx)
		if err != nil {
			h.log.Warn("check for a newer Release", "err", err)
		} else {
			h.mu.Lock()
			h.latest = latest
			h.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// availableRelease is the newest published Release when it is newer than
// the running one, "" otherwise.
func (h *Host) availableRelease() string {
	h.mu.Lock()
	latest := h.latest
	h.mu.Unlock()
	if semver.IsValid(latest) && semver.Compare(latest, h.runtime.Release()) > 0 {
		return latest
	}
	return ""
}
