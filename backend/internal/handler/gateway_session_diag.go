package handler

import (
	"os"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

const (
	gatewaySessionDiagUserIDsEnv   = "GATEWAY_SESSION_DIAG_USER_IDS"
	gatewaySessionDiagAPIKeyIDsEnv = "GATEWAY_SESSION_DIAG_API_KEY_IDS"
)

func gatewaySessionDiagEnabled(userID, apiKeyID int64) bool {
	return containsIDFromEnv(gatewaySessionDiagUserIDsEnv, userID) ||
		containsIDFromEnv(gatewaySessionDiagAPIKeyIDsEnv, apiKeyID)
}

func containsIDFromEnv(key string, id int64) bool {
	if id <= 0 {
		return false
	}
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return false
	}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	}) {
		v, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err == nil && v == id {
			return true
		}
	}
	return false
}

func gatewaySessionDiagSource(parsed *service.ParsedRequest, sessionHash string) (source string, metadataPresent bool, metadataParseOK bool, metadataFormat string) {
	if parsed == nil {
		if sessionHash == "" {
			return "none", false, false, ""
		}
		return "fallback_unknown", false, false, ""
	}

	rawMetadata := strings.TrimSpace(parsed.MetadataUserID)
	if rawMetadata != "" {
		metadataPresent = true
		if uid := service.ParseMetadataUserID(rawMetadata); uid != nil {
			metadataParseOK = true
			if uid.IsNewFormat {
				metadataFormat = "json"
			} else {
				metadataFormat = "legacy"
			}
			return "metadata_user_id", metadataPresent, metadataParseOK, metadataFormat
		}
		return "metadata_user_id_unparsed", metadataPresent, metadataParseOK, metadataFormat
	}

	if sessionHash == "" {
		return "none", false, false, ""
	}
	return "fallback_body", false, false, ""
}

func gatewaySessionDiagInitialFields(parsed *service.ParsedRequest, sessionHash, sessionKey string, sessionBoundAccountID int64, hasBoundSession bool) []zap.Field {
	source, metadataPresent, metadataParseOK, metadataFormat := gatewaySessionDiagSource(parsed, sessionHash)
	return []zap.Field{
		zap.String("session_hash_source", source),
		zap.Bool("metadata_user_id_present", metadataPresent),
		zap.Bool("metadata_user_id_parse_ok", metadataParseOK),
		zap.String("metadata_user_id_format", metadataFormat),
		zap.Bool("has_session_hash", sessionHash != ""),
		zap.String("session_hash_short", shortGatewaySessionHash(sessionHash)),
		zap.Bool("has_session_key", sessionKey != ""),
		zap.String("session_key_short", shortGatewaySessionHash(sessionKey)),
		zap.Int64("session_bound_account_id", sessionBoundAccountID),
		zap.Bool("has_bound_session", hasBoundSession),
	}
}

func shortGatewaySessionHash(sessionHash string) string {
	if sessionHash == "" {
		return ""
	}
	if len(sessionHash) <= 8 {
		return sessionHash
	}
	return sessionHash[:8]
}
