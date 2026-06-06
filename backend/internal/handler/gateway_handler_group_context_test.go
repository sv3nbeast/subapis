package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestAttachAPIKeyGroupToParsedRequest(t *testing.T) {
	groupID := int64(23)
	group := &service.Group{
		ID:                        groupID,
		Platform:                  service.PlatformKiro,
		KiroCacheEmulationEnabled: true,
		KiroCacheEmulationRatio:   0.91,
	}
	parsed := &service.ParsedRequest{}
	apiKey := &service.APIKey{
		GroupID: &groupID,
		Group:   group,
	}

	attachAPIKeyGroupToParsedRequest(parsed, apiKey)

	if parsed.GroupID == nil || *parsed.GroupID != groupID {
		t.Fatalf("parsed GroupID = %v, want %d", parsed.GroupID, groupID)
	}
	if parsed.Group != group {
		t.Fatalf("parsed Group was not attached from API key")
	}
	if !parsed.Group.KiroCacheEmulationEnabled {
		t.Fatalf("parsed Group lost Kiro cache emulation config")
	}
	if parsed.Group.KiroCacheEmulationRatio != 0.91 {
		t.Fatalf("parsed Group KiroCacheEmulationRatio = %v, want 0.91", parsed.Group.KiroCacheEmulationRatio)
	}
}

func TestAttachAPIKeyGroupToParsedRequestNilSafe(t *testing.T) {
	attachAPIKeyGroupToParsedRequest(nil, nil)

	parsed := &service.ParsedRequest{}
	attachAPIKeyGroupToParsedRequest(parsed, nil)
	if parsed.GroupID != nil || parsed.Group != nil {
		t.Fatalf("nil API key should leave parsed group fields empty: %+v", parsed)
	}
}
