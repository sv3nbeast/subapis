package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

const webAgentModelMaxInputBytes = 384 << 10
const webAgentModelMaxResponseBytes = 4 << 20

type WebAgentModelOutput struct {
	Content    string
	Generation *WebAgentGeneration
}
type WebAgentModelCaller interface {
	Generate(context.Context, *WebChatSession, *APIKey, []OpenAIChatMessage) (*WebAgentModelOutput, error)
}
type WebAgentModelClient struct {
	origin string
	http   *http.Client
}

// Only the local authenticated gateway is reachable. No task chooses a base URL
// or provider credential, and this client never retries an ambiguous generation.
func NewWebAgentModelClient(port int) (*WebAgentModelClient, error) {
	if port < 1 || port > 65535 {
		return nil, ErrWebAgentInvalid
	}
	return &WebAgentModelClient{origin: fmt.Sprintf("http://127.0.0.1:%d", port), http: &http.Client{
		Timeout:       4 * time.Minute,
		Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, ResponseHeaderTimeout: 4 * time.Minute, MaxResponseHeaderBytes: 64 << 10, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}
func (c *WebAgentModelClient) Generate(ctx context.Context, session *WebChatSession, key *APIKey, messages []OpenAIChatMessage) (*WebAgentModelOutput, error) {
	if c == nil || session == nil || key == nil || key.Key == "" {
		return nil, ErrWebAgentUnavailable
	}
	path, payload, anthropic, err := buildWebAgentModelRequest(session, messages)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "SubAPIs-WebAgent/1.0")
	result := &WebAgentModelOutput{Generation: &WebAgentGeneration{}}
	if anthropic {
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return result, webAgentModelTransportFailure("model_request_failed", fmt.Errorf("task model request failed: %w", err))
	}
	defer resp.Body.Close()
	result.Generation.RequestID = resp.Header.Get("X-Request-Id")
	// The gateway assigns its own correlation ID for billing. Do not substitute
	// an incoming caller-selected ID or change gateway deduplication semantics.
	if id := resp.Header.Get("X-Client-Request-Id"); len(id) <= 256 {
		result.Generation.ClientRequestID = id
	}
	if result.Generation.RequestID == "" {
		result.Generation.RequestID = resp.Header.Get("Request-Id")
	}
	if len(result.Generation.RequestID) > 256 {
		return nil, webAgentFailure("model_response_invalid", errors.New("invalid task request identifier"))
	}
	if resp.StatusCode != http.StatusOK {
		return result, webAgentFailure(fmt.Sprintf("model_http_%d", resp.StatusCode), fmt.Errorf("task model returned HTTP %d", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, webAgentModelMaxResponseBytes+1))
	if err != nil {
		return result, webAgentModelTransportFailure("model_response_interrupted", err)
	}
	if len(data) > webAgentModelMaxResponseBytes {
		return result, webAgentFailure("output_budget_exceeded", errors.New("task model response exceeds budget"))
	}
	if err = parseWebAgentModelResponse(data, anthropic, result); err != nil {
		return result, webAgentFailure("model_response_invalid", err)
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, nil
}

func webAgentModelTransportFailure(fallback string, err error) error {
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
		return webAgentFailure("model_timeout", err)
	}
	return webAgentFailure(fallback, err)
}

func buildWebAgentModelRequest(session *WebChatSession, messages []OpenAIChatMessage) (string, []byte, bool, error) {
	if session == nil || session.Model == "" || session.MaxOutputTokens < 1 || session.MaxOutputTokens > 32768 || len(messages) == 0 {
		return "", nil, false, ErrWebAgentInvalid
	}
	for _, message := range messages {
		if message.Role != "user" && message.Role != "assistant" {
			return "", nil, false, ErrWebAgentInvalid
		}
	}
	// Match the web chat's provider selection without changing its live adapter.
	ant := session.Platform == PlatformAnthropic || session.Platform == PlatformAntigravity || session.Platform == PlatformGemini
	path := "/v1/chat/completions"
	var payload []byte
	var err error
	if ant {
		path = "/v1/messages"
		converted := make([]apicompat.AnthropicMessage, 0, len(messages))
		for _, message := range messages {
			block := apicompat.AnthropicContentBlock{Type: "text", Text: message.Content}
			if message.Role == "user" {
				block.CacheControl = &apicompat.AnthropicCacheControl{Type: "ephemeral", TTL: "5m"}
			}
			content, _ := json.Marshal([]apicompat.AnthropicContentBlock{block})
			converted = append(converted, apicompat.AnthropicMessage{Role: message.Role, Content: content})
		}
		system, _ := json.Marshal(session.SystemPrompt)
		payload, err = json.Marshal(apicompat.AnthropicRequest{Model: session.Model, MaxTokens: session.MaxOutputTokens, Messages: converted, System: system, Temperature: session.Temperature, Stream: false})
	} else {
		withSystem := append([]OpenAIChatMessage{{Role: "system", Content: session.SystemPrompt}}, messages...)
		body := map[string]any{"model": session.Model, "messages": withSystem, "stream": false, "max_tokens": session.MaxOutputTokens}
		if session.Temperature != nil {
			body["temperature"] = *session.Temperature
		}
		payload, err = json.Marshal(body)
	}
	if err != nil {
		return "", nil, false, err
	}
	if len(payload) > webAgentModelMaxInputBytes {
		return "", nil, false, webAgentFailure("input_budget_exceeded", errors.New("task model input exceeds budget"))
	}
	return path, payload, ant, nil
}

func parseWebAgentModelResponse(data []byte, anthropic bool, out *WebAgentModelOutput) error {
	if out.Generation == nil {
		out.Generation = &WebAgentGeneration{}
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(data, &envelope) != nil || len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return errors.New("invalid task model response")
	}
	if anthropic {
		var response struct {
			Type       string `json:"type"`
			StopReason string `json:"stop_reason"`
			Content    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Usage *struct {
				Input  int64 `json:"input_tokens"`
				Output int64 `json:"output_tokens"`
				Read   int64 `json:"cache_read_input_tokens"`
				Write  int64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(data, &response) != nil {
			return errors.New("invalid task message response")
		}
		if response.Usage != nil {
			out.Generation.Usage = &WebChatUsage{InputTokens: response.Usage.Input, OutputTokens: response.Usage.Output, CacheReadTokens: response.Usage.Read, CacheCreationTokens: response.Usage.Write}
		}
		if response.Type != "message" || response.StopReason != "end_turn" {
			return webAgentFailure("model_incomplete", errors.New("task model did not complete normally"))
		}
		var text strings.Builder
		for _, block := range response.Content {
			switch block.Type {
			case "text":
				text.WriteString(block.Text)
			case "thinking", "redacted_thinking": // Never put private reasoning in the artifact.
			default:
				return webAgentFailure("model_unexpected_tool", errors.New("task model returned an unrequested tool or content type"))
			}
		}
		out.Content = text.String()
	} else {
		var response struct {
			Choices []struct {
				Finish  string `json:"finish_reason"`
				Message struct {
					Content string            `json:"content"`
					Refusal string            `json:"refusal"`
					Tools   []json.RawMessage `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
			Usage *struct {
				Input   int64 `json:"prompt_tokens"`
				Output  int64 `json:"completion_tokens"`
				Details struct {
					Cached int64 `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		if json.Unmarshal(data, &response) != nil {
			return errors.New("invalid task completion response")
		}
		if response.Usage != nil {
			out.Generation.Usage = &WebChatUsage{InputTokens: response.Usage.Input, OutputTokens: response.Usage.Output, CacheReadTokens: response.Usage.Details.Cached}
		}
		if len(response.Choices) != 1 || response.Choices[0].Finish != "stop" {
			return webAgentFailure("model_incomplete", errors.New("task model did not complete normally"))
		}
		message := response.Choices[0].Message
		if message.Refusal != "" || len(message.Tools) > 0 {
			return webAgentFailure("model_declined_or_tool", errors.New("task model declined or returned an unrequested tool"))
		}
		out.Content = message.Content
	}
	u := out.Generation.Usage
	if u != nil && (u.InputTokens < 0 || u.OutputTokens < 0 || u.CacheReadTokens < 0 || u.CacheCreationTokens < 0) {
		out.Generation.Usage = nil
		return errors.New("invalid task model usage")
	}
	if strings.TrimSpace(out.Content) == "" || len(out.Content) > 1<<20 {
		return errors.New("task model returned empty or oversized content")
	}
	return nil
}
