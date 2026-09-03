package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

const (
	grokComposerImageBridgeVisionModel     = "grok-build-0.1"
	grokComposerImageBridgeMaxOutputTokens = 512
	// Keep the legacy service-level alias in lockstep with the shared xAI CLI
	// transport headers used by OAuth probes and Grok forwarding.
	grokCLIVersion                   = xai.CLIClientVersion
	grokDefaultResponsesModel        = "grok-4.5"
	grokRateLimitFallbackCooldown    = 2 * time.Minute
	grokRateLimitRepeatCooldown      = 10 * time.Minute
	grokRateLimitSustainedCooldown   = 30 * time.Minute
	grokRateLimitMaxAdaptiveCooldown = time.Hour
	grokRateLimitBackoffQuietPeriod  = time.Hour
)

func applyGrokCLIHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	headers.Set("User-Agent", defaultGrokUpstreamUserAgent())
	headers.Set("X-Grok-Client-Version", grokCLIVersion)
	headers.Set("X-Grok-Client-Mode", "interactive")
}

// sanitizeGrokResponsesInput promotes Responses Lite additional_tools into
// top-level tools and removes the private carrier rejected by xAI.
func sanitizeGrokResponsesInput(body []byte) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"additional_tools"`)) {
		return body, nil
	}
	input := gjson.GetBytes(body, "input")
	if !input.Exists() || !input.IsArray() {
		return body, nil
	}
	rawItems := input.Array()
	filtered := make([]json.RawMessage, 0, len(rawItems))
	topLevelTools := gjson.GetBytes(body, "tools")
	mergedTools := make([]json.RawMessage, 0)
	seenTools := make(map[string]struct{})
	appendTool := func(tool gjson.Result) bool {
		key := grokResponsesToolDedupKey(tool)
		if _, exists := seenTools[key]; exists {
			return false
		}
		seenTools[key] = struct{}{}
		mergedTools = append(mergedTools, json.RawMessage(tool.Raw))
		return true
	}
	if topLevelTools.IsArray() {
		for _, tool := range topLevelTools.Array() {
			seenTools[grokResponsesToolDedupKey(tool)] = struct{}{}
			mergedTools = append(mergedTools, json.RawMessage(tool.Raw))
		}
	}
	promoted := false
	for _, item := range rawItems {
		if strings.TrimSpace(item.Get("type").String()) == "additional_tools" {
			if tools := item.Get("tools"); tools.IsArray() {
				for _, tool := range tools.Array() {
					if appendTool(tool) {
						promoted = true
					}
				}
			}
			continue
		}
		filtered = append(filtered, json.RawMessage(item.Raw))
	}
	if len(filtered) == len(rawItems) {
		return body, nil
	}
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return nil, err
	}
	body, err = sjson.SetRawBytes(body, "input", encoded)
	if err != nil || !promoted {
		return body, err
	}
	encodedTools, err := json.Marshal(mergedTools)
	if err != nil {
		return nil, err
	}
	return sjson.SetRawBytes(body, "tools", encodedTools)
}

func grokResponsesToolDedupKey(tool gjson.Result) string {
	toolType := strings.TrimSpace(tool.Get("type").String())
	if toolType != "" {
		if name := strings.TrimSpace(tool.Get("name").String()); name != "" {
			return "type:" + toolType + "\x00name:" + name
		}
		if toolType == "mcp" {
			if label := strings.TrimSpace(tool.Get("server_label").String()); label != "" {
				return "type:mcp\x00server_label:" + label
			}
		}
	}
	return "json:" + normalizeCompatSeedJSON(json.RawMessage(tool.Raw))
}

func isGrokCompactionReplayDecodeError(statusCode int, body []byte) bool {
	if (statusCode != http.StatusBadRequest && statusCode != http.StatusUnprocessableEntity) || len(body) == 0 {
		return false
	}
	for _, candidate := range grokStructuredErrorMessageCandidates(body) {
		message := strings.ToLower(candidate)
		decodeSignal := strings.Contains(message, "decode") ||
			strings.Contains(message, "deserialize") ||
			strings.Contains(message, "decoder")
		replaySignal := strings.Contains(message, "compaction") ||
			strings.Contains(message, "summary") ||
			strings.Contains(message, "encrypted_content") ||
			strings.Contains(message, "response history")
		if decodeSignal && replaySignal {
			return true
		}
	}
	return false
}

func sanitizeGrokCompactionReplayBody(body []byte) ([]byte, bool, error) {
	converted, err := convertOpenAICompactInputsForGrok(body)
	if err != nil {
		return nil, false, fmt.Errorf("convert Grok compaction replay: %w", err)
	}
	var requestBody map[string]any
	decoder := json.NewDecoder(bytes.NewReader(converted))
	decoder.UseNumber()
	if err := decoder.Decode(&requestBody); err != nil {
		return nil, false, err
	}

	changed := !bytes.Equal(converted, body)
	if trimOpenAIEncryptedReasoningItems(requestBody) {
		changed = true
	}
	if dropEmptyGrokReplayReasoning(requestBody) {
		changed = true
	}
	if previousID, _ := requestBody["previous_response_id"].(string); strings.TrimSpace(previousID) != "" && !HasFunctionCallOutput(requestBody) {
		delete(requestBody, "previous_response_id")
		if _, exists := requestBody["store"]; !exists {
			requestBody["store"] = false
		}
		changed = true
	}
	if !changed {
		return body, false, nil
	}
	retryBody, err := marshalOpenAIUpstreamJSON(requestBody)
	if err != nil {
		return nil, false, err
	}
	return retryBody, true, nil
}

func dropEmptyGrokReplayReasoning(requestBody map[string]any) bool {
	items, ok := requestBody["input"].([]any)
	if !ok {
		return false
	}
	filtered := items[:0]
	changed := false
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || strings.TrimSpace(grokStringValue(item["type"])) != "reasoning" {
			filtered = append(filtered, rawItem)
			continue
		}
		summary, _ := item["summary"].([]any)
		content, hasContent := item["content"]
		_, hasEncrypted := item["encrypted_content"]
		if hasEncrypted || len(summary) > 0 || (hasContent && content != nil) {
			filtered = append(filtered, rawItem)
			continue
		}
		changed = true
	}
	if changed {
		requestBody["input"] = filtered
	}
	return changed
}

func (s *OpenAIGatewayService) forwardGrokResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	originalModel string,
	reqStream bool,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	clearGrokResponsesClientToolMapping(c)
	if account.Type != AccountTypeOAuth && account.Type != AccountTypeAPIKey {
		return nil, fmt.Errorf("grok account type %s is not supported by forwarding", account.Type)
	}

	upstreamModel := account.GetMappedModel(originalModel)
	if strings.TrimSpace(upstreamModel) == "" {
		upstreamModel = "grok-4.3"
	}
	body, _, err := injectGrokPromptCacheIdentity(c, body, upstreamModel, "responses", grokPromptCacheKeyFromBody(body))
	if err != nil {
		return nil, fmt.Errorf("inject Grok prompt cache identity: %w", err)
	}
	patchedBody, clientToolMapping, err := patchGrokResponsesBodyWithClientTools(body, upstreamModel)
	if err != nil {
		if c != nil && !IsResponseCommitted(c) {
			c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
				"type": "invalid_request_error", "message": err.Error(), "param": "tools",
			}})
		}
		return nil, err
	}
	// xAI does not expose a native compact endpoint on every Grok deployment.
	// Keep the existing Responses transport, but synthesize the compact turn
	// before egress and convert its result back to the Responses compact shape.
	if isOpenAIResponsesCompactPath(c) {
		patchedBody, err = buildGrokCompactRequestBody(patchedBody)
		if err != nil {
			return nil, err
		}
	}
	setGrokResponsesClientToolMapping(c, clientToolMapping)

	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, err
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	upstreamReq, err := buildGrokResponsesRequest(upstreamCtx, c, account, patchedBody, token)
	if err != nil {
		return nil, err
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	resp, _, err = s.retryGrokAfterCredentialRefresh(ctx, account, resp, func(refreshedToken string) (*http.Response, error) {
		token = refreshedToken
		retryReq, buildErr := buildGrokResponsesRequest(upstreamCtx, c, account, patchedBody, refreshedToken)
		if buildErr != nil {
			return nil, buildErr
		}
		return s.httpUpstream.Do(retryReq, proxyURL, account.ID, account.Concurrency)
	})
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	// A compaction/reasoning blob is bound to the xAI response/cache identity
	// that produced it. Recover once by preserving visible history and dropping
	// only opaque replay state that the current decoder cannot consume.
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity {
		firstBody := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		invalidEncryptedContent := isGrokInvalidEncryptedContentResponse(resp.StatusCode, firstBody)
		compactionDecodeError := isGrokCompactionReplayDecodeError(resp.StatusCode, firstBody)
		if invalidEncryptedContent || compactionDecodeError {
			var retryBody []byte
			var changed bool
			var trimErr error
			if invalidEncryptedContent {
				retryBody, changed, trimErr = trimGrokInvalidEncryptedContentRetryBody(patchedBody)
			} else {
				retryBody, changed, trimErr = sanitizeGrokCompactionReplayBody(patchedBody)
			}
			if trimErr != nil {
				return nil, fmt.Errorf("prepare Grok replay decode retry: %w", trimErr)
			}
			if changed {
				retryReq, buildErr := buildGrokResponsesRequest(upstreamCtx, c, account, retryBody, token)
				if buildErr != nil {
					return nil, buildErr
				}
				resp, err = s.httpUpstream.Do(retryReq, proxyURL, account.ID, account.Concurrency)
				SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
				if err != nil {
					return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
				}
				patchedBody = retryBody
				logger.FromContext(ctx).Info("grok.responses_replay_decode_retry",
					zap.Int64("account_id", account.ID),
					zap.String("upstream_error_preview", truncateOpenAIWSLogValue(string(firstBody), 240)),
				)
			} else {
				resp.Body = io.NopCloser(bytes.NewReader(firstBody))
			}
		} else {
			resp.Body = io.NopCloser(bytes.NewReader(firstBody))
		}
	}

	// Codex can replay Responses output items (most notably `reasoning` items and
	// assistant `output_text` content) as the next request's input. OpenAI accepts
	// those output-shaped items, while xAI currently rejects some of them with a
	// 422 ModelInput deserialization error. Keep the normal, already-working path
	// byte-for-byte unchanged and only perform one narrowly-scoped compatibility
	// retry after xAI has positively identified this schema mismatch.
	if resp.StatusCode == http.StatusUnprocessableEntity {
		originalResponseBody := resp.Body
		firstBody := s.readUpstreamErrorBody(resp)
		_ = originalResponseBody.Close()
		retried := false
		if isGrokModelInputSchemaError(firstBody) {
			retryBody, changed, normalizeErr := normalizeGrokResponsesModelInput(patchedBody)
			if normalizeErr != nil {
				logger.FromContext(ctx).Warn("grok.responses_model_input_normalize_failed",
					zap.Int64("account_id", account.ID),
					zap.Error(normalizeErr),
				)
			} else if changed {
				retryReq, buildErr := buildGrokResponsesRequest(upstreamCtx, c, account, retryBody, token)
				if buildErr != nil {
					return nil, buildErr
				}
				resp, err = s.httpUpstream.Do(retryReq, proxyURL, account.ID, account.Concurrency)
				SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
				if err != nil {
					return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
				}
				patchedBody = retryBody
				retried = true
				logger.FromContext(ctx).Info("grok.responses_model_input_compat_retry",
					zap.Int64("account_id", account.ID),
					zap.Int("status_code", resp.StatusCode),
				)
			}
		}
		if !retried {
			resp.Body = io.NopCloser(bytes.NewReader(firstBody))
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		resp.Body = io.NopCloser(bytes.NewReader(respBody))
		s.updateGrokUsageSnapshot(ctx, account.ID, xai.ParseQuotaHeaders(resp.Header, resp.StatusCode))
		upstreamMsg := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(respBody))
		if upstreamMsg == "" {
			upstreamMsg = fmt.Sprintf("xAI upstream returned status %d", resp.StatusCode)
		}
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			ProxyID:            opsUpstreamProxyID(account),
			ProxyName:          opsUpstreamProxyName(account),
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
			Kind:               "failover",
			Message:            upstreamMsg,
		})
		s.handleGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
		if s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody) {
			retryable, retryDelay, retryDeadline, retryMax := grokSameAccountRetryMetadata(account, resp.StatusCode, respBody)
			return nil, &UpstreamFailoverError{
				StatusCode:               resp.StatusCode,
				ResponseBody:             respBody,
				ResponseHeaders:          resp.Header.Clone(),
				RetryableOnSameAccount:   retryable,
				RequestScopedTransient:   retryable && resp.StatusCode == http.StatusTooManyRequests,
				SameAccountRetryDelay:    retryDelay,
				SameAccountRetryDeadline: retryDeadline,
				SameAccountRetryMax:      retryMax,
			}
		}
		return s.handleErrorResponse(ctx, resp, c, account, patchedBody, upstreamModel)
	}

	var usage *OpenAIUsage
	var firstTokenMs *int
	responseID := ""
	searchCount := 0
	imageCount := 0
	var imageOutputSizes []string
	if reqStream {
		maxLineSize := defaultMaxLineSize
		if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
			maxLineSize = s.cfg.Gateway.MaxLineSize
		}
		resp.Body = newGrokResponsesBillingPingFilterBody(resp.Body, account, maxLineSize)
		if hasGrokResponsesClientToolMapping(clientToolMapping) {
			resp.Body = newGrokResponsesClientToolStreamBody(resp.Body, clientToolMapping, maxLineSize)
		}
		streamResult, err := s.handleStreamingResponse(ctx, resp, c, account, startTime, originalModel, upstreamModel)
		if err != nil {
			return nil, err
		}
		usage = streamResult.usage
		firstTokenMs = streamResult.firstTokenMs
		responseID = strings.TrimSpace(streamResult.responseID)
		searchCount = streamResult.searchCount
		imageCount = streamResult.imageCount
		imageOutputSizes = streamResult.imageOutputSizes
	} else {
		nonStreamResult, err := s.handleNonStreamingResponse(ctx, resp, c, account, originalModel, upstreamModel)
		if err != nil {
			return nil, err
		}
		usage = nonStreamResult.usage
		responseID = strings.TrimSpace(nonStreamResult.responseID)
		searchCount = nonStreamResult.searchCount
		imageCount = nonStreamResult.imageCount
		imageOutputSizes = nonStreamResult.imageOutputSizes
	}
	s.commitGrokUpstreamSuccess(ctx, account, resp.Header, resp.StatusCode)

	if usage == nil {
		usage = &OpenAIUsage{}
	}
	reasoningEffort := extractOpenAIReasoningEffortFromBody(patchedBody, originalModel)
	result := &OpenAIForwardResult{
		RequestID:        firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
		UpstreamEndpoint: OpenAIUpstreamEndpointResponses,
		ResponseID:       responseID,
		Usage:            *usage,
		Model:            originalModel,
		UpstreamModel:    upstreamModel,
		ReasoningEffort:  reasoningEffort,
		Stream:           reqStream,
		OpenAIWSMode:     false,
		ResponseHeaders:  resp.Header.Clone(),
		Duration:         time.Since(startTime),
		FirstTokenMs:     firstTokenMs,
		ImageCount:       imageCount,
		ImageOutputSizes: imageOutputSizes,
	}
	if searchCount > 0 {
		result.SearchCount = searchCount
	}
	return result, nil
}

func patchGrokResponsesBody(body []byte, upstreamModel string) ([]byte, error) {
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid json request body")
	}
	// sjson may reuse the input backing array; keep the caller's request bytes
	// unchanged because the same body can be inspected for billing/retry paths.
	out, err := sjson.SetBytes(append([]byte(nil), body...), "model", upstreamModel)
	if err != nil {
		return nil, err
	}
	out, err = normalizeGrokResponsesReasoningEffort(out, upstreamModel)
	if err != nil {
		return nil, err
	}
	for _, unsupportedField := range []string{"prompt_cache_retention", "safety_identifier"} {
		if gjson.GetBytes(out, unsupportedField).Exists() {
			out, err = sjson.DeleteBytes(out, unsupportedField)
			if err != nil {
				return nil, err
			}
		}
	}
	if strings.EqualFold(upstreamModel, "grok-4.5") {
		for _, unsupportedField := range []string{"presence_penalty", "presencePenalty", "frequency_penalty", "frequencyPenalty", "stop"} {
			if gjson.GetBytes(out, unsupportedField).Exists() {
				out, err = sjson.DeleteBytes(out, unsupportedField)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if grokModelRejectsLogprobs(upstreamModel) {
		for _, unsupportedField := range []string{"logprobs", "top_logprobs"} {
			if gjson.GetBytes(out, unsupportedField).Exists() {
				out, err = sjson.DeleteBytes(out, unsupportedField)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	out, err = sanitizeGrokResponsesUnsupportedFields(out)
	if err != nil {
		return nil, err
	}
	out, err = normalizeGrokResponsesResponseFormat(out)
	if err != nil {
		return nil, err
	}
	// The gateway returns synthetic compaction items for /responses/compact.
	// Convert those items back to the reasoning shape xAI accepts when the
	// client replays the compact result on a later Responses turn.
	out, err = convertOpenAICompactInputsForGrok(out)
	if err != nil {
		return nil, err
	}
	out, err = sanitizeGrokResponsesInput(out)
	if err != nil {
		return nil, err
	}
	out, err = sanitizeGrokResponsesModelInput(out)
	if err != nil {
		return nil, err
	}
	out, err = sanitizeGrokResponsesTools(out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func normalizeGrokResponsesResponseFormat(body []byte) ([]byte, error) {
	if !gjson.GetBytes(body, "response_format").Exists() {
		return body, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	var text map[string]json.RawMessage
	if raw := payload["text"]; len(bytes.TrimSpace(raw)) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, fmt.Errorf("invalid Grok text response format: %w", err)
		}
	}
	if text == nil {
		text = make(map[string]json.RawMessage)
	}
	if raw := bytes.TrimSpace(text["format"]); len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		format, err := normalizeGrokLegacyResponseFormat(payload["response_format"])
		if err != nil {
			return nil, err
		}
		text["format"] = format
	}
	encodedText, err := json.Marshal(text)
	if err != nil {
		return nil, err
	}
	payload["text"] = encodedText
	delete(payload, "response_format")
	return json.Marshal(payload)
}

func normalizeGrokLegacyResponseFormat(raw json.RawMessage) (json.RawMessage, error) {
	var format map[string]json.RawMessage
	if err := json.Unmarshal(raw, &format); err != nil {
		return nil, fmt.Errorf("invalid Grok response_format: %w", err)
	}
	var formatType string
	_ = json.Unmarshal(format["type"], &formatType)
	if formatType != "json_schema" || len(bytes.TrimSpace(format["json_schema"])) == 0 {
		return raw, nil
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(format["json_schema"], &schema); err != nil {
		return nil, fmt.Errorf("invalid Grok response_format.json_schema: %w", err)
	}
	result := make(map[string]json.RawMessage, len(schema)+1)
	typeJSON, _ := json.Marshal("json_schema")
	result["type"] = typeJSON
	for key, value := range schema {
		if key != "type" {
			result[key] = value
		}
	}
	return json.Marshal(result)
}

func isGrokModelInputSchemaError(body []byte) bool {
	message := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(body, "error.message").String(),
		gjson.GetBytes(body, "error").String(),
		gjson.GetBytes(body, "message").String(),
		string(body),
	)))
	return strings.Contains(message, "failed to deserialize") &&
		strings.Contains(message, "modelinput")
}

// isGrokInvalidEncryptedContentResponse recognizes xAI's encrypted reasoning
// and compaction replay failures. xAI has emitted several envelopes over time;
// the compaction error notably does not mention the JSON field name, only that
// the blob was decoded/modified.
func isGrokInvalidEncryptedContentResponse(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}

	code := strings.TrimSpace(gjson.GetBytes(body, "code").String())
	if code == "" {
		code = strings.TrimSpace(gjson.GetBytes(body, "error.code").String())
	}
	message := strings.TrimSpace(extractUpstreamErrorMessage(body))
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	normalized := strings.ToLower(message)
	if strings.EqualFold(code, "invalid_encrypted_content") {
		return true
	}
	if code != "" && !strings.EqualFold(code, "invalid-argument") {
		return false
	}
	if strings.Contains(normalized, "compaction blob") {
		return strings.Contains(normalized, "decode") || strings.Contains(normalized, "unmodified")
	}
	return strings.Contains(normalized, "encrypted_content") &&
		(strings.Contains(normalized, "decrypt") ||
			strings.Contains(normalized, "unmodified") ||
			strings.Contains(normalized, "verify"))
}

// requestHasGrokEncryptedContent reports whether an outbound Responses body
// contains provider-owned opaque state that can be removed for one recovery
// attempt. Compaction items are included because xAI rejects the whole item if
// its blob was produced under another account/cache identity.
func requestHasGrokEncryptedContent(body []byte) bool {
	input := gjson.GetBytes(body, "input")
	if !input.Exists() {
		return false
	}
	items := input.Array()
	if input.IsObject() {
		items = []gjson.Result{input}
	}
	for _, item := range items {
		typ := strings.TrimSpace(item.Get("type").String())
		if typ != "reasoning" && typ != "compaction" && typ != "compaction_summary" {
			continue
		}
		if encrypted := item.Get("encrypted_content"); encrypted.Exists() &&
			encrypted.Type != gjson.Null && strings.TrimSpace(encrypted.String()) != "" {
			return true
		}
	}
	return false
}

// trimGrokInvalidEncryptedContentRetryBody removes only provider-owned opaque
// state. Visible messages and tool calls remain intact for the retry.
func trimGrokInvalidEncryptedContentRetryBody(body []byte) ([]byte, bool, error) {
	if !requestHasGrokEncryptedContent(body) {
		return body, false, nil
	}
	var requestBody map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&requestBody); err != nil {
		return nil, false, err
	}
	if !trimOpenAIEncryptedReasoningItems(requestBody) {
		return body, false, nil
	}
	retryBody, err := marshalOpenAIUpstreamJSON(requestBody)
	if err != nil {
		return nil, false, err
	}
	return retryBody, true, nil
}

// stripAnthropicThinkingSignatures removes provider-owned thinking signatures
// from a Claude Messages history before a Grok account switch. A signature is
// account/cache-bound opaque state; retaining it for a different account can
// make an otherwise valid visible conversation fail again with HTTP 400.
func stripAnthropicThinkingSignatures(body []byte) ([]byte, bool) {
	if len(body) == 0 || !bytes.Contains(body, []byte(`"signature"`)) {
		return body, false
	}
	var requestBody map[string]any
	if err := json.Unmarshal(body, &requestBody); err != nil {
		return body, false
	}
	messages, ok := requestBody["messages"].([]any)
	if !ok || len(messages) == 0 {
		return body, false
	}
	changed := false
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		content, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for _, rawBlock := range content {
			block, ok := rawBlock.(map[string]any)
			if !ok || grokCompactStringValue(block["type"]) != "thinking" {
				continue
			}
			if _, exists := block["signature"]; exists {
				delete(block, "signature")
				changed = true
			}
		}
	}
	if !changed {
		return body, false
	}
	stripped, err := json.Marshal(requestBody)
	if err != nil {
		return body, false
	}
	return stripped, true
}

// HasGrokAnthropicThinkingSignatures reports whether a Messages request carries
// an opaque provider signature. It is used before account selection so the
// scheduler can enforce strict sticky routing for this request.
func HasGrokAnthropicThinkingSignatures(body []byte) bool {
	_, changed := stripAnthropicThinkingSignatures(body)
	return changed
}

// StripGrokAnthropicThinkingSignatures removes provider-owned state before a
// request is intentionally moved to another Grok credential. The original
// request body is never mutated.
func StripGrokAnthropicThinkingSignatures(body []byte) ([]byte, bool) {
	return stripAnthropicThinkingSignatures(body)
}

// HasGrokEncryptedState reports whether a Responses request carries an opaque
// reasoning/compaction blob that is only safe on its producing account.
func HasGrokEncryptedState(body []byte) bool {
	return requestHasGrokEncryptedContent(body)
}

// StripGrokEncryptedStateForRouting drops encrypted reasoning/compaction items
// before a deliberate account move. Visible messages and tool calls remain.
func StripGrokEncryptedStateForRouting(body []byte) ([]byte, bool, error) {
	if !requestHasGrokEncryptedContent(body) {
		return body, false, nil
	}
	var requestBody map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&requestBody); err != nil {
		return body, false, err
	}
	if !dropOpenAIEncryptedReasoningInputItems(requestBody) {
		return body, false, nil
	}
	stripped, err := marshalOpenAIUpstreamJSON(requestBody)
	if err != nil {
		return body, false, err
	}
	return stripped, true, nil
}

type grokEncryptedContentStripRetriedKey struct{}

func markGrokEncryptedContentStripRetried(ctx context.Context) context.Context {
	return context.WithValue(ctx, grokEncryptedContentStripRetriedKey{}, true)
}

func grokEncryptedContentStripRetried(ctx context.Context) bool {
	retried, _ := ctx.Value(grokEncryptedContentStripRetriedKey{}).(bool)
	return retried
}

func grokContextWindowClientMessage(upstreamMessage string) string {
	message := strings.TrimSpace(sanitizeUpstreamErrorMessage(upstreamMessage))
	if message == "" {
		return "prompt is too long for the selected model"
	}
	if strings.Contains(strings.ToLower(message), "prompt is too long") {
		return message
	}
	return "prompt is too long: " + message
}

// normalizeGrokResponsesModelInput converts only output-shaped Responses
// history that xAI cannot deserialize back into canonical input-shaped items.
// It is intentionally called only after the exact xAI ModelInput 422 above, so
// successful Grok traffic is never rewritten.
func normalizeGrokResponsesModelInput(body []byte) ([]byte, bool, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false, err
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) == 0 {
		return body, false, nil
	}

	normalized := make([]any, 0, len(input))
	changed := false
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok {
			normalized = append(normalized, rawItem)
			continue
		}

		itemType := strings.ToLower(strings.TrimSpace(grokJSONText(item["type"])))
		if itemType == "reasoning" {
			// Reasoning output is provider-specific opaque state. It is safe to
			// omit when replaying the visible conversation to another provider.
			changed = true
			continue
		}
		if itemType == "item_reference" {
			// A response/item ID issued by OpenAI cannot be resolved by xAI.
			// Codex normally sends the concrete history alongside the reference,
			// so discard only the unusable reference during the fallback retry.
			changed = true
			continue
		}
		if isGrokCodexToolCallType(itemType) {
			item = normalizeGrokCodexToolCall(item, itemType)
			itemType = "function_call"
			changed = true
		} else if isGrokCodexToolOutputType(itemType) {
			item = normalizeGrokCodexToolOutput(item)
			itemType = "function_call_output"
			changed = true
		}
		if itemType == "" && strings.TrimSpace(grokJSONText(item["role"])) != "" {
			item["type"] = "message"
			itemType = "message"
			changed = true
		}

		if itemType == "message" {
			for _, outputOnlyField := range []string{"id", "status"} {
				if _, exists := item[outputOnlyField]; exists {
					delete(item, outputOnlyField)
					changed = true
				}
			}
			if content, ok := item["content"].([]any); ok {
				for _, rawPart := range content {
					part, ok := rawPart.(map[string]any)
					if !ok {
						continue
					}
					if strings.EqualFold(strings.TrimSpace(grokJSONText(part["type"])), "output_text") {
						part["type"] = "input_text"
						changed = true
					}
					for _, outputOnlyField := range []string{"annotations", "logprobs"} {
						if _, exists := part[outputOnlyField]; exists {
							delete(part, outputOnlyField)
							changed = true
						}
					}
				}
			}
		}

		normalized = append(normalized, item)
	}
	if !changed || len(normalized) == 0 {
		return body, false, nil
	}
	payload["input"] = normalized
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func isGrokCodexToolCallType(itemType string) bool {
	switch itemType {
	case "tool_call", "local_shell_call", "tool_search_call", "custom_tool_call", "mcp_tool_call":
		return true
	default:
		return false
	}
}

func isGrokCodexToolOutputType(itemType string) bool {
	switch itemType {
	case "local_shell_call_output", "tool_search_output", "custom_tool_call_output", "mcp_tool_call_output":
		return true
	default:
		return false
	}
}

func normalizeGrokCodexToolCall(item map[string]any, itemType string) map[string]any {
	callID := strings.TrimSpace(grokJSONText(item["call_id"]))
	if callID == "" {
		callID = strings.TrimSpace(grokJSONText(item["id"]))
	}
	name := strings.TrimSpace(grokJSONText(item["name"]))
	if name == "" {
		name = strings.TrimSpace(grokJSONText(item["tool_name"]))
	}
	if name == "" {
		if function, ok := item["function"].(map[string]any); ok {
			name = strings.TrimSpace(grokJSONText(function["name"]))
		}
	}
	if name == "" {
		name = strings.TrimSuffix(itemType, "_call")
	}

	arguments := item["arguments"]
	if arguments == nil {
		arguments = item["input"]
	}
	if arguments == nil {
		arguments = item["action"]
	}
	return map[string]any{
		"type":      "function_call",
		"call_id":   callID,
		"name":      name,
		"arguments": grokJSONString(arguments),
	}
}

func normalizeGrokCodexToolOutput(item map[string]any) map[string]any {
	callID := strings.TrimSpace(grokJSONText(item["call_id"]))
	if callID == "" {
		callID = strings.TrimSpace(grokJSONText(item["id"]))
	}
	output := item["output"]
	if output == nil {
		output = item["result"]
	}
	if _, ok := output.(string); !ok {
		output = grokJSONString(output)
	}
	return map[string]any{
		"type":    "function_call_output",
		"call_id": callID,
		"output":  output,
	}
}

func grokJSONString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func grokJSONText(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// xAI's Grok 4.20 family and newer models do not support OpenAI's logprobs
// fields. Remove them before egress instead of forwarding a request the
// upstream rejects. Older Grok models retain the fields for compatibility.
func grokModelRejectsLogprobs(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if slash := strings.LastIndex(model, "/"); slash >= 0 {
		model = strings.TrimSpace(model[slash+1:])
	}
	return strings.HasPrefix(model, "grok-4.20")
}

func sanitizeGrokResponsesModelCapabilities(body []byte, upstreamModel string) ([]byte, error) {
	if !grokModelRejectsReasoningEffort(upstreamModel) {
		return body, nil
	}

	out := body
	for _, field := range []string{"reasoning", "reasoning_effort", "reasoningEffort"} {
		if !gjson.GetBytes(out, field).Exists() {
			continue
		}
		var err error
		out, err = sjson.DeleteBytes(out, field)
		if err != nil {
			return nil, fmt.Errorf("remove unsupported Grok Composer %s: %w", field, err)
		}
	}
	return out, nil
}

func grokModelRejectsReasoningEffort(model string) bool {
	model = strings.TrimSpace(strings.ToLower(model))
	if slash := strings.LastIndex(model, "/"); slash >= 0 {
		model = strings.TrimSpace(model[slash+1:])
	}
	switch model {
	case "grok-composer", "grok-composer-2.5-fast", "composer-2.5":
		return true
	default:
		return false
	}
}

func normalizeGrokResponsesReasoningEffort(body []byte, upstreamModel string) ([]byte, error) {
	supportsEffort := grokSupportsReasoningEffort(upstreamModel)
	out := body
	var err error
	for _, field := range []string{"reasoning.effort", "reasoning_effort"} {
		value := gjson.GetBytes(out, field)
		if !value.Exists() {
			continue
		}
		normalized, keep := normalizeGrokReasoningEffortValue(value.String(), upstreamModel)
		if !supportsEffort || !keep {
			out, err = sjson.DeleteBytes(out, field)
		} else {
			out, err = sjson.SetBytes(out, field, normalized)
		}
		if err != nil {
			return nil, fmt.Errorf("normalize Grok reasoning field %s: %w", field, err)
		}
	}
	if camel := gjson.GetBytes(out, "reasoningEffort"); camel.Exists() {
		normalized, keep := normalizeGrokReasoningEffortValue(camel.String(), upstreamModel)
		out, err = sjson.DeleteBytes(out, "reasoningEffort")
		if err != nil {
			return nil, fmt.Errorf("remove Grok reasoningEffort: %w", err)
		}
		if supportsEffort && keep && !gjson.GetBytes(out, "reasoning_effort").Exists() {
			out, err = sjson.SetBytes(out, "reasoning_effort", normalized)
			if err != nil {
				return nil, fmt.Errorf("set Grok reasoning_effort: %w", err)
			}
		}
	}
	if reasoning := gjson.GetBytes(out, "reasoning"); reasoning.Exists() && reasoning.IsObject() && len(reasoning.Map()) == 0 {
		out, err = sjson.DeleteBytes(out, "reasoning")
		if err != nil {
			return nil, fmt.Errorf("remove empty Grok reasoning: %w", err)
		}
	}
	return out, nil
}

func normalizeGrokChatReasoningEffort(body []byte, upstreamModel string) ([]byte, error) {
	raw := strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	if raw == "" {
		raw = strings.TrimSpace(gjson.GetBytes(body, "reasoningEffort").String())
	}
	normalized, keep := normalizeGrokReasoningEffortValue(raw, upstreamModel)
	keep = keep && grokSupportsReasoningEffort(upstreamModel)
	out := body
	var err error
	if gjson.GetBytes(out, "reasoningEffort").Exists() {
		out, err = sjson.DeleteBytes(out, "reasoningEffort")
		if err != nil {
			return nil, err
		}
	}
	if !keep {
		if gjson.GetBytes(out, "reasoning_effort").Exists() {
			out, err = sjson.DeleteBytes(out, "reasoning_effort")
		}
		return out, err
	}
	out, err = sjson.SetBytes(out, "reasoning_effort", normalized)
	return out, err
}

func normalizeGrokReasoningEffortValue(raw, model string) (string, bool) {
	value := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(raw)))
	switch value {
	case "none", "low", "medium", "high":
		return value, true
	case "minimal":
		return "low", true
	case "xhigh", "extrahigh":
		if GrokSupportsXHighReasoningEffort(model) {
			return "xhigh", true
		}
		return "high", true
	case "max", "ultra":
		return "high", true
	default:
		return "", false
	}
}

// GrokSupportsXHighReasoningEffort reports whether the model advertises and
// forwards the xhigh reasoning effort (Grok 4.6 and its undated alias).
func GrokSupportsXHighReasoningEffort(model string) bool {
	model = strings.ToLower(xai.StripGrokProviderPrefix(strings.TrimSpace(model)))
	return model == "grok-4.6" || model == "grok-4.6-latest"
}

func grokSupportsReasoningEffort(model string) bool {
	model = strings.ToLower(xai.StripGrokProviderPrefix(strings.TrimSpace(model)))
	switch model {
	case "grok-4.5", "grok-4.5-latest", "grok-4.6", "grok-4.6-latest",
		"grok-4.3", "grok-4.3-latest",
		"grok-3-mini", "grok-3-mini-fast", "grok-4.20-0309-reasoning",
		"grok-4.20-reasoning", "grok-4.20-multi-agent-0309":
		return true
	default:
		return false
	}
}

var grokResponsesUnsupportedRecursiveFields = map[string]struct{}{
	"external_web_access": {},
}

func sanitizeGrokResponsesUnsupportedFields(body []byte) ([]byte, error) {
	if !bytes.Contains(body, []byte(`"external_web_access"`)) {
		return body, nil
	}

	var payload any
	if err := decodeOpenAIJSONUseNumber(body, &payload); err != nil {
		return nil, err
	}
	if !deleteJSONFields(payload, grokResponsesUnsupportedRecursiveFields) {
		return body, nil
	}
	return marshalOpenAIUpstreamJSON(payload)
}

func deleteJSONFields(value any, fields map[string]struct{}) bool {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		for field := range fields {
			if _, ok := typed[field]; ok {
				delete(typed, field)
				changed = true
			}
		}
		for _, child := range typed {
			if deleteJSONFields(child, fields) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, child := range typed {
			if deleteJSONFields(child, fields) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

var grokResponsesSupportedToolTypes = map[string]struct{}{
	"code_execution":     {},
	"code_interpreter":   {},
	"collections_search": {},
	"file_search":        {},
	"function":           {},
	"mcp":                {},
	"shell":              {},
	"web_search":         {},
	"x_search":           {},
}

const grokSafeFunctionParameters = `{"type":"object","properties":{},"additionalProperties":true}`

func sanitizeGrokResponsesTools(body []byte) ([]byte, error) {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() {
		return deleteGrokOrphanToolControls(body)
	}
	if !tools.IsArray() {
		// xAI rejects tool_choice when tools is null/object. Drop the malformed
		// collection and any orphan tool controls instead of forwarding a pair
		// the Grok Responses endpoint cannot interpret.
		body, err := sjson.DeleteBytes(body, "tools")
		if err != nil {
			return nil, err
		}
		return deleteGrokOrphanToolControls(body)
	}

	rawTools := tools.Array()
	filteredTools := make([]json.RawMessage, 0, len(rawTools))
	toolsChanged := false
	for _, tool := range rawTools {
		toolType := strings.TrimSpace(tool.Get("type").String())
		if _, ok := grokResponsesSupportedToolTypes[toolType]; ok {
			raw := json.RawMessage(tool.Raw)
			if toolType == "function" && (!tool.Get("parameters").Exists() || tool.Get("parameters").Type == gjson.Null) {
				var payload map[string]any
				if err := decodeOpenAIJSONUseNumber(raw, &payload); err != nil {
					return nil, err
				}
				payload["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
				encoded, err := marshalOpenAIUpstreamJSON(payload)
				if err != nil {
					return nil, err
				}
				raw = encoded
				toolsChanged = true
			} else if toolType == "function" && grokFunctionParametersHaveInvalidUnionRoot(tool.Get("parameters")) {
				var err error
				raw, err = sjson.SetRawBytes(raw, "parameters", []byte(grokSafeFunctionParameters))
				if err != nil {
					return nil, err
				}
				if strict := tool.Get("strict"); strict.Exists() && strict.Bool() {
					raw, err = sjson.SetBytes(raw, "strict", false)
					if err != nil {
						return nil, err
					}
				}
				toolsChanged = true
			}
			filteredTools = append(filteredTools, raw)
		}
	}
	if !grokRawToolsContainType(filteredTools, "tool_search") {
		for index, raw := range filteredTools {
			if !gjson.GetBytes(raw, "defer_loading").Exists() {
				continue
			}
			cleaned, deleteErr := sjson.DeleteBytes(raw, "defer_loading")
			if deleteErr != nil {
				return nil, deleteErr
			}
			filteredTools[index] = cleaned
			toolsChanged = true
		}
	}

	var err error
	if len(filteredTools) != len(rawTools) || toolsChanged {
		if len(filteredTools) == 0 {
			body, err = sjson.DeleteBytes(body, "tools")
		} else {
			var encoded []byte
			encoded, err = json.Marshal(filteredTools)
			if err != nil {
				return nil, err
			}
			body, err = sjson.SetRawBytes(body, "tools", encoded)
		}
		if err != nil {
			return nil, err
		}
	}
	if len(filteredTools) == 0 {
		return deleteGrokOrphanToolControls(body)
	}

	toolChoice := gjson.GetBytes(body, "tool_choice")
	if !toolChoice.Exists() {
		return body, nil
	}
	if shouldDropGrokToolChoice(toolChoice, filteredTools) {
		body, err = sjson.DeleteBytes(body, "tool_choice")
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

func grokFunctionParametersHaveInvalidUnionRoot(parameters gjson.Result) bool {
	if !parameters.Exists() || !parameters.IsObject() {
		return false
	}
	for _, keyword := range []string{"anyOf", "oneOf"} {
		branches := parameters.Get(keyword)
		if !branches.IsArray() {
			continue
		}
		values := branches.Array()
		if len(values) == 0 {
			continue
		}
		for _, branch := range values {
			if !strings.EqualFold(strings.TrimSpace(branch.Get("type").String()), "object") {
				return true
			}
		}
	}
	return false
}

func grokRawToolsContainType(tools []json.RawMessage, want string) bool {
	for _, tool := range tools {
		if strings.TrimSpace(gjson.GetBytes(tool, "type").String()) == want {
			return true
		}
	}
	return false
}

func deleteGrokOrphanToolControls(body []byte) ([]byte, error) {
	var err error
	for _, field := range []string{"tool_choice", "parallel_tool_calls"} {
		if !gjson.GetBytes(body, field).Exists() {
			continue
		}
		body, err = sjson.DeleteBytes(body, field)
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}

func shouldDropGrokToolChoice(toolChoice gjson.Result, tools []json.RawMessage) bool {
	if len(tools) == 0 {
		return true
	}
	if !toolChoice.IsObject() {
		return false
	}
	choiceType := strings.TrimSpace(toolChoice.Get("type").String())
	if choiceType == "" {
		return false
	}
	if _, ok := grokResponsesSupportedToolTypes[choiceType]; !ok {
		return true
	}
	if choiceType == "function" {
		choiceName := strings.TrimSpace(toolChoice.Get("name").String())
		if choiceName == "" {
			choiceName = strings.TrimSpace(toolChoice.Get("function.name").String())
		}
		if choiceName == "" {
			return false
		}
		for _, tool := range tools {
			var item struct {
				Type     string `json:"type"`
				Name     string `json:"name"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if err := json.Unmarshal(tool, &item); err != nil {
				continue
			}
			name := strings.TrimSpace(item.Name)
			if name == "" {
				name = strings.TrimSpace(item.Function.Name)
			}
			if strings.TrimSpace(item.Type) == "function" && name == choiceName {
				return false
			}
		}
		return true
	}
	return false
}

