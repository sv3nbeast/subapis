package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

type gatewayResponsesWSBridgeOutcome struct {
	result *ForwardResult
	err    error
}

// isOpenAIResponsesGenerateFalse reports only an explicit JSON boolean false.
// A missing generate field is the normal business request and must continue to
// Kiro. Invalid/non-boolean values remain on the ordinary validation path.
func isOpenAIResponsesGenerateFalse(payload []byte) bool {
	generate := gjson.GetBytes(payload, "generate")
	return generate.Exists() && generate.Type == gjson.False
}

// completeKiroResponsesPrewarm emulates the Responses WebSocket
// generate=false contract locally. OpenAI uses this frame to establish request
// context without inference; forwarding it to Kiro incorrectly starts a real
// model request that deterministically ends as a 2xx zero-frame EOF for Codex's
// large tool/system prewarm payload.
func (s *GatewayService) completeKiroResponsesPrewarm(
	c *gin.Context,
	account *Account,
	payload []byte,
	parsed *ParsedRequest,
	writeClientMessage func([]byte) error,
) (*ForwardResult, error) {
	startedAt := time.Now()
	body, err := prepareOpenAIWSHTTPBridgeBodyWithPreviousResponseID(payload, true)
	if err != nil {
		return nil, fmt.Errorf("prepare Kiro Responses prewarm body: %w", err)
	}
	normalizedBody, toolMetadata, err := normalizeKiroCodexResponsesTools(body)
	if err != nil {
		return nil, fmt.Errorf("normalize Kiro Responses prewarm tools: %w", err)
	}
	var request apicompat.ResponsesRequest
	if err := json.Unmarshal(normalizedBody, &request); err != nil {
		return nil, fmt.Errorf("parse Kiro Responses prewarm request: %w", err)
	}
	model := strings.TrimSpace(request.Model)
	if !IsOpenAIKiroBridgeModel(model) {
		return nil, fmt.Errorf("unsupported Kiro Responses prewarm model: %s", model)
	}

	responseID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	response := map[string]any{
		"id":     responseID,
		"object": "response",
		"model":  model,
		"status": "completed",
		"output": []any{},
		"usage": map[string]any{
			"input_tokens":  0,
			"output_tokens": 0,
			"total_tokens":  0,
		},
	}
	event, err := json.Marshal(map[string]any{
		"type":            "response.completed",
		"sequence_number": 0,
		"response":        response,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal Kiro Responses prewarm completion: %w", err)
	}

	result := &ForwardResult{
		RequestID:        responseID,
		ResponseID:       responseID,
		Model:            model,
		Stream:           true,
		Duration:         time.Since(startedAt),
		SyntheticPrewarm: true,
	}
	if writeErr := writeClientMessage(event); writeErr != nil {
		if isOpenAIWSClientDisconnectError(writeErr) {
			result.ClientDisconnect = true
			return result, nil
		}
		return result, fmt.Errorf("write Kiro Responses prewarm completion: %w", writeErr)
	}

	// Codex may reference the synthetic response ID on a later connection.
	// Retain the normalized (declaration carrier removed) context even when
	// store=false: this is protocol continuation state, not billable model work.
	scope := kiroResponsesScopeForRequest(c, parsed)
	globalKiroResponsesHistoryStore.saveEphemeral(kiroResponsesHistoryEntry{
		ID:                 responseID,
		PreviousResponseID: request.PreviousResponseID,
		Model:              model,
		Instructions:       request.Instructions,
		Input:              append(json.RawMessage(nil), request.Input...),
		APIKeyID:           scope.APIKeyID,
		GroupID:            scope.GroupID,
	})
	logger.L().Debug("kiro.responses_synthetic_prewarm_completed",
		zap.Int64("account_id", account.ID),
		zap.String("response_id", responseID),
		zap.Int("payload_bytes", len(payload)),
		zap.Int("declared_tool_count", toolMetadata.DeclaredToolCount),
		zap.Int("forwarded_tool_count", toolMetadata.ForwardedToolCount),
	)
	return result, nil
}

// ForwardAsResponsesWebSocketTurn runs the existing Responses compatibility
// pipeline while relaying each generated SSE data frame to a WebSocket client.
// The pipe is always drained through provider completion, even after the client
// disconnects, so Kiro stream cleanup cannot cancel a terminal event early.
func (s *GatewayService) ForwardAsResponsesWebSocketTurn(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	payload []byte,
	parsed *ParsedRequest,
	preservePreviousResponseID bool,
	writeClientMessage func([]byte) error,
) (*ForwardResult, error) {
	if s == nil {
		return nil, errors.New("gateway service is nil")
	}
	if c == nil {
		return nil, errors.New("gin context is nil")
	}
	if account == nil {
		return nil, errors.New("account is nil")
	}
	if parsed == nil {
		return nil, errors.New("parsed request is nil")
	}
	if writeClientMessage == nil {
		return nil, errors.New("client websocket writer is nil")
	}
	if account.Platform == PlatformKiro &&
		IsOpenAIKiroBridgeModel(strings.TrimSpace(gjson.GetBytes(payload, "model").String())) &&
		isOpenAIResponsesGenerateFalse(payload) {
		return s.completeKiroResponsesPrewarm(c, account, payload, parsed, writeClientMessage)
	}

	body, err := prepareOpenAIWSHTTPBridgeBodyWithPreviousResponseID(payload, preservePreviousResponseID)
	if err != nil {
		return nil, fmt.Errorf("prepare responses websocket bridge body: %w", err)
	}

	pipeReader, pipeWriter := io.Pipe()
	bridgeWriter := newOpenAIWSPipeResponseWriter(pipeWriter)
	bridgeContext := c.Copy()
	bridgeContext.Writer = bridgeWriter
	outcomeCh := make(chan gatewayResponsesWSBridgeOutcome, 1)

	go func() {
		result, forwardErr := s.ForwardAsResponses(ctx, bridgeContext, account, body, parsed)
		if forwardErr != nil {
			_ = pipeWriter.CloseWithError(forwardErr)
		} else {
			_ = pipeWriter.Close()
		}
		outcomeCh <- gatewayResponsesWSBridgeOutcome{result: result, err: forwardErr}
	}()

	scanner := bufio.NewScanner(pipeReader)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanBuf := getSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)
	defer putSSEScannerBuf64K(scanBuf)

	terminalEvents := 0
	eventCount := 0
	clientDisconnected := false
	var downstreamErr error
	for scanner.Scan() {
		data, ok := extractOpenAISSEDataLine(scanner.Text())
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(data)
		if trimmed == "" || trimmed == "[DONE]" {
			continue
		}
		message := []byte(trimmed)
		eventType, _, _ := parseOpenAIWSEventEnvelope(message)
		if eventType == "" {
			continue
		}
		eventCount++
		if isOpenAIWSTerminalEvent(eventType) {
			terminalEvents++
		}
		if clientDisconnected || downstreamErr != nil {
			continue
		}
		if writeErr := writeClientMessage(message); writeErr != nil {
			if isOpenAIWSClientDisconnectError(writeErr) {
				clientDisconnected = true
				continue
			}
			downstreamErr = writeErr
		}
	}
	scanErr := scanner.Err()
	_ = pipeReader.Close()
	outcome := <-outcomeCh
	if outcome.result != nil && clientDisconnected {
		outcome.result.ClientDisconnect = true
	}
	if downstreamErr != nil {
		return outcome.result, fmt.Errorf("write responses websocket bridge event: %w", downstreamErr)
	}
	if outcome.err != nil {
		return outcome.result, outcome.err
	}
	if scanErr != nil {
		return outcome.result, fmt.Errorf("read responses websocket bridge stream: %w", scanErr)
	}
	if eventCount == 0 || terminalEvents != 1 {
		return outcome.result, &UpstreamFailoverError{
			StatusCode:  http.StatusServiceUnavailable,
			FailureKind: UpstreamFailureIncompleteStream,
			Cause:       fmt.Errorf("responses websocket bridge ended with terminal_events=%d events=%d", terminalEvents, eventCount),
		}
	}
	return outcome.result, nil
}
