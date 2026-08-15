package service

// This file contains the deliberately small, strict ingress boundary used by
// the versioned Anthropic stable executor. It is separate from the legacy
// gjson/sjson request parser because that parser is allowed to normalize and
// re-encode requests. The D1 same-installation canary retains every body byte.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

const (
	AnthropicStableIngressMaxBodyBytes = 64 << 20
	AnthropicStableMessagesPath        = "/v1/messages"
	AnthropicStableRefreshURL          = AnthropicStableOAuthTokenOriginV1 + AnthropicStableOAuthTokenPathV1
	AnthropicStableIngressQueryV1      = "beta=true"
	AnthropicStableIngressXAppV1       = "cli"
	AnthropicStableIngressAPIVersionV1 = "2023-06-01"
)

const (
	AnthropicStableIngressProfileCLI211222V1    = "claude_cli_2_1_222_v1"
	AnthropicStableIngressProfileSDKCLI211222V1 = "claude_sdk_cli_2_1_222_v1"
	AnthropicStableIngressFamilyCLI211222V1     = "claude_code_2_1_222_family_v1"
)

// These are the exact beta header variants observed from Claude CLI 2.1.222
// in the local claude-gateway capture.  Claude Code changes the enabled beta
// set as the request moves between the initial prompt, an agentic turn and a
// follow-up turn.  They are deliberately kept as a finite allow-list: the
// stable executor must not turn this into a broad "contains claude-code"
// check that could admit an arbitrary SDK/client.
const (
	AnthropicStableIngressBetaCLI211222BaseV1       = "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05"
	AnthropicStableIngressBetaCLI211222AgenticV1    = "interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,claude-code-20250219,effort-2025-11-24"
	AnthropicStableIngressBetaCLI211222FullV1       = "interleaved-thinking-2025-05-14,redact-thinking-2026-02-12,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,effort-2025-11-24,structured-outputs-2025-12-15"
	AnthropicStableIngressBetaSDKCLI211222V1        = "claude-code-20250219,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05"
	AnthropicStableIngressBetaSDKCLI211222EffortV1  = "claude-code-20250219,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,effort-2025-11-24"
	AnthropicStableIngressBetaSDKCLI211222AgenticV1 = "claude-code-20250219,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,effort-2025-11-24"
)

var anthropicStableIngressProfiles = map[string]struct {
	userAgent string
	// beta is retained as the canonical/default variant for existing tests and
	// account records. acceptedBetas is the exact cohort allow-list.
	beta          string
	acceptedBetas []string
}{
	AnthropicStableIngressProfileCLI211222V1: {
		userAgent: "claude-cli/2.1.222 (external, cli)",
		beta:      AnthropicStableIngressBetaCLI211222FullV1,
		acceptedBetas: []string{
			AnthropicStableIngressBetaCLI211222BaseV1,
			AnthropicStableIngressBetaCLI211222AgenticV1,
			AnthropicStableIngressBetaCLI211222FullV1,
		},
	},
	AnthropicStableIngressProfileSDKCLI211222V1: {
		userAgent: "claude-cli/2.1.222 (external, sdk-cli)",
		beta:      AnthropicStableIngressBetaSDKCLI211222V1,
		acceptedBetas: []string{
			AnthropicStableIngressBetaSDKCLI211222V1,
			AnthropicStableIngressBetaSDKCLI211222EffortV1,
			AnthropicStableIngressBetaSDKCLI211222AgenticV1,
		},
	},
}

// The durable stable-identity profile name predates the concrete Claude CLI
// 2.1.222 capture name. Keep it as an explicit alias rather than adding a
// second map entry: duplicate entries would make Detect() depend on Go map
// iteration order and could bind the same request to two generations.
var anthropicStableIngressProfileAliases = map[string]string{
	AnthropicStableIngressProfileClaudeCLICustomBaseV1: AnthropicStableIngressProfileCLI211222V1,
}

func canonicalAnthropicStableIngressProfileID(profileID string) string {
	profileID = strings.TrimSpace(profileID)
	if canonical, ok := anthropicStableIngressProfileAliases[profileID]; ok {
		return canonical
	}
	return profileID
}

func anthropicStableIngressFamilyID(profileID string) string {
	switch canonicalAnthropicStableIngressProfileID(profileID) {
	case AnthropicStableIngressProfileCLI211222V1, AnthropicStableIngressProfileSDKCLI211222V1:
		return AnthropicStableIngressFamilyCLI211222V1
	default:
		return canonicalAnthropicStableIngressProfileID(profileID)
	}
}

// AnthropicStableIngressProfilesEquivalent compares persisted transport-policy
// labels. The cli, sdk-cli and durable custom-base labels all use the same
// claude-gateway-style outbound construction. D1 still performs exact capture
// admission; the shared identity scheduler admits native client upgrades under
// this stable outbound family.
func AnthropicStableIngressProfilesEquivalent(left, right string) bool {
	left = anthropicStableIngressFamilyID(left)
	right = anthropicStableIngressFamilyID(right)
	return left != "" && left == right
}

