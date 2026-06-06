package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	droidFactoryDefaultBaseURL = "https://api.factory.ai/api/llm"
	droidFactoryUserAgent      = "factory-cli/0.32.1"
	droidSystemPrompt          = "You are Droid, an AI software engineering agent built by Factory."
)

type droidEndpointType string

const (
	droidEndpointAnthropic droidEndpointType = "anthropic"
	droidEndpointOpenAI    droidEndpointType = "openai"
	droidEndpointComm      droidEndpointType = "comm"
)

type droidForwardInput struct {
	Endpoint      droidEndpointType
	Body          []byte
	OriginalModel string
	RequestStream bool
	StartTime     time.Time
}

func (s *GatewayService) forwardDroidMessages(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest, startTime time.Time) (*ForwardResult, error) {
	if parsed == nil {
		return nil, fmt.Errorf("droid forward: missing parsed request")
	}
	body := s.prepareDroidRequestBody(parsed.Body, droidEndpointAnthropic, parsed.Model)
	return s.forwardDroid(ctx, c, account, droidForwardInput{
		Endpoint:      droidEndpointAnthropic,
		Body:          body,
		OriginalModel: parsed.Model,
		RequestStream: parsed.Stream,
		StartTime:     startTime,
	})
}

func (s *GatewayService) forwardDroidOpenAI(ctx context.Context, c *gin.Context, account *Account, body []byte, endpoint droidEndpointType, startTime time.Time) (*ForwardResult, error) {
	originalModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	requestStream := gjson.GetBytes(body, "stream").Bool()
	body = s.prepareDroidRequestBody(body, endpoint, originalModel)
	return s.forwardDroid(ctx, c, account, droidForwardInput{
		Endpoint:      endpoint,
		Body:          body,
		OriginalModel: originalModel,
		RequestStream: requestStream,
		StartTime:     startTime,
	})
}

func (s *GatewayService) forwardDroid(ctx context.Context, c *gin.Context, account *Account, input droidForwardInput) (*ForwardResult, error) {
	if account == nil {
		return nil, fmt.Errorf("droid forward: missing account")
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	targetURL, err := s.buildDroidUpstreamURL(account, input.Endpoint)
	if err != nil {
		return nil, err
	}

	resp, err := s.executeDroidUpstream(ctx, c, account, targetURL, proxyURL, input.Body, true)
	if resp == nil || resp.Body == nil {
		return nil, errors.New("droid upstream request failed: empty response")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return s.handleDroidErrorResponse(ctx, c, account, resp)
	}

	var usage ClaudeUsage
	var firstTokenMs *int
	var clientDisconnect bool
	if input.RequestStream {
		streamResult, err := s.streamDroidResponse(c, resp, input.Endpoint, input.StartTime)
		if err != nil {
			return nil, err
		}
		usage = streamResult.usage
		firstTokenMs = streamResult.firstTokenMs
		clientDisconnect = streamResult.clientDisconnect
	} else {
		usage, err = s.bufferDroidResponse(c, resp, input.Endpoint)
		if err != nil {
			return nil, err
		}
	}

	return &ForwardResult{
		RequestID:        getHeaderRaw(resp.Header, "x-request-id"),
		Usage:            usage,
		Model:            input.OriginalModel,
		UpstreamModel:    strings.TrimSpace(gjson.GetBytes(input.Body, "model").String()),
		Stream:           input.RequestStream,
		Duration:         time.Since(input.StartTime),
		FirstTokenMs:     firstTokenMs,
		ClientDisconnect: clientDisconnect,
	}, nil
}

func (s *GatewayService) executeDroidUpstream(ctx context.Context, c *gin.Context, account *Account, targetURL, proxyURL string, body []byte, allowRefreshReplay bool) (*http.Response, error) {
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("droid token is empty")
	}

	req, err := s.buildDroidUpstreamRequest(ctx, c, targetURL, token, body)
	if err != nil {
		return nil, err
	}
	setOpsUpstreamRequestBody(c, body)

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveDroidTLSProfile(account))
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			UpstreamURL:        safeUpstreamURL(targetURL),
			Kind:               "request_error",
			Message:            safeErr,
		})
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Upstream request failed"}})
		return nil, fmt.Errorf("droid upstream request failed: %s", safeErr)
	}

	if allowRefreshReplay && account.Type == AccountTypeOAuth && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && s.droidTokenProvider != nil {
		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr == nil {
			if refreshedToken, refreshErr := s.droidTokenProvider.ForceRefreshAccessToken(ctx, account); refreshErr == nil && strings.TrimSpace(refreshedToken) != "" {
				return s.executeDroidUpstream(ctx, c, account, targetURL, proxyURL, body, false)
			}
		}
		resetHTTPResponseBody(resp, respBody)
	}

	return resp, nil
}

