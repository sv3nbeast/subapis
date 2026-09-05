package service

import (
	"strings"

	"github.com/google/uuid"
)

// StableGrokAudioBillingRequestID returns the durable idempotency key used for
// one upstream voice request, independent of polling/request wrapper IDs.
func StableGrokAudioBillingRequestID(upstreamRequestID string) string {
	upstreamRequestID = strings.TrimSpace(upstreamRequestID)
	if upstreamRequestID == "" {
		upstreamRequestID = uuid.NewString()
	}
	if strings.HasPrefix(upstreamRequestID, "grok_audio:") {
		return upstreamRequestID
	}
	return "grok_audio:" + upstreamRequestID
}

func StableGrokRealtimeBillingRequestID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if strings.HasPrefix(sessionID, "grok_realtime:") {
		return sessionID
	}
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	return "grok_realtime:" + sessionID
}