func anthropicStableIngressProfileAcceptsBeta(profile struct {
	userAgent     string
	beta          string
	acceptedBetas []string
}, beta string) bool {
	for _, accepted := range profile.acceptedBetas {
		if beta == accepted {
			return true
		}
	}
	// Keep profiles created before acceptedBetas was introduced valid. This is
	// also a defensive fallback for a future profile declaration mistake.
	return beta == profile.beta
}

// DetectAnthropicStableIngressProfile returns a profile only for an exact
// captured UA/beta pair. Unknown client versions fail closed instead of being
// admitted by a broad version regex.
func DetectAnthropicStableIngressProfile(userAgent, anthropicBeta string) string {
	for id, profile := range anthropicStableIngressProfiles {
		if userAgent == profile.userAgent && anthropicStableIngressProfileAcceptsBeta(profile, anthropicBeta) {
			return id
		}
	}
	return ""
}

// DetectAnthropicStableIdentityIngressProfile recognizes the native Claude
// Code client family used by the shared stable-identity scheduler. Unlike the
// capture-backed canary detector above, it deliberately does not pin a client
// version or anthropic-beta value: the reference claude-gateway rebuilds the
// outbound HTTP request, forwards the client's beta/version headers and lets
// Anthropic negotiate those features.
func DetectAnthropicStableIdentityIngressProfile(userAgent string) string {
	if !claudeStableIdentityUserAgentPattern.MatchString(strings.TrimSpace(userAgent)) {
		return ""
	}
	return AnthropicStableIngressProfileClaudeCLICustomBaseV1
}

// LooksLikeAnthropicStableClaudeCode is intentionally broader than either
// admission detector: it recognizes the Claude CLI product token so managed
// groups do not silently fall back to a generic account. The shared identity
// detector then validates the native cli/sdk-cli form without pinning a
// version, while the D1 canary detector retains its exact capture policy.
func LooksLikeAnthropicStableClaudeCode(userAgent string) bool {
	return claudeStableUserAgentPattern.MatchString(strings.TrimSpace(userAgent))
}

var (
	// Claude Code sends a product/version token followed by an optional comment.
	// The body profile below remains the authoritative signal; this check only
	// prevents accidentally routing an arbitrary SDK request into the strict
	// executor.
	claudeStableUserAgentPattern         = regexp.MustCompile(`^claude-cli/[0-9]+\.[0-9]+\.[0-9]+(?:\s|$)`)
	claudeStableIdentityUserAgentPattern = regexp.MustCompile(`^claude-cli/[0-9]+\.[0-9]+\.[0-9]+ \(external, (?:cli|sdk-cli)\)$`)
	claudeStableUUIDPattern              = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

var (
	ErrAnthropicStableIngressNotClaudeCode = errors.New("stable ingress is not a Claude Code request")
	ErrAnthropicStableIngressMalformed     = errors.New("stable ingress request is malformed")
	ErrAnthropicStableIngressDuplicateKey  = errors.New("stable ingress request contains duplicate JSON keys")
	ErrAnthropicStableIngressDevicePatch   = errors.New("stable ingress device patch is not safe")
	ErrAnthropicStableIngressCCHPresent    = errors.New("stable ingress request contains an unsupported cch billing marker")
)

// AnthropicStableIngressRequest is a zero-copy view over the inbound body.
// RawBody is never modified or rewritten on the stable forwarding paths. The
// parsed identity fields are used only for admission and session routing; the
// client device_id and account_uuid remain part of the original upstream body.
type AnthropicStableIngressRequest struct {
	RawBody       []byte
	ProfileID     string
	Model         string
	MaxTokens     int64
	HasMaxTokens  bool
	Stream        bool
	HasStream     bool
	SessionID     string
	InboundDevice string
	AccountUUID   string
	DeviceStart   int
	DeviceEnd     int
}

// ParseAnthropicStableIngress validates only the native Claude Code
// /v1/messages shape.  It intentionally does not inspect or rewrite system,
// tools, thinking, cache_control, metadata, or any other request field.
func ParseAnthropicStableIngress(method, path, contentEncoding, userAgent string, body []byte) (*AnthropicStableIngressRequest, error) {
	if method != http.MethodPost || path != AnthropicStableMessagesPath {
		return nil, fmt.Errorf("%w: method/path", ErrAnthropicStableIngressNotClaudeCode)
	}
	if strings.TrimSpace(contentEncoding) != "" && !strings.EqualFold(strings.TrimSpace(contentEncoding), "identity") {
		return nil, fmt.Errorf("%w: content encoding is not identity", ErrAnthropicStableIngressMalformed)
	}
	if len(body) == 0 || len(body) > AnthropicStableIngressMaxBodyBytes {
		return nil, fmt.Errorf("%w: body size %d", ErrAnthropicStableIngressMalformed, len(body))
	}
	if !claudeStableUserAgentPattern.MatchString(strings.TrimSpace(userAgent)) {
		return nil, fmt.Errorf("%w: user-agent", ErrAnthropicStableIngressNotClaudeCode)
	}

	parser := stableJSONScanner{body: body, maxDepth: 512}
	fields, err := parser.parseIngressRoot()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAnthropicStableIngressMalformed, err)
	}
	// The D1 profile is the captured no-CCH variant.  Keep this check scoped to
	// the first-party billing system block so an ordinary user prompt that
	// happens to contain the text "cch=" is not rejected.  The strict scanner
	// above remains authoritative for JSON validity and duplicate keys.
	if stableAnthropicBillingCCHPresent(body) {
		return nil, fmt.Errorf("%w: %w", ErrAnthropicStableIngressMalformed, ErrAnthropicStableIngressCCHPresent)
	}
	userID, deviceStartInDecoded, deviceEndInDecoded, accountUUID, sessionID, err := parseStableMetadataUserID(
		fields.metadataUserID,
		fields.metadataUserIDMapping,
	)
	if err != nil {
		return nil, err
	}
	if !claudeStableUUIDPattern.MatchString(sessionID) {
		return nil, fmt.Errorf("%w: session_id must be a lowercase canonical UUID", ErrAnthropicStableIngressMalformed)
	}
	if !IsValidAnthropicStableDeviceID(userID) {
		return nil, fmt.Errorf("%w: device_id must be lowercase 64-byte hex", ErrAnthropicStableIngressMalformed)
	}
	if deviceStartInDecoded < 0 || deviceEndInDecoded-deviceStartInDecoded != len(userID) {
		return nil, fmt.Errorf("%w: device range", ErrAnthropicStableIngressMalformed)
	}
	if deviceStartInDecoded >= len(fields.metadataUserIDMapping) || deviceEndInDecoded > len(fields.metadataUserIDMapping) {
		return nil, fmt.Errorf("%w: device mapping range", ErrAnthropicStableIngressMalformed)
	}
	deviceStart := fields.metadataUserIDMapping[deviceStartInDecoded]
	deviceEnd := fields.metadataUserIDMapping[deviceEndInDecoded-1] + 1
	if deviceStart < fields.metadataUserIDRawStart || deviceEnd > fields.metadataUserIDRawEnd || deviceEnd-deviceStart != len(userID) {
		return nil, fmt.Errorf("%w: device raw range", ErrAnthropicStableIngressMalformed)
	}
	for offset := 0; offset < len(userID); offset++ {
		if fields.metadataUserIDMapping[deviceStartInDecoded+offset] != deviceStart+offset {
			return nil, fmt.Errorf("%w: escaped device value", ErrAnthropicStableIngressMalformed)
		}
	}

	result := &AnthropicStableIngressRequest{
		RawBody: body, Model: fields.model, MaxTokens: fields.maxTokens,
		HasMaxTokens: fields.maxTokensSeen, SessionID: sessionID,
		InboundDevice: userID, AccountUUID: accountUUID,
		DeviceStart: deviceStart, DeviceEnd: deviceEnd,
		HasStream: fields.hasStream, Stream: fields.stream,
	}
	return result, nil
}