func (s *OpenAIGatewayService) bridgeGrokComposerImageInputs(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
) ([]byte, OpenAIUsage, bool, error) {
	if !shouldBridgeGrokComposerImageInputs(body) {
		return body, OpenAIUsage{}, false, nil
	}

	var reqBody map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &reqBody); err != nil {
		return body, OpenAIUsage{}, false, fmt.Errorf("parse grok composer image bridge request: %w", err)
	}

	imageURLs := collectGrokComposerImageURLs(reqBody)
	if len(imageURLs) == 0 {
		return body, OpenAIUsage{}, false, nil
	}

	descriptions := make([]string, 0, len(imageURLs))
	var bridgeUsage OpenAIUsage
	for index, imageURL := range imageURLs {
		description, usage, err := s.describeGrokComposerImage(ctx, c, account, token, imageURL, index+1)
		if err != nil {
			return body, bridgeUsage, false, err
		}
		descriptions = append(descriptions, description)
		addOpenAIUsage(&bridgeUsage, usage)
	}

	if !rewriteGrokComposerImagesAsText(reqBody, descriptions) {
		return body, bridgeUsage, false, nil
	}
	bridgedBody, err := marshalOpenAIUpstreamJSON(reqBody)
	if err != nil {
		return body, bridgeUsage, false, fmt.Errorf("serialize grok composer image bridge request: %w", err)
	}
	return bridgedBody, bridgeUsage, true, nil
}