func (s *GatewayService) resolveDroidTLSProfile(account *Account) *tlsfingerprint.Profile {
	if s == nil || s.tlsFPProfileService == nil {
		return nil
	}
	return s.tlsFPProfileService.ResolveTLSProfile(account)
}

func (s *GatewayService) buildDroidUpstreamURL(account *Account, endpoint droidEndpointType) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(account.GetCredential("base_url")), "/")
	if baseURL == "" {
		baseURL = droidFactoryDefaultBaseURL
	}
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	switch endpoint {
	case droidEndpointOpenAI:
		return validatedURL + "/o/v1/responses", nil
	case droidEndpointComm:
		return validatedURL + "/o/v1/chat/completions", nil
	default:
		return validatedURL + "/a/v1/messages", nil
	}
}

func (s *GatewayService) buildDroidUpstreamRequest(ctx context.Context, c *gin.Context, targetURL, token string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if c != nil && c.Request != nil {
		for key, values := range c.Request.Header {
			lowerKey := strings.ToLower(strings.TrimSpace(key))
			if lowerKey == "authorization" || lowerKey == "x-api-key" || lowerKey == "cookie" || lowerKey == "content-length" {
				continue
			}
			for _, v := range values {
				req.Header.Add(key, v)
			}
		}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("X-Factory-Client", "cli")
	applyDroidProviderHeaders(req.Header, body, droidEndpointFromURL(targetURL))
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", droidFactoryUserAgent)
	}
	if req.Header.Get("X-Session-ID") == "" {
		req.Header.Set("X-Session-ID", uuid.NewString())
	}
	return req, nil
}

func (s *GatewayService) prepareDroidRequestBody(body []byte, endpoint droidEndpointType, model string) []byte {
	body = s.applyDroidModelMapping(body, endpoint, model)
	body = stripDroidMetadata(body)
	body = normalizeDroidStreamField(body)
	body = ensureDroidSystemPrompt(body, endpoint)
	body = sanitizeDroidSamplingFields(body)
	return body
}

func (s *GatewayService) applyDroidModelMapping(body []byte, endpoint droidEndpointType, model string) []byte {
	trimmedModel := strings.TrimSpace(model)
	lowerModel := strings.ToLower(trimmedModel)
	switch {
	case endpoint == droidEndpointAnthropic && strings.Contains(lowerModel, "haiku"):
		if trimmedModel != "claude-sonnet-4-20250514" {
			return s.replaceModelInBody(body, "claude-sonnet-4-20250514")
		}
	case endpoint == droidEndpointOpenAI && lowerModel == "gpt-5":
		return s.replaceModelInBody(body, "gpt-5-2025-08-07")
	}
	return body
}

func ensureDroidSystemPrompt(body []byte, endpoint droidEndpointType) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	switch endpoint {
	case droidEndpointOpenAI:
		return ensureDroidInstructionsPrompt(body)
	case droidEndpointComm:
		return ensureDroidChatSystemPrompt(body)
	case droidEndpointAnthropic:
	default:
		return body
	}
	system := gjson.GetBytes(body, "system")
	promptBlock := map[string]any{
		"type": "text",
		"text": droidSystemPrompt,
	}
	if !system.Exists() {
		out, ok := setJSONValueBytes(body, "system", []any{promptBlock})
		if ok {
			return out
		}
		return body
	}
	if system.IsArray() {
		hasPrompt := false
		items := system.Array()
		for _, item := range items {
			if item.Get("type").String() == "text" && item.Get("text").String() == droidSystemPrompt {
				hasPrompt = true
				break
			}
		}
		if hasPrompt {
			return body
		}
		rawItems := make([][]byte, 0, len(items)+1)
		promptJSON, _ := json.Marshal(promptBlock)
		rawItems = append(rawItems, promptJSON)
		for _, item := range items {
			if item.Raw != "" {
				rawItems = append(rawItems, []byte(item.Raw))
			}
		}
		out, err := sjson.SetRawBytes(body, "system", buildJSONArrayRaw(rawItems))
		if err == nil {
			return out
		}
		return body
	}
	out, ok := setJSONValueBytes(body, "system", []any{promptBlock})
	if ok {
		return out
	}
	return body
}

func ensureDroidInstructionsPrompt(body []byte) []byte {
	instructions := gjson.GetBytes(body, "instructions")
	if instructions.Exists() && instructions.Type == gjson.String {
		value := instructions.String()
		if strings.HasPrefix(value, droidSystemPrompt) {
			return body
		}
		out, ok := setJSONValueBytes(body, "instructions", droidSystemPrompt+value)
		if ok {
			return out
		}
		return body
	}
	out, ok := setJSONValueBytes(body, "instructions", droidSystemPrompt)
	if ok {
		return out
	}
	return body
}