func stableAnthropicBillingCCHPresent(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	system := gjson.GetBytes(body, "system")
	if !system.Exists() {
		return false
	}
	containsCCH := func(value string) bool {
		value = strings.ToLower(strings.TrimSpace(value))
		return strings.HasPrefix(value, anthropicBillingHeaderPrefix) && strings.Contains(value, "cch=")
	}
	if system.Type == gjson.String {
		return containsCCH(system.String())
	}
	if !system.IsArray() {
		return false
	}
	found := false
	system.ForEach(func(_, item gjson.Result) bool {
		text := item.Get("text")
		if text.Type == gjson.String && containsCCH(text.String()) {
			found = true
			return false
		}
		return true
	})
	return found
}

// ParseAnthropicStableIngressProfile is the strict admission boundary used by
// the online canary. Unlike the compatibility parser above, it accepts only a
// captured CLI profile and validates the inbound query/session/header tuple
// before the body can reach any generic gateway parser.
func ParseAnthropicStableIngressProfile(
	method, path, rawQuery, contentEncoding, userAgent, xApp, sessionHeader,
	anthropicBeta, anthropicVersion, profileID string, body []byte,
) (*AnthropicStableIngressRequest, error) {
	canonicalProfileID := canonicalAnthropicStableIngressProfileID(profileID)
	profile, ok := anthropicStableIngressProfiles[canonicalProfileID]
	if !ok || rawQuery != AnthropicStableIngressQueryV1 || xApp != AnthropicStableIngressXAppV1 ||
		anthropicVersion != AnthropicStableIngressAPIVersionV1 || userAgent != profile.userAgent ||
		!anthropicStableIngressProfileAcceptsBeta(profile, anthropicBeta) {
		return nil, fmt.Errorf("%w: captured Claude Code profile mismatch", ErrAnthropicStableIngressNotClaudeCode)
	}
	result, err := ParseAnthropicStableIngress(method, path, contentEncoding, userAgent, body)
	if err != nil {
		return nil, err
	}
	if sessionHeader != result.SessionID {
		return nil, fmt.Errorf("%w: session header does not match body", ErrAnthropicStableIngressMalformed)
	}
	if !result.HasStream || !result.Stream || !result.HasMaxTokens {
		return nil, fmt.Errorf("%w: captured profile requires stream=true and max_tokens", ErrAnthropicStableIngressMalformed)
	}
	// Preserve the caller's persisted label for audit/account comparison while
	// validating against the canonical finite cohort above.
	result.ProfileID = strings.TrimSpace(profileID)
	return result, nil
}

