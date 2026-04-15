package handler

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestGatewaySessionDiagEnabled(t *testing.T) {
	t.Setenv(gatewaySessionDiagUserIDsEnv, "12, 34;56\ninvalid")
	t.Setenv(gatewaySessionDiagAPIKeyIDsEnv, "90")

	if !gatewaySessionDiagEnabled(34, 0) {
		t.Fatal("expected user id match to enable gateway session diagnostics")
	}
	if !gatewaySessionDiagEnabled(0, 90) {
		t.Fatal("expected api key id match to enable gateway session diagnostics")
	}
	if gatewaySessionDiagEnabled(35, 91) {
		t.Fatal("did not expect diagnostics for unmatched ids")
	}
}

func TestGatewaySessionDiagSource(t *testing.T) {
	deviceID := strings.Repeat("a", 64)
	sessionID := "123e4567-e89b-12d3-a456-426614174000"

	tests := []struct {
		name                string
		parsed              *service.ParsedRequest
		sessionHash         string
		wantSource          string
		wantMetadataPresent bool
		wantMetadataOK      bool
		wantFormat          string
	}{
		{
			name:       "none",
			wantSource: "none",
		},
		{
			name:        "fallback unknown",
			sessionHash: "abc123",
			wantSource:  "fallback_unknown",
		},
		{
			name:        "fallback body",
			parsed:      &service.ParsedRequest{},
			sessionHash: "abc123",
			wantSource:  "fallback_body",
		},
		{
			name:                "legacy metadata",
			parsed:              &service.ParsedRequest{MetadataUserID: service.FormatMetadataUserID(deviceID, "", sessionID, "1.0.0")},
			wantSource:          "metadata_user_id",
			wantMetadataPresent: true,
			wantMetadataOK:      true,
			wantFormat:          "legacy",
		},
		{
			name:                "json metadata",
			parsed:              &service.ParsedRequest{MetadataUserID: service.FormatMetadataUserID(deviceID, "", sessionID, service.NewMetadataFormatMinVersion)},
			wantSource:          "metadata_user_id",
			wantMetadataPresent: true,
			wantMetadataOK:      true,
			wantFormat:          "json",
		},
		{
			name:                "unparsed metadata",
			parsed:              &service.ParsedRequest{MetadataUserID: "bad-user-id"},
			wantSource:          "metadata_user_id_unparsed",
			wantMetadataPresent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, present, ok, format := gatewaySessionDiagSource(tt.parsed, tt.sessionHash)
			if source != tt.wantSource {
				t.Fatalf("source = %q, want %q", source, tt.wantSource)
			}
			if present != tt.wantMetadataPresent {
				t.Fatalf("metadata present = %v, want %v", present, tt.wantMetadataPresent)
			}
			if ok != tt.wantMetadataOK {
				t.Fatalf("metadata ok = %v, want %v", ok, tt.wantMetadataOK)
			}
			if format != tt.wantFormat {
				t.Fatalf("format = %q, want %q", format, tt.wantFormat)
			}
		})
	}
}

func TestShortGatewaySessionHash(t *testing.T) {
	if got := shortGatewaySessionHash("1234567890"); got != "12345678" {
		t.Fatalf("shortGatewaySessionHash = %q, want %q", got, "12345678")
	}
	if got := shortGatewaySessionHash("1234"); got != "1234" {
		t.Fatalf("shortGatewaySessionHash = %q, want %q", got, "1234")
	}
}