func ensureDroidChatSystemPrompt(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body
	}
	items := messages.Array()
	if len(items) == 0 {
		return body
	}
	systemIndex := -1
	for idx, item := range items {
		if item.Get("role").String() == "system" {
			systemIndex = idx
			break
		}
	}
	if systemIndex >= 0 {
		content := items[systemIndex].Get("content")
		if content.Type != gjson.String || strings.HasPrefix(content.String(), droidSystemPrompt) {
			return body
		}
		out, ok := setJSONValueBytes(body, fmt.Sprintf("messages.%d.content", systemIndex), droidSystemPrompt+content.String())
		if ok {
			return out
		}
		return body
	}
	promptMessage := map[string]any{"role": "system", "content": droidSystemPrompt}
	rawItems := make([][]byte, 0, len(items)+1)
	promptJSON, _ := json.Marshal(promptMessage)
	rawItems = append(rawItems, promptJSON)
	for _, item := range items {
		if item.Raw != "" {
			rawItems = append(rawItems, []byte(item.Raw))
		}
	}
	out, err := sjson.SetRawBytes(body, "messages", buildJSONArrayRaw(rawItems))
	if err == nil {
		return out
	}
	return body
}

func stripDroidMetadata(body []byte) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) || !gjson.GetBytes(body, "metadata").Exists() {
		return body
	}
	out, err := sjson.DeleteBytes(body, "metadata")
	if err != nil {
		return body
	}
	return out
}

func normalizeDroidStreamField(body []byte) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	stream := gjson.GetBytes(body, "stream")
	if !stream.Exists() {
		return body
	}
	out, ok := setJSONValueBytes(body, "stream", stream.Bool())
	if ok {
		return out
	}
	return body
}

func sanitizeDroidSamplingFields(body []byte) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	if gjson.GetBytes(body, "temperature").Exists() && gjson.GetBytes(body, "top_p").Exists() {
		out, err := sjson.DeleteBytes(body, "top_p")
		if err == nil {
			return out
		}
	}
	return body
}

func applyDroidProviderHeaders(header http.Header, body []byte, endpoint droidEndpointType) {
	switch endpoint {
	case droidEndpointAnthropic:
		header.Set("Accept", "application/json")
		header.Set("Anthropic-Version", "2023-06-01")
		header.Set("X-Api-Key", "placeholder")
		header.Set("X-Api-Provider", "anthropic")
		if droidThinkingRequested(body) {
			header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")
		}
	case droidEndpointOpenAI:
		header.Set("Accept", "*/*")
		model := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "model").String()))
		if strings.Contains(model, "-max") {
			header.Set("X-Api-Provider", "openai")
		} else {
			header.Set("X-Api-Provider", "azure_openai")
		}
	case droidEndpointComm:
		header.Set("Accept", "*/*")
		header.Set("X-Api-Provider", inferDroidProviderFromModel(gjson.GetBytes(body, "model").String()))
	default:
		header.Set("Accept", "*/*")
	}
}

func droidEndpointFromURL(targetURL string) droidEndpointType {
	switch {
	case strings.Contains(targetURL, "/o/v1/chat/completions"):
		return droidEndpointComm
	case strings.Contains(targetURL, "/o/v1/responses"):
		return droidEndpointOpenAI
	default:
		return droidEndpointAnthropic
	}
}

func droidThinkingRequested(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	thinking := gjson.GetBytes(body, "thinking")
	if !thinking.Exists() {
		return false
	}
	switch thinking.Type {
	case gjson.True:
		return true
	case gjson.String:
		return strings.EqualFold(strings.TrimSpace(thinking.String()), "enabled")
	case gjson.JSON:
		if thinking.Get("enabled").Bool() {
			return true
		}
		return strings.EqualFold(strings.TrimSpace(thinking.Get("type").String()), "enabled")
	default:
		return false
	}
}

func inferDroidProviderFromModel(model string) string {
	lower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(lower, "gemini-") || strings.Contains(lower, "gemini"):
		return "google"
	case strings.HasPrefix(lower, "claude-") || strings.Contains(lower, "claude"):
		return "anthropic"
	case strings.HasPrefix(lower, "gpt-") || strings.Contains(lower, "gpt"):
		return "azure_openai"
	case strings.HasPrefix(lower, "glm-") || strings.Contains(lower, "glm"):
		return "fireworks"
	default:
		return "baseten"
	}
}