// ParseAnthropicStableIdentityIngress is the shared scheduler's native Claude
// Code admission boundary. It retains the structural checks needed for safe
// multi-user session binding and equal-length device replacement, but it does
// not use the client version, anthropic-beta or anthropic-version as a feature
// allow-list. Those headers are preserved by BuildAnthropicStableMessageRequest
// and the OAuth beta is appended only when absent.
func ParseAnthropicStableIdentityIngress(
	method, path, rawQuery, contentEncoding, userAgent, xApp, sessionHeader string,
	body []byte,
) (*AnthropicStableIngressRequest, error) {
	profileID := DetectAnthropicStableIdentityIngressProfile(userAgent)
	if profileID == "" || rawQuery != AnthropicStableIngressQueryV1 || xApp != AnthropicStableIngressXAppV1 {
		return nil, fmt.Errorf("%w: native Claude Code identity mismatch", ErrAnthropicStableIngressNotClaudeCode)
	}
	result, err := ParseAnthropicStableIngress(method, path, contentEncoding, userAgent, body)
	if err != nil {
		return nil, err
	}
	if sessionHeader != result.SessionID {
		return nil, fmt.Errorf("%w: session header does not match body", ErrAnthropicStableIngressMalformed)
	}
	if !result.HasStream || !result.Stream || !result.HasMaxTokens {
		return nil, fmt.Errorf("%w: native Claude Code requires stream=true and max_tokens", ErrAnthropicStableIngressMalformed)
	}
	result.ProfileID = profileID
	return result, nil
}

// PatchDevice is retained as a small compatibility utility for older callers
// and tests. Native stable forwarding does not call it: the upstream request
// must retain the client's original device_id and account_uuid.
func (r *AnthropicStableIngressRequest) PatchDevice(deviceID string) ([]byte, error) {
	if r == nil || r.RawBody == nil || r.DeviceStart < 0 || r.DeviceEnd <= r.DeviceStart ||
		r.DeviceEnd > len(r.RawBody) || r.DeviceEnd-r.DeviceStart != 64 ||
		!IsValidAnthropicStableDeviceID(r.InboundDevice) || !IsValidAnthropicStableDeviceID(deviceID) {
		return nil, ErrAnthropicStableIngressDevicePatch
	}
	patched := make([]byte, len(r.RawBody))
	copy(patched, r.RawBody)
	copy(patched[r.DeviceStart:r.DeviceEnd], deviceID)
	if len(patched) != len(r.RawBody) || !bytes.Equal(patched[:r.DeviceStart], r.RawBody[:r.DeviceStart]) ||
		!bytes.Equal(patched[r.DeviceEnd:], r.RawBody[r.DeviceEnd:]) {
		return nil, ErrAnthropicStableIngressDevicePatch
	}
	return patched, nil
}

func parseStableMetadataUserID(value string, mapping []int) (device string, deviceStart, deviceEnd int, accountUUID, sessionID string, err error) {
	deviceStart, deviceEnd = -1, -1
	if len(mapping) != len(value) {
		return "", -1, -1, "", "", fmt.Errorf("%w: metadata string mapping", ErrAnthropicStableIngressMalformed)
	}
	nestedParser := stableJSONScanner{body: []byte(value), maxDepth: 32}
	fields, parseErr := nestedParser.parseMetadataUserID()
	if parseErr != nil {
		return "", -1, -1, "", "", fmt.Errorf("%w: metadata.user_id must contain JSON", ErrAnthropicStableIngressMalformed)
	}
	device, accountUUID, sessionID = fields.deviceID, fields.accountUUID, fields.sessionID
	if !IsValidAnthropicStableDeviceID(device) {
		return "", -1, -1, "", "", fmt.Errorf("%w: metadata device", ErrAnthropicStableIngressMalformed)
	}
	if fields.deviceStart < 0 || fields.deviceEnd < fields.deviceStart || fields.deviceEnd-fields.deviceStart != len(device) {
		return "", -1, -1, "", "", fmt.Errorf("%w: metadata device range", ErrAnthropicStableIngressMalformed)
	}
	if string(nestedParser.body[fields.deviceStart:fields.deviceEnd]) != device {
		return "", -1, -1, "", "", fmt.Errorf("%w: escaped device value", ErrAnthropicStableIngressMalformed)
	}
	deviceStart, deviceEnd = fields.deviceStart, fields.deviceEnd
	return device, deviceStart, deviceEnd, accountUUID, sessionID, nil
}