func shouldBridgeGrokComposerImageInputs(body []byte) bool {
	if len(body) == 0 || !isGrokComposerModel(gjson.GetBytes(body, "model").String()) {
		return false
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() {
		return false
	}
	return openAIJSONValueMayContainImageInput(messages)
}

func isGrokComposerModel(model string) bool {
	model = strings.TrimSpace(strings.ToLower(model))
	if model == "" {
		return false
	}
	if strings.Contains(model, "/") {
		parts := strings.Split(model, "/")
		model = strings.TrimSpace(parts[len(parts)-1])
	}
	return strings.Contains(model, "composer")
}

func collectGrokComposerImageURLs(reqBody map[string]any) []string {
	messages, ok := reqBody["messages"].([]any)
	if !ok {
		return nil
	}

	var imageURLs []string
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			if imageURL := grokComposerImageURLFromPart(part); imageURL != "" {
				imageURLs = append(imageURLs, imageURL)
			}
		}
	}
	return imageURLs
}

func grokComposerImageURLFromPart(part any) string {
	partMap, ok := part.(map[string]any)
	if !ok {
		return ""
	}
	if strings.TrimSpace(strings.ToLower(fmt.Sprint(partMap["type"]))) != "image_url" {
		return ""
	}
	switch imageURL := partMap["image_url"].(type) {
	case string:
		return normalizeGrokComposerImageURL(imageURL)
	case map[string]any:
		raw, _ := imageURL["url"].(string)
		return normalizeGrokComposerImageURL(raw)
	default:
		return ""
	}
}

func normalizeGrokComposerImageURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || isEmptyBase64DataURI(trimmed) {
		return ""
	}
	return trimmed
}

func (s *OpenAIGatewayService) describeGrokComposerImage(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	token string,
	imageURL string,
	index int,
) (string, OpenAIUsage, error) {
	body, err := buildGrokComposerImageDescriptionBody(imageURL, index)
	if err != nil {
		return "", OpenAIUsage{}, err
	}

	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	upstreamReq, err := buildGrokResponsesRequest(upstreamCtx, c, account, body, token)
	releaseUpstreamCtx()
	if err != nil {
		return "", OpenAIUsage{}, fmt.Errorf("build grok composer image bridge request: %w", err)
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.doOpenAIUpstream(upstreamReq, proxyURL, account)
	if err != nil {
		return "", OpenAIUsage{}, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody := s.readUpstreamErrorBody(resp)
		s.updateGrokUsageSnapshot(ctx, account.ID, xai.ParseQuotaHeaders(resp.Header, resp.StatusCode))
		upstreamMsg := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(respBody))
		if upstreamMsg == "" {
			upstreamMsg = fmt.Sprintf("xAI image bridge upstream returned status %d", resp.StatusCode)
		}
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			ProxyID:            opsUpstreamProxyID(account),
			ProxyName:          opsUpstreamProxyName(account),
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
			Kind:               "failover",
			Message:            upstreamMsg,
		})
		s.handleGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, grokComposerImageBridgeVisionModel)
		if s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody) {
			retryable, retryDelay, retryDeadline, retryMax := grokSameAccountRetryMetadata(account, resp.StatusCode, respBody)
			return "", OpenAIUsage{}, &UpstreamFailoverError{
				StatusCode:               resp.StatusCode,
				ResponseBody:             respBody,
				ResponseHeaders:          resp.Header.Clone(),
				RetryableOnSameAccount:   retryable,
				RequestScopedTransient:   retryable && resp.StatusCode == http.StatusTooManyRequests,
				SameAccountRetryDelay:    retryDelay,
				SameAccountRetryDeadline: retryDeadline,
				SameAccountRetryMax:      retryMax,
			}
		}
		return "", OpenAIUsage{}, fmt.Errorf("grok composer image bridge upstream error: %s", upstreamMsg)
	}

	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, nil)
	if err != nil {
		return "", OpenAIUsage{}, fmt.Errorf("read grok composer image bridge response: %w", err)
	}

	var parsed apicompat.ResponsesResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", OpenAIUsage{}, fmt.Errorf("parse grok composer image bridge response: %w", err)
	}
	description := strings.TrimSpace(grokResponsesOutputText(&parsed))
	if description == "" {
		return "", copyOpenAIUsageFromResponsesUsage(parsed.Usage), fmt.Errorf("grok composer image bridge returned empty description")
	}
	return description, copyOpenAIUsageFromResponsesUsage(parsed.Usage), nil
}

