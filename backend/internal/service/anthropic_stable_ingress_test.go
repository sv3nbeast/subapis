package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	stableTestDeviceA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	stableTestDeviceB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	stableTestSession = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
)

func stableTestBody(device, accountUUID, session string) []byte {
	return []byte(`{"model":"claude-opus-4-6","max_tokens":1024,"messages":[{"role":"user","content":[{"type":"text","text":"keep exact bytes"}]}],"metadata":{"user_id":"{\"device_id\":\"` + device + `\",\"account_uuid\":\"` + accountUUID + `\",\"session_id\":\"` + session + `\"}"},"stream":true,"thinking":{"type":"enabled"},"tools":[{"name":"keep-order","input_schema":{"type":"object"}}]}`)
}

func TestParseAnthropicStableIngressAndEqualLengthPatch(t *testing.T) {
	body := stableTestBody(stableTestDeviceA, "", stableTestSession)
	parsed, err := ParseAnthropicStableIngress(http.MethodPost, "/v1/messages", "", "claude-cli/2.1.222 (external, cli)", body)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-6", parsed.Model)
	require.True(t, parsed.Stream)
	require.Equal(t, stableTestSession, parsed.SessionID)
	require.Equal(t, stableTestDeviceA, parsed.InboundDevice)
	require.Greater(t, parsed.DeviceStart, 0)
	require.Equal(t, parsed.DeviceStart+64, parsed.DeviceEnd)

	patched, err := parsed.PatchDevice(stableTestDeviceB)
	require.NoError(t, err)
	require.Len(t, patched, len(body))
	require.Equal(t, stableTestDeviceB, string(patched[parsed.DeviceStart:parsed.DeviceEnd]))
	require.Equal(t, strings.Replace(string(body), stableTestDeviceA, stableTestDeviceB, 1), string(patched))
	for i := range body {
		if i >= parsed.DeviceStart && i < parsed.DeviceEnd {
			continue
		}
		require.Equal(t, body[i], patched[i], "unexpected mutation at byte %d", i)
	}
}

func TestParseAnthropicStableIngressRejectsProfileDrift(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		encoding  string
		userAgent string
		body      []byte
		want      error
	}{
		{name: "wrong method", method: http.MethodPut, path: "/v1/messages", userAgent: "claude-cli/2.1.222", body: stableTestBody(stableTestDeviceA, "", stableTestSession), want: ErrAnthropicStableIngressNotClaudeCode},
		{name: "wrong path", method: http.MethodPost, path: "/v1/messages?beta=true", userAgent: "claude-cli/2.1.222", body: stableTestBody(stableTestDeviceA, "", stableTestSession), want: ErrAnthropicStableIngressNotClaudeCode},
		{name: "gzip", method: http.MethodPost, path: "/v1/messages", encoding: "gzip", userAgent: "claude-cli/2.1.222", body: stableTestBody(stableTestDeviceA, "", stableTestSession), want: ErrAnthropicStableIngressMalformed},
		{name: "sdk ua", method: http.MethodPost, path: "/v1/messages", userAgent: "anthropic-sdk-go/1.0", body: stableTestBody(stableTestDeviceA, "", stableTestSession), want: ErrAnthropicStableIngressNotClaudeCode},
		{name: "account uuid", method: http.MethodPost, path: "/v1/messages", userAgent: "claude-cli/2.1.222", body: stableTestBody(stableTestDeviceA, "account-uuid", stableTestSession), want: ErrAnthropicStableIngressMalformed},
		{name: "uppercase session", method: http.MethodPost, path: "/v1/messages", userAgent: "claude-cli/2.1.222", body: stableTestBody(stableTestDeviceA, "", strings.ToUpper(stableTestSession)), want: ErrAnthropicStableIngressMalformed},
		{name: "duplicate top-level key", method: http.MethodPost, path: "/v1/messages", userAgent: "claude-cli/2.1.222", body: []byte(`{"model":"a","model":"b","messages":[],"metadata":{"user_id":"{\"device_id\":\"` + stableTestDeviceA + `\",\"account_uuid\":\"\",\"session_id\":\"` + stableTestSession + `\"}"}}`), want: ErrAnthropicStableIngressMalformed},
		{name: "duplicate nested key", method: http.MethodPost, path: "/v1/messages", userAgent: "claude-cli/2.1.222", body: []byte(`{"model":"a","messages":[],"metadata":{"user_id":"{\"device_id\":\"` + stableTestDeviceA + `\",\"device_id\":\"` + stableTestDeviceA + `\",\"account_uuid\":\"\",\"session_id\":\"` + stableTestSession + `\"}"}}`), want: ErrAnthropicStableIngressMalformed},
		{name: "duplicate key in skipped message", method: http.MethodPost, path: "/v1/messages", userAgent: "claude-cli/2.1.222", body: []byte(`{"model":"a","messages":[{"role":"user","role":"assistant"}],"metadata":{"user_id":"{\"device_id\":\"` + stableTestDeviceA + `\",\"account_uuid\":\"\",\"session_id\":\"` + stableTestSession + `\"}"}}`), want: ErrAnthropicStableIngressMalformed},
		{name: "malformed skipped string", method: http.MethodPost, path: "/v1/messages", userAgent: "claude-cli/2.1.222", body: []byte(`{"model":"a","messages":[{"content":"unterminated}],"metadata":{"user_id":"ignored"}}`), want: ErrAnthropicStableIngressMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseAnthropicStableIngress(tt.method, tt.path, tt.encoding, tt.userAgent, tt.body)
			require.Error(t, err)
			require.ErrorIs(t, err, tt.want)
		})
	}
}