type droidStreamResult struct {
	usage            ClaudeUsage
	firstTokenMs     *int
	clientDisconnect bool
}

func (s *GatewayService) streamDroidResponse(c *gin.Context, resp *http.Response, endpoint droidEndpointType, startTime time.Time) (*droidStreamResult, error) {
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "text/event-stream"
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	if requestID := getHeaderRaw(resp.Header, "x-request-id"); requestID != "" {
		c.Header("x-request-id", requestID)
	}
	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	result := &droidStreamResult{}
	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	for scanner.Scan() {
		line := scanner.Text()
		if data, ok := extractAnthropicSSEDataLine(line); ok {
			trimmed := strings.TrimSpace(data)
			if result.firstTokenMs == nil && trimmed != "" && trimmed != "[DONE]" {
				ms := int(time.Since(startTime).Milliseconds())
				result.firstTokenMs = &ms
			}
			s.parseDroidSSEUsage(data, &result.usage, endpoint)
		}
		if _, err := io.WriteString(w, line+"\n"); err != nil {
			result.clientDisconnect = true
			continue
		}
		if line == "" {
			flusher.Flush()
		}
	}
	if err := scanner.Err(); err != nil && !result.clientDisconnect {
		return result, err
	}
	if !result.clientDisconnect {
		flusher.Flush()
	}
	return result, nil
}

func (s *GatewayService) bufferDroidResponse(c *gin.Context, resp *http.Response, endpoint droidEndpointType) (ClaudeUsage, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "upstream_error", "message": "Failed to read upstream response"}})
		return ClaudeUsage{}, err
	}
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	c.Data(http.StatusOK, contentType, body)
	return parseDroidUsageFromBody(body, endpoint), nil
}

func (s *GatewayService) handleDroidErrorResponse(ctx context.Context, c *gin.Context, account *Account, resp *http.Response) (*ForwardResult, error) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  getHeaderRaw(resp.Header, "x-request-id"),
		Kind:               "upstream_error",
		Message:            upstreamMsg,
	})
	if s.shouldFailoverUpstreamError(resp.StatusCode) {
		if s.rateLimitService != nil {
			s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, "")
		}
		return nil, &UpstreamFailoverError{
			StatusCode:   resp.StatusCode,
			ResponseBody: body,
		}
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(mapUpstreamStatusCode(resp.StatusCode), contentType, body)
	return nil, fmt.Errorf("droid upstream error: %d %s", resp.StatusCode, upstreamMsg)
}

func (s *GatewayService) parseDroidSSEUsage(data string, usage *ClaudeUsage, endpoint droidEndpointType) {
	if usage == nil {
		return
	}
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[DONE]" || !gjson.Valid(trimmed) {
		return
	}
	if endpoint == droidEndpointAnthropic {
		s.parseSSEUsagePassthrough(trimmed, usage)
		return
	}
	mergeOpenAIUsageIntoClaude(usage, []byte(trimmed))
}

func parseDroidUsageFromBody(body []byte, endpoint droidEndpointType) ClaudeUsage {
	if endpoint == droidEndpointAnthropic {
		if usage := parseClaudeUsageFromResponseBody(body); usage != nil {
			return *usage
		}
		return ClaudeUsage{}
	}
	var usage ClaudeUsage
	mergeOpenAIUsageIntoClaude(&usage, body)
	return usage
}

func mergeOpenAIUsageIntoClaude(dst *ClaudeUsage, body []byte) {
	if dst == nil || len(body) == 0 || !gjson.ValidBytes(body) {
		return
	}
	candidates := []gjson.Result{
		gjson.GetBytes(body, "usage"),
		gjson.GetBytes(body, "response.usage"),
		gjson.GetBytes(body, "data.usage"),
	}
	for _, usage := range candidates {
		if !usage.Exists() {
			continue
		}
		input := usage.Get("input_tokens").Int()
		if input == 0 {
			input = usage.Get("prompt_tokens").Int()
		}
		output := usage.Get("output_tokens").Int()
		if output == 0 {
			output = usage.Get("completion_tokens").Int()
		}
		total := usage.Get("total_tokens").Int()
		if output == 0 && total > 0 && input >= 0 {
			output = total - input
			if output < 0 {
				output = 0
			}
		}
		cached := usage.Get("input_tokens_details.cached_tokens").Int()
		if cached == 0 {
			cached = usage.Get("prompt_tokens_details.cached_tokens").Int()
		}
		if input > 0 {
			dst.InputTokens = int(input)
		}
		if output > 0 {
			dst.OutputTokens = int(output)
		}
		if cached > 0 {
			dst.CacheReadInputTokens = int(cached)
		}
		return
	}
}