func buildGrokComposerImageDescriptionBody(imageURL string, index int) ([]byte, error) {
	prompt := fmt.Sprintf("Describe image %d in concise, factual text for a downstream coding/composer model. Include visible text, UI elements, diagrams, errors, and spatial relationships. Do not mention that you are an image analysis bridge.", index)
	req := map[string]any{
		"model":             grokComposerImageBridgeVisionModel,
		"stream":            false,
		"store":             false,
		"max_output_tokens": grokComposerImageBridgeMaxOutputTokens,
		"input": []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": prompt},
					map[string]any{"type": "input_image", "image_url": imageURL},
				},
			},
		},
	}
	return marshalOpenAIUpstreamJSON(req)
}

func grokResponsesOutputText(resp *apicompat.ResponsesResponse) string {
	if resp == nil {
		return ""
	}
	var parts []string
	for _, output := range resp.Output {
		for _, content := range output.Content {
			if content.Type == "output_text" || content.Type == "text" || content.Type == "input_text" {
				if text := strings.TrimSpace(content.Text); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func rewriteGrokComposerImagesAsText(reqBody map[string]any, descriptions []string) bool {
	messages, ok := reqBody["messages"].([]any)
	if !ok {
		return false
	}

	imageIndex := 0
	changed := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := msgMap["content"].([]any)
		if !ok {
			continue
		}
		var textParts []string
		messageChanged := false
		for _, part := range parts {
			if imageURL := grokComposerImageURLFromPart(part); imageURL != "" {
				if imageIndex < len(descriptions) {
					textParts = append(textParts, fmt.Sprintf("Image %d description: %s", imageIndex+1, strings.TrimSpace(descriptions[imageIndex])))
				}
				imageIndex++
				messageChanged = true
				continue
			}
			if text := grokComposerTextFromPart(part); text != "" {
				textParts = append(textParts, text)
			}
		}
		if messageChanged {
			msgMap["content"] = strings.Join(textParts, "\n\n")
			changed = true
		}
	}
	return changed
}

func grokComposerTextFromPart(part any) string {
	partMap, ok := part.(map[string]any)
	if !ok {
		return ""
	}
	partType := strings.TrimSpace(strings.ToLower(fmt.Sprint(partMap["type"])))
	switch partType {
	case "text", "input_text":
		text, _ := partMap["text"].(string)
		return strings.TrimSpace(text)
	default:
		return ""
	}
}

func addOpenAIUsage(dst *OpenAIUsage, usage OpenAIUsage) {
	if dst == nil {
		return
	}
	dst.InputTokens += usage.InputTokens
	dst.ImageInputTokens += usage.ImageInputTokens
	dst.OutputTokens += usage.OutputTokens
	dst.CacheCreationInputTokens += usage.CacheCreationInputTokens
	dst.CacheReadInputTokens += usage.CacheReadInputTokens
	dst.ImageOutputTokens += usage.ImageOutputTokens
}

func buildGrokResponsesRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, token string, options ...any) (*http.Request, error) {
	cacheIdentity := ""
	var cfg *config.Config
	for _, option := range options {
		switch value := option.(type) {
		case string:
			cacheIdentity = value
		case *config.Config:
			cfg = value
		}
	}
	targetURL, err := buildGrokResponsesURL(account, cfg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileGrok))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "sub2api-grok/1.0")
	// Free Build (cli-chat-proxy) requires Grok CLI client headers; api.x.ai does not.
	if account != nil {
		xai.ApplyCLIChatProxyHeaders(req, account.GetGrokBaseURL(), grokCLIRequestMetadata(c, account, body, gjson.GetBytes(body, "model").String()))
		applyGrokCacheHeaders(req.Header, cacheIdentity)
	}
	if c != nil {
		if v := c.GetHeader("OpenAI-Beta"); strings.TrimSpace(v) != "" {
			req.Header.Set("OpenAI-Beta", v)
		}
	}
	account.ApplyHeaderOverrides(req.Header)
	return req, nil
}