func TestParseAnthropicStableIngressAcceptsFieldOrderAndPreservesRawBody(t *testing.T) {
	body := []byte(`{"metadata":{"source":"cli","user_id":"{\"session_id\":\"` + stableTestSession + `\",\"device_id\":\"` + stableTestDeviceA + `\",\"account_uuid\":\"\"}"},"messages":[{"content":"世界 🌍","role":"user"}],"stream":false,"model":"claude-opus-4-6","tools":[{"input_schema":{"type":"object"},"name":"read"}]}`)

	parsed, err := ParseAnthropicStableIngress(http.MethodPost, "/v1/messages", "identity", "claude-cli/2.1.222", body)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-6", parsed.Model)
	require.True(t, parsed.HasStream)
	require.False(t, parsed.Stream)
	require.Equal(t, stableTestSession, parsed.SessionID)
	require.Equal(t, string(body), string(parsed.RawBody))
	require.Equal(t, &body[0], &parsed.RawBody[0], "parser must retain the original body without copying it")
}

func TestParseAnthropicStableIngressLargeContentHasBoundedAllocations(t *testing.T) {
	content := strings.Repeat("x", 4<<20)
	body := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":"` + content + `"}],"metadata":{"user_id":"{\"device_id\":\"` + stableTestDeviceA + `\",\"account_uuid\":\"\",\"session_id\":\"` + stableTestSession + `\"}"},"stream":true}`)

	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			parsed, err := ParseAnthropicStableIngress(http.MethodPost, "/v1/messages", "", "claude-cli/2.1.222", body)
			if err != nil {
				b.Fatal(err)
			}
			if len(parsed.RawBody) != len(body) {
				b.Fatal("raw body length changed")
			}
		}
	})
	require.Less(t, result.AllocedBytesPerOp(), int64(512<<10), "large content must not be materialized as a JSON AST")
}

func TestParseAnthropicStableIngressRejectsEscapedDevice(t *testing.T) {
	body := stableTestBody(stableTestDeviceA, "", stableTestSession)
	// An escaped hex digit changes the raw byte layout and cannot be replaced
	// while preserving the reference request byte-for-byte.
	needle := `device_id\":\"` + stableTestDeviceA
	require.Contains(t, string(body), needle)
	body = []byte(strings.Replace(string(body), needle, `device_id\":\"\\u0061`+stableTestDeviceA[1:], 1))
	_, err := ParseAnthropicStableIngress(http.MethodPost, "/v1/messages", "", "claude-cli/2.1.222", body)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAnthropicStableIngressMalformed)
}