// BuildAnthropicStableMessageRequest mirrors the reference gateway's message
// request construction: only Content-Type, Bearer auth, API version and the
// OAuth beta are written; inbound Authorization, API key, cookies and UA are
// never copied.  The default net/http User-Agent is intentionally left unset.
func BuildAnthropicStableMessageRequest(
	ctx context.Context,
	baseURL string,
	header http.Header,
	body []byte,
	token string,
) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	baseURL = strings.TrimSpace(baseURL)
	if baseURL != AnthropicStableMessagesOriginV1 || token == "" || len(body) == 0 {
		return nil, fmt.Errorf("stable message request is incomplete")
	}
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil || parsedBaseURL.Scheme != "https" || parsedBaseURL.Host != "api.anthropic.com" ||
		parsedBaseURL.Path != "" || parsedBaseURL.RawPath != "" || parsedBaseURL.RawQuery != "" ||
		parsedBaseURL.Fragment != "" || parsedBaseURL.User != nil {
		return nil, fmt.Errorf("invalid stable base URL")
	}
	parsedBaseURL.Path = AnthropicStableMessagesPath
	parsedBaseURL.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedBaseURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create stable message request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if header != nil {
		if value := header.Get("anthropic-beta"); value != "" {
			req.Header.Set("anthropic-beta", value)
		}
		if value := header.Get("anthropic-version"); value != "" {
			req.Header.Set("anthropic-version", value)
		}
	}
	if req.Header.Get("anthropic-version") == "" {
		req.Header.Set("anthropic-version", AnthropicStableDefaultAPIVersionV1)
	}
	appendAnthropicStableBeta(req.Header, AnthropicStableOAuthBetaV1)
	req.Header.Del("x-api-key")
	req.Header.Set("Authorization", "Bearer "+token)
	return req, nil
}

func appendAnthropicStableBeta(header http.Header, beta string) {
	if header == nil || beta == "" {
		return
	}
	existing := header.Get("anthropic-beta")
	if existing == "" {
		header.Set("anthropic-beta", beta)
		return
	}
	for _, value := range strings.Split(existing, ",") {
		if strings.TrimSpace(value) == beta {
			return
		}
	}
	header.Set("anthropic-beta", existing+","+beta)
}

// BuildAnthropicStableRefreshRequest mirrors claude-gateway's refresh body.
// encoding/json sorts map keys, matching the reference implementation's map
// payload order while keeping the refresh token out of logs and URL strings.
func BuildAnthropicStableRefreshRequest(ctx context.Context, refreshToken string) (*http.Request, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("stable refresh token is empty")
	}
	payload := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode stable refresh request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, AnthropicStableRefreshURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create stable refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-beta", AnthropicStableOAuthBetaV1)
	return req, nil
}

// --- Allocation-bounded strict JSON scanner ------------------------------

const (
	stableJSONMaxObjectKeys     = 131072
	stableJSONMaxKeyBytes       = 1 << 20
	stableIngressMaxModelBytes  = 1024
	stableIngressMaxUserIDBytes = 4096
)

type stableIngressScanFields struct {
	model                  string
	modelSeen              bool
	maxTokens              int64
	maxTokensSeen          bool
	messagesSeen           bool
	metadataSeen           bool
	metadataUserID         string
	metadataUserIDMapping  []int
	metadataUserIDRawStart int
	metadataUserIDRawEnd   int
	stream                 bool
	hasStream              bool
}

type stableMetadataUserIDFields struct {
	deviceID    string
	accountUUID string
	sessionID   string
	deviceStart int
	deviceEnd   int
	deviceSeen  bool
	accountSeen bool
	sessionSeen bool
}

type stableJSONScanner struct {
	body     []byte
	index    int
	depth    int
	maxDepth int
}

func (p *stableJSONScanner) parseIngressRoot() (*stableIngressScanFields, error) {
	if p == nil || p.maxDepth <= 0 {
		return nil, fmt.Errorf("invalid scanner")
	}
	fields := &stableIngressScanFields{}
	p.skipSpace()
	if p.index >= len(p.body) || p.body[p.index] != '{' {
		return nil, fmt.Errorf("top-level value is not an object")
	}
	if err := p.parseObject(func(key string) error {
		switch key {
		case "model":
			value, _, _, _, err := p.parseBoundedDecodedString(stableIngressMaxModelBytes, false)
			if err != nil || strings.TrimSpace(value) == "" {
				return fmt.Errorf("model must be a bounded non-empty string")
			}
			fields.model = value
			fields.modelSeen = true
			return nil
		case "messages":
			p.skipSpace()
			if p.index >= len(p.body) || p.body[p.index] != '[' {
				return fmt.Errorf("messages must be an array")
			}
			fields.messagesSeen = true
			return p.parseArray()
		case "max_tokens":
			value, err := p.parsePositiveInteger()
			if err != nil {
				return fmt.Errorf("max_tokens must be a positive integer")
			}
			fields.maxTokens = value
			fields.maxTokensSeen = true
			return nil
		case "metadata":
			fields.metadataSeen = true
			return p.parseIngressMetadata(fields)
		case "stream":
			value, err := p.parseBoolean()
			if err != nil {
				return fmt.Errorf("stream must be boolean")
			}
			fields.stream = value
			fields.hasStream = true
			return nil
		default:
			return p.skipValue()
		}
	}); err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.index != len(p.body) {
		return nil, fmt.Errorf("trailing bytes at offset %d", p.index)
	}
	if !fields.modelSeen || !fields.messagesSeen || !fields.metadataSeen || fields.metadataUserID == "" {
		return nil, fmt.Errorf("model, messages, and metadata.user_id are required")
	}
	return fields, nil
}