func (s *OpenAIGatewayService) updateGrokUsageFromResponse(ctx context.Context, account *Account, headers http.Header, statusCode int) {
	if account == nil {
		return
	}
	if snapshot := xai.ParseQuotaHeaders(headers, statusCode); snapshot != nil {
		s.updateGrokUsageSnapshot(ctx, account.ID, snapshot)
	}
}

func (s *OpenAIGatewayService) updateGrokUsageSnapshot(ctx context.Context, accountID int64, snapshot *xai.QuotaSnapshot) {
	if s == nil || s.accountRepo == nil || accountID <= 0 || snapshot == nil {
		return
	}
	if s.codexSnapshotThrottle != nil && !s.codexSnapshotThrottle.Allow(accountID, time.Now()) {
		return
	}
	_ = s.accountRepo.UpdateExtra(ctx, accountID, map[string]any{
		grokQuotaSnapshotExtraKey: snapshot,
	})
}

func (s *OpenAIGatewayService) handleGrokAccountUpstreamError(ctx context.Context, account *Account, statusCode int, headers http.Header, responseBody []byte, requestedModel ...string) {
	if s == nil || account == nil {
		return
	}
	// Content-policy refusals are scoped to this request, not to the OAuth
	// credential. Do not cool down, mark, or fail over the account for them.
	if isGrokContentPolicyRejection(statusCode, responseBody) {
		return
	}
	if statusCode == http.StatusTooManyRequests && len(requestedModel) > 0 &&
		s.markGrokZeroRPMModelRateLimitIfDetected(ctx, account, requestedModel[0], responseBody) {
		return
	}
	// A provider can advertise an exhausted request/token window in headers
	// even when the body is generic (or when a final successful response used
	// the last token). Persist that signal before body-specific handling so the
	// next scheduler pass cannot select the same credential again.
	if snapshot := xai.ParseQuotaHeaders(headers, statusCode); snapshot != nil {
		s.markGrokQuotaExhaustedFromSnapshot(ctx, account, snapshot)
	}
	// The native Grok forwarding paths handle their own failover error
	// construction and therefore do not pass through the generic OpenAI error
	// side-effect hook. Persist deterministic model-capability failures here so
	// the scheduler excludes this account/model on the next selection.
	if s.rateLimitService != nil && len(requestedModel) > 0 {
		stateCtx, cancel := openAIAccountStateContext(ctx)
		_ = s.rateLimitService.HandleUpstreamModelNotFound(stateCtx, account, requestedModel[0], statusCode, responseBody)
		cancel()
	}
	switch statusCode {
	case http.StatusUnauthorized:
		s.tempUnscheduleGrok(ctx, account, 10*time.Minute, "grok oauth token unauthorized")
	case http.StatusPaymentRequired:
		// Grok free (cli-chat-proxy) 计费额度耗尽以 402
		// personal-team-blocked:spending-limit 返回。必须识别为额度耗尽并限流到
		// 计费周期结束，否则账号会被反复选中重试。
		if s.markGrokQuotaExhaustedIfDetected(ctx, account, responseBody) {
			return
		}
		// 其它 402（真实余额/计费问题）：短暂停调度，等待人工处理。
		s.tempUnscheduleGrok(ctx, account, 30*time.Minute, "grok payment required (402): billing issue")
	case http.StatusForbidden:
		if s.markGrokQuotaExhaustedIfDetected(ctx, account, responseBody) {
			return
		}
		if s.applyGrokForbiddenPolicy(ctx, account, responseBody) {
			return
		}
		if s.rateLimitService != nil {
			stateCtx, cancel := openAIAccountStateContext(ctx)
			defer cancel()
			upstreamMsg := sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(responseBody))
			s.rateLimitService.handleGrok403(stateCtx, account, upstreamMsg, responseBody)
			return
		}
		s.tempUnscheduleGrok(ctx, account, 30*time.Minute, "grok access or entitlement denied")
	case http.StatusTooManyRequests:
		// Free Build reports its rolling 24-hour token allowance exhaustion as
		// 429 subscription:free-usage-exhausted. This is not a short burst limit.
		if s.markGrokQuotaExhaustedIfDetected(ctx, account, responseBody) {
			return
		}
		cooldown := 2 * time.Minute
		if snapshot := xai.ParseQuotaHeaders(headers, statusCode); snapshot != nil && snapshot.RetryAfterSeconds != nil && *snapshot.RetryAfterSeconds > 0 {
			cooldown = time.Duration(*snapshot.RetryAfterSeconds) * time.Second
		}
		s.tempUnscheduleGrok(ctx, account, cooldown, "grok rate limited")
	default:
		if statusCode >= 500 {
			s.tempUnscheduleGrok(ctx, account, 2*time.Minute, "grok upstream temporary error")
		}
	}
}

