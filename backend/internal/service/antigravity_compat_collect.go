package service

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

// collectClaudeStreamResponse collects a Gemini SSE response and converts it
// into one Anthropic JSON response for the compatibility bridge. It is kept
// separate from the client-writing streaming path so Responses and Chat
// Completions can apply their own final translation.
func (s *AntigravityGatewayService) collectClaudeStreamResponse(c *gin.Context, resp *http.Response, startTime time.Time, originalModel string) ([]byte, *antigravityStreamResult, error) {
	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.settingService.cfg != nil && s.settingService.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.settingService.cfg.Gateway.MaxLineSize
	}
	scanBuf := getSSEScannerBuf64K()
	scanner.Buffer(scanBuf[:0], maxLineSize)

	var firstTokenMs *int
	var last map[string]any
	var lastWithParts map[string]any
	var collectedParts []map[string]any
	var meaningfulResponse bool

	type scanEvent struct {
		line string
		err  error
	}
	events := make(chan scanEvent, 16)
	done := make(chan struct{})
	sendEvent := func(ev scanEvent) bool {
		select {
		case events <- ev:
			return true
		case <-done:
			return false
		}
	}

	var lastReadAt int64
	atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
	go func(scanBuf *sseScannerBuf64K) {
		defer putSSEScannerBuf64K(scanBuf)
		defer close(events)
		for scanner.Scan() {
			atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
			if !sendEvent(scanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}(scanBuf)
	defer close(done)

	streamInterval := time.Duration(0)
	if s.settingService.cfg != nil && s.settingService.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		streamInterval = time.Duration(s.settingService.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	var intervalCh <-chan time.Time
	if streamInterval > 0 {
		ticker := time.NewTicker(streamInterval)
		defer ticker.Stop()
		intervalCh = ticker.C
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				goto returnResponse
			}
			if ev.err != nil {
				if errors.Is(ev.err, bufio.ErrTooLong) {
					logger.LegacyPrintf("service.antigravity_gateway", "SSE line too long (antigravity compat non-stream): max_size=%d error=%v", maxLineSize, ev.err)
				}
				return nil, nil, ev.err
			}

			trimmed := strings.TrimRight(ev.line, "\r\n")
			s.observeAntigravityGeminiSSELine(c, ev.line)
			if !strings.HasPrefix(trimmed, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			inner, err := s.unwrapV1InternalResponse([]byte(payload))
			if err != nil {
				continue
			}
			var parsed map[string]any
			if err := json.Unmarshal(inner, &parsed); err != nil {
				continue
			}
			last = parsed
			parts := extractGeminiParts(parsed)
			if len(parts) > 0 {
				lastWithParts = parsed
				collectedParts = append(collectedParts, parts...)
			}
			if len(parts) > 0 || strings.TrimSpace(extractGeminiFinishReason(parsed)) != "" {
				meaningfulResponse = true
				if firstTokenMs == nil {
					ms := int(time.Since(startTime).Milliseconds())
					firstTokenMs = &ms
				}
			}

		case <-intervalCh:
			if time.Since(time.Unix(0, atomic.LoadInt64(&lastReadAt))) < streamInterval {
				continue
			}
			return nil, nil, fmt.Errorf("stream data interval timeout")
		}
	}

returnResponse:
	if !meaningfulResponse {
		return nil, nil, &UpstreamFailoverError{
			StatusCode:             http.StatusBadGateway,
			ResponseBody:           []byte(`{"error":"empty stream response from upstream"}`),
			RetryableOnSameAccount: true,
		}
	}
	finalResponse := pickGeminiCollectResult(last, lastWithParts)
	if len(collectedParts) > 0 {
		finalResponse = mergeCollectedPartsToResponse(finalResponse, collectedParts)
	}
	geminiBody, err := json.Marshal(finalResponse)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal gemini response: %w", err)
	}
	claudeResp, agUsage, err := antigravity.TransformGeminiToClaude(geminiBody, originalModel, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse upstream response: %w", err)
	}
	usage := &ClaudeUsage{
		InputTokens:              agUsage.InputTokens,
		OutputTokens:             agUsage.OutputTokens,
		CacheCreationInputTokens: agUsage.CacheCreationInputTokens,
		CacheReadInputTokens:     agUsage.CacheReadInputTokens,
		ImageOutputTokens:        agUsage.ImageOutputTokens,
	}
	return claudeResp, &antigravityStreamResult{usage: usage, firstTokenMs: firstTokenMs}, nil
}
