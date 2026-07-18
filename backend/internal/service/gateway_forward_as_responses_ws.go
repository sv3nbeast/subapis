package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type gatewayResponsesWSBridgeOutcome struct {
	result *ForwardResult
	err    error
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