func (s *OpenAIGatewayService) markGrokZeroRPMModelRateLimitIfDetected(ctx context.Context, account *Account, requestedModel string, responseBody []byte) bool {
	if s == nil || account == nil || account.Platform != PlatformGrok || !isGrokZeroRPMRateLimitResponse(responseBody) {
		return false
	}
	modelKey := modelRateLimitKeyForUpstreamModelNotFound(ctx, account, requestedModel)
	if modelKey == "" {
		return false
	}
	resetAt := time.Now().Add(grokZeroRPMModelCooldown)
	if s.accountRepo == nil {
		slog.Warn("grok_zero_rpm_model_rate_limit_repo_unavailable", "account_id", account.ID, "model", modelKey)
		return true
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	err := s.accountRepo.SetModelRateLimit(stateCtx, account.ID, modelKey, resetAt, grokZeroRPMModelRateLimitReason)
	cancel()
	if err != nil {
		slog.Warn("grok_zero_rpm_set_model_rate_limit_failed", "account_id", account.ID, "model", modelKey, "error", err)
		return true
	}
	slog.Info("grok_zero_rpm_model_rate_limited", "account_id", account.ID, "model", modelKey, "reset_at", resetAt)
	return true
}

// markGrokQuotaExhaustedIfDetected rate-limits a Grok account until its
// applicable quota window resets.
// xAI reports this as a 403 on api.x.ai, a 402 spending-limit error, or a 429
// free-usage-exhausted error on the free Build path. All must stop scheduling
// instead of retrying. Returns true when handled.
func (s *OpenAIGatewayService) markGrokQuotaExhaustedIfDetected(ctx context.Context, account *Account, responseBody []byte) bool {
	if !isGrokQuotaExhausted(account, responseBody) {
		return false
	}
	resetAt := resolveGrokQuotaResetAtForResponse(account, responseBody, time.Now())
	s.markGrokQuotaExhaustedUntil(ctx, account, resetAt)
	return true
}

// markGrokQuotaExhaustedFromSnapshot applies an explicit zero quota window
// observed in response headers. It is intentionally shared by success and
// error paths because a 2xx response may consume the final available token.
func (s *OpenAIGatewayService) markGrokQuotaExhaustedFromSnapshot(ctx context.Context, account *Account, snapshot *xai.QuotaSnapshot) bool {
	if account == nil || account.Platform != PlatformGrok {
		return false
	}
	exhausted, resetAt := isGrokQuotaSnapshotExhausted(snapshot, time.Now())
	if !exhausted {
		return false
	}
	if resetAt.IsZero() {
		resetAt = time.Now().Add(grokQuotaExhaustedFallbackCooldown)
	}
	s.markGrokQuotaExhaustedUntil(ctx, account, resetAt)
	return true
}

func (s *OpenAIGatewayService) markGrokQuotaExhaustedUntil(ctx context.Context, account *Account, resetAt time.Time) {
	if s == nil || account == nil || account.Platform != PlatformGrok {
		return
	}
	now := time.Now()
	if !resetAt.After(now) {
		resetAt = now.Add(grokQuotaExhaustedFallbackCooldown)
	}
	// Never shorten a longer provider window already attached to this account.
	if account.RateLimitResetAt != nil && account.RateLimitResetAt.After(resetAt) {
		resetAt = *account.RateLimitResetAt
	}
	if account.RateLimitResetAt == nil || !account.RateLimitResetAt.Equal(resetAt) {
		rateLimitedAt := now
		account.RateLimitedAt = &rateLimitedAt
		account.RateLimitResetAt = &resetAt
		if s.accountRepo != nil {
			stateCtx, cancel := openAIAccountStateContext(ctx)
			if err := s.accountRepo.SetRateLimited(stateCtx, account.ID, resetAt); err != nil {
				slog.Warn("grok_quota_set_rate_limited_failed", "account_id", account.ID, "error", err)
			}
			// Keep the distributed scheduler snapshot in sync immediately. The
			// outbox worker remains the cross-process fallback, but a local stale
			// snapshot must not hand this account out in the next request.
			if s.schedulerSnapshot != nil {
				if err := s.schedulerSnapshot.UpdateAccountInCache(stateCtx, account); err != nil {
					slog.Debug("grok_quota_scheduler_cache_update_failed", "account_id", account.ID, "error", err)
				}
			}
			cancel()
		}
	} else {
		rateLimitedAt := now
		account.RateLimitedAt = &rateLimitedAt
	}
	s.BlockAccountScheduling(account, resetAt, "grok quota exhausted")
}

func (s *OpenAIGatewayService) tempUnscheduleGrok(ctx context.Context, account *Account, cooldown time.Duration, reason string) {
	if s == nil || account == nil {
		return
	}
	until := time.Now().Add(cooldown)
	if account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(until) {
		until = *account.TempUnschedulableUntil
	}
	s.BlockAccountScheduling(account, until, reason)
	if s.accountRepo != nil {
		stateCtx, cancel := openAIAccountStateContext(ctx)
		defer cancel()
		_ = s.accountRepo.SetTempUnschedulable(stateCtx, account.ID, until, reason)
	}
}

// parseGrokQuotaSnapshot preserves an observation timestamp for 429 responses
// even when xAI omits quota headers.
func parseGrokQuotaSnapshot(headers http.Header, statusCode int, now time.Time) *xai.QuotaSnapshot {
	snapshot := xai.ParseQuotaHeaders(headers, statusCode)
	if snapshot == nil && statusCode == http.StatusTooManyRequests {
		return &xai.QuotaSnapshot{StatusCode: statusCode, UpdatedAt: now.UTC().Format(time.RFC3339)}
	}
	return snapshot
}

// grokTeamRateLimitModelContextKey carries the upstream model for team/model cooldowns.
type grokTeamRateLimitModelContextKey struct{}

func withGrokTeamRateLimitModel(ctx context.Context, model string) context.Context {
	model = strings.TrimSpace(model)
	if model == "" || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, grokTeamRateLimitModelContextKey{}, model)
}