func (p *stableJSONScanner) parseIngressMetadata(fields *stableIngressScanFields) error {
	p.skipSpace()
	if fields == nil || p.index >= len(p.body) || p.body[p.index] != '{' {
		return fmt.Errorf("metadata must be an object")
	}
	userIDSeen := false
	if err := p.parseObject(func(key string) error {
		if key != "user_id" {
			return p.skipValue()
		}
		value, mapping, rawStart, rawEnd, err := p.parseBoundedDecodedString(stableIngressMaxUserIDBytes, true)
		if err != nil || len(value) == 0 {
			return fmt.Errorf("metadata.user_id must be a bounded string")
		}
		fields.metadataUserID = value
		fields.metadataUserIDMapping = mapping
		fields.metadataUserIDRawStart = rawStart
		fields.metadataUserIDRawEnd = rawEnd
		userIDSeen = true
		return nil
	}); err != nil {
		return err
	}
	if !userIDSeen {
		return fmt.Errorf("metadata.user_id is required")
	}
	return nil
}

func (p *stableJSONScanner) parseMetadataUserID() (*stableMetadataUserIDFields, error) {
	if p == nil || p.maxDepth <= 0 {
		return nil, fmt.Errorf("invalid metadata scanner")
	}
	fields := &stableMetadataUserIDFields{deviceStart: -1, deviceEnd: -1}
	p.skipSpace()
	if p.index >= len(p.body) || p.body[p.index] != '{' {
		return nil, fmt.Errorf("metadata user ID is not an object")
	}
	if err := p.parseObject(func(key string) error {
		value, _, rawStart, rawEnd, err := p.parseDecodedString(false)
		if err != nil {
			return fmt.Errorf("metadata user ID field %q must be a string", key)
		}
		switch key {
		case "device_id":
			fields.deviceID = value
			fields.deviceStart = rawStart
			fields.deviceEnd = rawEnd
			fields.deviceSeen = true
		case "account_uuid":
			fields.accountUUID = value
			fields.accountSeen = true
		case "session_id":
			fields.sessionID = value
			fields.sessionSeen = true
		default:
			return fmt.Errorf("metadata user ID contains unsupported field %q", key)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.index != len(p.body) || !fields.deviceSeen || !fields.accountSeen || !fields.sessionSeen {
		return nil, fmt.Errorf("metadata user ID profile is incomplete")
	}
	return fields, nil
}

func (p *stableJSONScanner) skipValue() error {
	p.skipSpace()
	if p.index >= len(p.body) {
		return fmt.Errorf("unexpected end of input")
	}
	switch p.body[p.index] {
	case '{':
		return p.parseObject(func(string) error { return p.skipValue() })
	case '[':
		return p.parseArray()
	case '"':
		_, err := p.scanStringToken()
		return err
	case 't':
		return p.parseLiteral("true")
	case 'f':
		return p.parseLiteral("false")
	case 'n':
		return p.parseLiteral("null")
	default:
		if p.body[p.index] == '-' || (p.body[p.index] >= '0' && p.body[p.index] <= '9') {
			return p.parseNumber()
		}
	}
	return fmt.Errorf("unexpected byte %q at offset %d", p.body[p.index], p.index)
}

func (p *stableJSONScanner) parseObject(onField func(string) error) error {
	if p == nil || onField == nil || p.index >= len(p.body) || p.body[p.index] != '{' {
		return fmt.Errorf("object expected at offset %d", p.index)
	}
	if p.depth >= p.maxDepth {
		return fmt.Errorf("maximum JSON depth exceeded")
	}
	p.index++
	p.depth++
	defer func() { p.depth-- }()
	p.skipSpace()
	if p.consume('}') {
		return nil
	}
	seen := make(map[string]struct{})
	for {
		p.skipSpace()
		start := p.index
		end, err := p.scanStringToken()
		if err != nil {
			return fmt.Errorf("object key expected at offset %d: %w", start, err)
		}
		if end-start > stableJSONMaxKeyBytes {
			return fmt.Errorf("object key exceeds the size limit")
		}
		key, err := decodeStableJSONString(p.body[start:end])
		if err != nil {
			return err
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: %q", ErrAnthropicStableIngressDuplicateKey, key)
		}
		if len(seen) >= stableJSONMaxObjectKeys {
			return fmt.Errorf("object key count exceeds the limit")
		}
		seen[key] = struct{}{}
		p.skipSpace()
		if !p.consume(':') {
			return fmt.Errorf("object colon expected at offset %d", p.index)
		}
		p.skipSpace()
		before := p.index
		if err := onField(key); err != nil {
			return err
		}
		if p.index <= before {
			return fmt.Errorf("object field parser made no progress at offset %d", before)
		}
		p.skipSpace()
		if p.consume('}') {
			return nil
		}
		if !p.consume(',') {
			return fmt.Errorf("object comma expected at offset %d", p.index)
		}
	}
}

func (p *stableJSONScanner) parseArray() error {
	if p == nil || p.index >= len(p.body) || p.body[p.index] != '[' {
		return fmt.Errorf("array expected at offset %d", p.index)
	}
	if p.depth >= p.maxDepth {
		return fmt.Errorf("maximum JSON depth exceeded")
	}
	p.index++
	p.depth++
	defer func() { p.depth-- }()
	p.skipSpace()
	if p.consume(']') {
		return nil
	}
	for {
		if err := p.skipValue(); err != nil {
			return err
		}
		p.skipSpace()
		if p.consume(']') {
			return nil
		}
		if !p.consume(',') {
			return fmt.Errorf("array comma expected at offset %d", p.index)
		}
		p.skipSpace()
	}
}

func (p *stableJSONScanner) parseBoolean() (bool, error) {
	p.skipSpace()
	if p.index < len(p.body) && bytes.HasPrefix(p.body[p.index:], []byte("true")) {
		return true, p.parseLiteral("true")
	}
	if p.index < len(p.body) && bytes.HasPrefix(p.body[p.index:], []byte("false")) {
		return false, p.parseLiteral("false")
	}
	return false, fmt.Errorf("boolean expected at offset %d", p.index)
}

func (p *stableJSONScanner) parsePositiveInteger() (int64, error) {
	p.skipSpace()
	if p.index >= len(p.body) || p.body[p.index] < '1' || p.body[p.index] > '9' {
		return 0, fmt.Errorf("positive integer expected at offset %d", p.index)
	}
	const maxInt64 = uint64(^uint64(0) >> 1)
	var value uint64
	for p.index < len(p.body) && p.body[p.index] >= '0' && p.body[p.index] <= '9' {
		digit := uint64(p.body[p.index] - '0')
		if value > (maxInt64-digit)/10 {
			return 0, fmt.Errorf("positive integer overflows int64")
		}
		value = value*10 + digit
		p.index++
	}
	return int64(value), nil
}

func (p *stableJSONScanner) parseDecodedString(withMapping bool) (string, []int, int, int, error) {
	p.skipSpace()
	start := p.index
	end, err := p.scanStringToken()
	if err != nil {
		return "", nil, 0, 0, err
	}
	value, err := decodeStableJSONString(p.body[start:end])
	if err != nil {
		return "", nil, 0, 0, err
	}
	if !withMapping {
		return value, nil, start + 1, end - 1, nil
	}
	mapping, err := stableJSONStringByteMapping(p.body, start, end, value)
	if err != nil {
		return "", nil, 0, 0, err
	}
	return value, mapping, start + 1, end - 1, nil
}

func (p *stableJSONScanner) parseBoundedDecodedString(maxDecodedBytes int, withMapping bool) (string, []int, int, int, error) {
	p.skipSpace()
	start := p.index
	end, err := p.scanStringToken()
	if err != nil {
		return "", nil, 0, 0, err
	}
	// A JSON escape consumes at most six source bytes for one decoded byte.
	// Reject oversized tokens before json.Unmarshal can allocate from attacker-
	// controlled input. The decoded limit remains the authoritative bound.
	if maxDecodedBytes <= 0 || end-start-2 > maxDecodedBytes*6 {
		return "", nil, 0, 0, fmt.Errorf("decoded JSON string exceeds the size limit")
	}
	value, err := decodeStableJSONString(p.body[start:end])
	if err != nil || len(value) > maxDecodedBytes {
		return "", nil, 0, 0, fmt.Errorf("decoded JSON string exceeds the size limit")
	}
	if !withMapping {
		return value, nil, start + 1, end - 1, nil
	}
	mapping, err := stableJSONStringByteMapping(p.body, start, end, value)
	if err != nil {
		return "", nil, 0, 0, err
	}
	return value, mapping, start + 1, end - 1, nil
}

func (p *stableJSONScanner) scanStringToken() (int, error) {
	if p == nil || p.index >= len(p.body) || p.body[p.index] != '"' {
		return p.index, fmt.Errorf("string expected at offset %d", p.index)
	}
	start := p.index
	for p.index++; p.index < len(p.body); p.index++ {
		character := p.body[p.index]
		if character == '"' {
			end := p.index + 1
			if !utf8.Valid(p.body[start+1 : p.index]) {
				return p.index, fmt.Errorf("invalid UTF-8 in string")
			}
			p.index = end
			return end, nil
		}
		if character < 0x20 {
			return p.index, fmt.Errorf("control character in string at offset %d", p.index)
		}
		if character != '\\' {
			continue
		}
		if p.index+1 >= len(p.body) {
			return p.index, fmt.Errorf("unterminated escape")
		}
		escape := p.body[p.index+1]
		switch escape {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			p.index++
		case 'u':
			if p.index+5 >= len(p.body) {
				return p.index, fmt.Errorf("short unicode escape")
			}
			for offset := p.index + 2; offset <= p.index+5; offset++ {
				if !isHexByte(p.body[offset]) {
					return p.index, fmt.Errorf("invalid unicode escape")
				}
			}
			p.index += 5
		default:
			return p.index, fmt.Errorf("invalid escape \\%c", escape)
		}
	}
	return len(p.body), fmt.Errorf("unterminated string")
}

func (p *stableJSONScanner) parseLiteral(literal string) error {
	if p.index >= len(p.body) || !bytes.HasPrefix(p.body[p.index:], []byte(literal)) {
		return fmt.Errorf("invalid literal at offset %d", p.index)
	}
	p.index += len(literal)
	return nil
}

func (p *stableJSONScanner) parseNumber() error {
	if p.body[p.index] == '-' {
		p.index++
	}
	if p.index >= len(p.body) {
		return fmt.Errorf("incomplete number")
	}
	if p.body[p.index] == '0' {
		p.index++
	} else {
		if p.body[p.index] < '1' || p.body[p.index] > '9' {
			return fmt.Errorf("invalid number at offset %d", p.index)
		}
		for p.index < len(p.body) && p.body[p.index] >= '0' && p.body[p.index] <= '9' {
			p.index++
		}
	}
	if p.index < len(p.body) && p.body[p.index] == '.' {
		p.index++
		if p.index >= len(p.body) || p.body[p.index] < '0' || p.body[p.index] > '9' {
			return fmt.Errorf("invalid number fraction")
		}
		for p.index < len(p.body) && p.body[p.index] >= '0' && p.body[p.index] <= '9' {
			p.index++
		}
	}
	if p.index < len(p.body) && (p.body[p.index] == 'e' || p.body[p.index] == 'E') {
		p.index++
		if p.index < len(p.body) && (p.body[p.index] == '+' || p.body[p.index] == '-') {
			p.index++
		}
		if p.index >= len(p.body) || p.body[p.index] < '0' || p.body[p.index] > '9' {
			return fmt.Errorf("invalid number exponent")
		}
		for p.index < len(p.body) && p.body[p.index] >= '0' && p.body[p.index] <= '9' {
			p.index++
		}
	}
	return nil
}

func (p *stableJSONScanner) skipSpace() {
	for p.index < len(p.body) {
		switch p.body[p.index] {
		case ' ', '\t', '\n', '\r':
			p.index++
		default:
			return
		}
	}
}

func (p *stableJSONScanner) consume(value byte) bool {
	if p.index < len(p.body) && p.body[p.index] == value {
		p.index++
		return true
	}
	return false
}

func decodeStableJSONString(token []byte) (string, error) {
	var value string
	if len(token) < 2 || json.Unmarshal(token, &value) != nil {
		return "", fmt.Errorf("invalid JSON string")
	}
	return value, nil
}

func stableJSONStringByteMapping(body []byte, start, end int, decoded string) ([]int, error) {
	if start < 0 || end > len(body) || end-start < 2 || body[start] != '"' || body[end-1] != '"' {
		return nil, fmt.Errorf("invalid JSON string mapping range")
	}
	mapping := make([]int, 0, len(decoded))
	for index := start + 1; index < end-1; {
		if body[index] != '\\' {
			_, size := utf8.DecodeRune(body[index : end-1])
			if size <= 0 {
				return nil, fmt.Errorf("invalid UTF-8 mapping")
			}
			for offset := 0; offset < size; offset++ {
				mapping = append(mapping, index+offset)
			}
			index += size
			continue
		}
		escapeStart := index
		if index+1 >= end-1 {
			return nil, fmt.Errorf("invalid escape mapping")
		}
		if body[index+1] != 'u' {
			mapping = append(mapping, escapeStart)
			index += 2
			continue
		}
		segmentEnd := index + 6
		if segmentEnd > end-1 {
			return nil, fmt.Errorf("invalid unicode mapping")
		}
		if stableUnicodeEscapeIsHighSurrogate(body[index:segmentEnd]) &&
			segmentEnd+6 <= end-1 && stableUnicodeEscapeIsLowSurrogate(body[segmentEnd:segmentEnd+6]) {
			segmentEnd += 6
		}
		segment := make([]byte, 0, segmentEnd-index+2)
		segment = append(segment, '"')
		segment = append(segment, body[index:segmentEnd]...)
		segment = append(segment, '"')
		part, err := decodeStableJSONString(segment)
		if err != nil {
			return nil, err
		}
		for range []byte(part) {
			mapping = append(mapping, escapeStart)
		}
		index = segmentEnd
	}
	if len(mapping) != len(decoded) {
		return nil, fmt.Errorf("decoded JSON string mapping length mismatch")
	}
	return mapping, nil
}

func stableUnicodeEscapeIsHighSurrogate(value []byte) bool {
	code, ok := stableUnicodeEscapeValue(value)
	return ok && code >= 0xD800 && code <= 0xDBFF
}

func stableUnicodeEscapeIsLowSurrogate(value []byte) bool {
	code, ok := stableUnicodeEscapeValue(value)
	return ok && code >= 0xDC00 && code <= 0xDFFF
}

func stableUnicodeEscapeValue(value []byte) (uint16, bool) {
	if len(value) != 6 || value[0] != '\\' || value[1] != 'u' {
		return 0, false
	}
	var code uint16
	for _, character := range value[2:] {
		code <<= 4
		switch {
		case character >= '0' && character <= '9':
			code += uint16(character - '0')
		case character >= 'a' && character <= 'f':
			code += uint16(character-'a') + 10
		case character >= 'A' && character <= 'F':
			code += uint16(character-'A') + 10
		default:
			return 0, false
		}
	}
	return code, true
}

func isHexByte(value byte) bool {
	return (value >= '0' && value <= '9') || (value >= 'a' && value <= 'f') || (value >= 'A' && value <= 'F')
}