func TestParseAnthropicStableIngressRejectsBillingCCHButAllowsPromptText(t *testing.T) {
	billing := []byte(`{"model":"claude-opus-4-6","system":[{"type":"text","text":"x-anthropic-billing-header: cch=00000"}],"messages":[],"metadata":{"user_id":"{\"device_id\":\"` + stableTestDeviceA + `\",\"account_uuid\":\"\",\"session_id\":\"` + stableTestSession + `\"}"},"stream":true}`)
	_, err := ParseAnthropicStableIngress(http.MethodPost, "/v1/messages", "", "claude-cli/2.1.222", billing)
	require.ErrorIs(t, err, ErrAnthropicStableIngressCCHPresent)

	prompt := []byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":[{"type":"text","text":"please explain cch=00000 literally"}]}],"metadata":{"user_id":"{\"device_id\":\"` + stableTestDeviceA + `\",\"account_uuid\":\"\",\"session_id\":\"` + stableTestSession + `\"}"},"stream":true}`)
	_, err = ParseAnthropicStableIngress(http.MethodPost, "/v1/messages", "", "claude-cli/2.1.222", prompt)
	require.NoError(t, err)
}

func TestParseAnthropicStableIngressProfileRequiresExactCapturedTuple(t *testing.T) {
	body := stableTestBody(stableTestDeviceA, "", stableTestSession)
	profile := anthropicStableIngressProfiles[AnthropicStableIngressProfileCLI211222V1]
	_, err := ParseAnthropicStableIngressProfile(
		http.MethodPost, "/v1/messages", AnthropicStableIngressQueryV1, "identity",
		profile.userAgent, AnthropicStableIngressXAppV1, stableTestSession,
		profile.beta, AnthropicStableIngressAPIVersionV1,
		AnthropicStableIngressProfileCLI211222V1, body,
	)
	require.NoError(t, err)

	_, err = ParseAnthropicStableIngressProfile(
		http.MethodPost, "/v1/messages", "beta=true&x=1", "identity",
		profile.userAgent, AnthropicStableIngressXAppV1, stableTestSession,
		profile.beta, AnthropicStableIngressAPIVersionV1,
		AnthropicStableIngressProfileCLI211222V1, body,
	)
	require.ErrorIs(t, err, ErrAnthropicStableIngressNotClaudeCode)
}

func TestAnthropicStableIngressAcceptsOnlyCapturedCLI211222BetaCohort(t *testing.T) {
	body := stableTestBody(stableTestDeviceA, "", stableTestSession)
	profile := anthropicStableIngressProfiles[AnthropicStableIngressProfileCLI211222V1]
	variants := []string{
		AnthropicStableIngressBetaCLI211222BaseV1,
		AnthropicStableIngressBetaCLI211222AgenticV1,
		AnthropicStableIngressBetaCLI211222FullV1,
	}
	for _, beta := range variants {
		t.Run(beta, func(t *testing.T) {
			require.Equal(t, AnthropicStableIngressProfileCLI211222V1,
				DetectAnthropicStableIngressProfile(profile.userAgent, beta))
			_, err := ParseAnthropicStableIngressProfile(
				http.MethodPost, "/v1/messages", AnthropicStableIngressQueryV1, "identity",
				profile.userAgent, AnthropicStableIngressXAppV1, stableTestSession,
				beta, AnthropicStableIngressAPIVersionV1,
				AnthropicStableIngressProfileCLI211222V1, body,
			)
			require.NoError(t, err)
		})
	}

	// A one-token change is not a compatible version upgrade. It must fail
	// closed until a new capture is reviewed and explicitly added to the
	// cohort.
	unknown := variants[0] + ",effort-2025-11-24"
	require.Empty(t, DetectAnthropicStableIngressProfile(profile.userAgent, unknown))
	_, err := ParseAnthropicStableIngressProfile(
		http.MethodPost, "/v1/messages", AnthropicStableIngressQueryV1, "identity",
		profile.userAgent, AnthropicStableIngressXAppV1, stableTestSession,
		unknown, AnthropicStableIngressAPIVersionV1,
		AnthropicStableIngressProfileCLI211222V1, body,
	)
	require.ErrorIs(t, err, ErrAnthropicStableIngressNotClaudeCode)
}

func TestAnthropicStableIngressSDKCLI211222AcceptsOnlyCapturedVariants(t *testing.T) {
	profile := anthropicStableIngressProfiles[AnthropicStableIngressProfileSDKCLI211222V1]
	for _, beta := range []string{
		AnthropicStableIngressBetaSDKCLI211222V1,
		AnthropicStableIngressBetaSDKCLI211222EffortV1,
		AnthropicStableIngressBetaSDKCLI211222AgenticV1,
	} {
		require.Equal(t, AnthropicStableIngressProfileSDKCLI211222V1,
			DetectAnthropicStableIngressProfile(profile.userAgent, beta))
	}
	require.Empty(t, DetectAnthropicStableIngressProfile(
		profile.userAgent, AnthropicStableIngressBetaCLI211222BaseV1,
	))
	require.Empty(t, DetectAnthropicStableIngressProfile(
		profile.userAgent, AnthropicStableIngressBetaSDKCLI211222AgenticV1+",structured-outputs-2025-12-15",
	))
}

func TestAnthropicStableIdentityIngressAcceptsNativeClientVersionsWithoutBetaAdmission(t *testing.T) {
	body := stableTestBody(stableTestDeviceA, "", stableTestSession)
	for _, userAgent := range []string{
		"claude-cli/2.1.222 (external, cli)",
		"claude-cli/2.1.223 (external, cli)",
		"claude-cli/3.0.0 (external, sdk-cli)",
	} {
		t.Run(userAgent, func(t *testing.T) {
			require.Equal(t, AnthropicStableIngressProfileClaudeCLICustomBaseV1,
				DetectAnthropicStableIdentityIngressProfile(userAgent))
			result, err := ParseAnthropicStableIdentityIngress(
				http.MethodPost, "/v1/messages", AnthropicStableIngressQueryV1, "identity",
				userAgent, AnthropicStableIngressXAppV1, stableTestSession, body,
			)
			require.NoError(t, err)
			require.Equal(t, AnthropicStableIngressProfileClaudeCLICustomBaseV1, result.ProfileID)
		})
	}

	for _, userAgent := range []string{
		"claude-cli/2.1.223",
		"claude-cli/2.1.223 (external, desktop)",
		"anthropic-sdk-go/1.0",
	} {
		require.Empty(t, DetectAnthropicStableIdentityIngressProfile(userAgent))
	}
}

func TestAnthropicStableIdentityIngressDoesNotInspectFeatureHeaders(t *testing.T) {
	body := stableTestBody(stableTestDeviceA, "", stableTestSession)
	userAgent := "claude-cli/2.1.222 (external, cli)"

	// anthropic-beta and anthropic-version are intentionally absent from this
	// parser's inputs. The shared identity route preserves them at request-build
	// time rather than treating feature negotiation as client admission.
	result, err := ParseAnthropicStableIdentityIngress(
		http.MethodPost, "/v1/messages", AnthropicStableIngressQueryV1, "identity",
		userAgent, AnthropicStableIngressXAppV1, stableTestSession, body,
	)
	require.NoError(t, err)
	require.Equal(t, stableTestSession, result.SessionID)
}

func TestAnthropicStableIdentityIngressRetainsSessionAndNativeShapeGuards(t *testing.T) {
	body := stableTestBody(stableTestDeviceA, "", stableTestSession)
	userAgent := "claude-cli/2.1.223 (external, cli)"

	_, err := ParseAnthropicStableIdentityIngress(
		http.MethodPost, "/v1/messages", "beta=true&extra=1", "identity",
		userAgent, AnthropicStableIngressXAppV1, stableTestSession, body,
	)
	require.ErrorIs(t, err, ErrAnthropicStableIngressNotClaudeCode)

	_, err = ParseAnthropicStableIdentityIngress(
		http.MethodPost, "/v1/messages", AnthropicStableIngressQueryV1, "identity",
		userAgent, AnthropicStableIngressXAppV1, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", body,
	)
	require.ErrorIs(t, err, ErrAnthropicStableIngressMalformed)

	nonStreaming := []byte(strings.Replace(string(body), `"stream":true`, `"stream":false`, 1))
	_, err = ParseAnthropicStableIdentityIngress(
		http.MethodPost, "/v1/messages", AnthropicStableIngressQueryV1, "identity",
		userAgent, AnthropicStableIngressXAppV1, stableTestSession, nonStreaming,
	)
	require.ErrorIs(t, err, ErrAnthropicStableIngressMalformed)
}

func TestAnthropicStableIngressAcceptsDurableCustomBaseAlias(t *testing.T) {
	body := stableTestBody(stableTestDeviceA, "", stableTestSession)
	profile := anthropicStableIngressProfiles[AnthropicStableIngressProfileCLI211222V1]
	_, err := ParseAnthropicStableIngressProfile(
		http.MethodPost, "/v1/messages", AnthropicStableIngressQueryV1, "identity",
		profile.userAgent, AnthropicStableIngressXAppV1, stableTestSession,
		AnthropicStableIngressBetaCLI211222BaseV1, AnthropicStableIngressAPIVersionV1,
		AnthropicStableIngressProfileClaudeCLICustomBaseV1, body,
	)
	require.NoError(t, err)
	require.True(t, AnthropicStableIngressProfilesEquivalent(
		AnthropicStableIngressProfileCLI211222V1,
		AnthropicStableIngressProfileClaudeCLICustomBaseV1,
	))
	require.True(t, AnthropicStableIngressProfilesEquivalent(
		AnthropicStableIngressProfileCLI211222V1,
		AnthropicStableIngressProfileSDKCLI211222V1,
	), "cli and sdk-cli are finite request variants of the same captured installation family")
}

func TestParseAnthropicStableIngressProfileRequiresStreamAndPositiveMaxTokens(t *testing.T) {
	profile := anthropicStableIngressProfiles[AnthropicStableIngressProfileCLI211222V1]
	base := stableTestBody(stableTestDeviceA, "", stableTestSession)
	tests := []struct {
		name string
		body []byte
	}{
		{name: "missing stream", body: []byte(strings.Replace(string(base), `,"stream":true`, "", 1))},
		{name: "stream false", body: []byte(strings.Replace(string(base), `"stream":true`, `"stream":false`, 1))},
		{name: "missing max tokens", body: []byte(strings.Replace(string(base), `"max_tokens":1024,`, "", 1))},
		{name: "zero max tokens", body: []byte(strings.Replace(string(base), `"max_tokens":1024`, `"max_tokens":0`, 1))},
		{name: "fractional max tokens", body: []byte(strings.Replace(string(base), `"max_tokens":1024`, `"max_tokens":1.5`, 1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseAnthropicStableIngressProfile(
				http.MethodPost, "/v1/messages", AnthropicStableIngressQueryV1, "identity",
				profile.userAgent, AnthropicStableIngressXAppV1, stableTestSession,
				profile.beta, AnthropicStableIngressAPIVersionV1,
				AnthropicStableIngressProfileCLI211222V1, tt.body,
			)
			require.ErrorIs(t, err, ErrAnthropicStableIngressMalformed)
		})
	}
}

func TestBuildAnthropicStableMessageRequestMatchesReferenceHeaderPolicy(t *testing.T) {
	header := make(http.Header)
	header.Set("Authorization", "Bearer inbound")
	header.Set("x-api-key", "inbound-api-key")
	header.Set("Cookie", "session=secret")
	header.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20")
	// The reference leaves User-Agent unset, allowing net/http's implicit
	// Go-http-client/1.1 identity to be selected by the transport.
	req, err := BuildAnthropicStableMessageRequest(context.Background(), AnthropicStableMessagesOriginV1, header, []byte(`{"messages":[]}`), "upstream-token")
	require.NoError(t, err)
	require.Equal(t, "https://api.anthropic.com/v1/messages", req.URL.String())
	require.Equal(t, http.MethodPost, req.Method)
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.Equal(t, "Bearer upstream-token", req.Header.Get("Authorization"))
	require.Empty(t, req.Header.Get("x-api-key"))
	require.Empty(t, req.Header.Get("Cookie"))
	require.Empty(t, req.Header.Get("User-Agent"))
	require.Equal(t, "claude-code-20250219,oauth-2025-04-20", req.Header.Get("anthropic-beta"))
	require.Equal(t, AnthropicStableDefaultAPIVersionV1, req.Header.Get("anthropic-version"))

	_, err = BuildAnthropicStableMessageRequest(context.Background(), "https://api.anthropic.com?ignored=1", header, []byte(`{"messages":[]}`), "upstream-token")
	require.Error(t, err, "stable request construction must reject query-bearing or alternate origins")
	_, err = BuildAnthropicStableMessageRequest(context.Background(), "https://example.com", header, []byte(`{"messages":[]}`), "upstream-token")
	require.Error(t, err)
}

func TestBuildAnthropicStableRefreshRequestUsesReferencePayloadOrder(t *testing.T) {
	req, err := BuildAnthropicStableRefreshRequest(context.Background(), "refresh-token")
	require.NoError(t, err)
	require.Equal(t, AnthropicStableRefreshURL, req.URL.String())
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.Equal(t, AnthropicStableOAuthBetaV1, req.Header.Get("anthropic-beta"))
	var body []byte
	body, err = readRequestBodyForStableTest(req)
	require.NoError(t, err)
	require.Equal(t, `{"client_id":"9d1c250a-e61b-44d9-88ed-5944d1962f5e","grant_type":"refresh_token","refresh_token":"refresh-token"}`, string(body))
}

func readRequestBodyForStableTest(req *http.Request) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, nil
	}
	defer req.Body.Close()
	return io.ReadAll(req.Body)
}