func grokRequestedModelFromCtx(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	model, _ := ctx.Value(grokTeamRateLimitModelContextKey{}).(string)
	return strings.TrimSpace(model)
}

func isGrokSpendingLimitError(responseBody []byte) bool {
	if len(responseBody) == 0 {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(responseBody, "code").String(),
		gjson.GetBytes(responseBody, "error.code").String(),
	)))
	if code == "personal-team-blocked:spending-limit" {
		return true
	}
	message := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		gjson.GetBytes(responseBody, "error").String(),
		gjson.GetBytes(responseBody, "error.message").String(),
		gjson.GetBytes(responseBody, "message").String(),
	)))
	return strings.Contains(message, "spending limit") || strings.Contains(message, "run out of credits")
}

func (s *OpenAIGatewayService) rateLimitGrok(ctx context.Context, account *Account, resetAt time.Time) {
	if s == nil || account == nil {
		return
	}
	now := time.Now()
	resetAt = normalizeGrokRateLimitResetAt(account, resetAt, now)
	runtimeUntil := resetAt
	if account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(runtimeUntil) {
		runtimeUntil = *account.TempUnschedulableUntil
	}
	s.BlockAccountScheduling(account, runtimeUntil, "429")
	persistGrokRateLimit(ctx, s.accountRepo, account, resetAt)
	if model := grokRequestedModelFromCtx(ctx); model != "" {
		markGrokTeamModelRateLimit(account, model, resolveGrokTeamRateLimitUntil(resetAt, now))
	}
}

func isGrokHeavyTransientModel(requestedModel string) bool {
	model := strings.ToLower(strings.TrimSpace(xai.ResolveGrokTextResponsesModelID(requestedModel)))
	return strings.Contains(model, "multi-agent")
}

func persistGrokTransientModelCooldown(account *Account, decision GrokUpstreamFailureDecision) bool {
	if account == nil {
		return false
	}
	model := strings.TrimSpace(decision.Model)
	if model == "" {
		return false
	}
	cooldown := decision.Cooldown
	if cooldown <= 0 {
		cooldown = 3 * time.Minute
	}
	markGrokModelTransientBlock(account.ID, model, time.Now().Add(cooldown))
	return true
}
