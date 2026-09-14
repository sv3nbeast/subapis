package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
)

// Cursor 的上游事件只有文本与思考两种增量。把它们先归一成 Chat Completions
// chunk，再交给 apicompat 的既有桥接器翻成 Anthropic / Responses 事件——三条
// 出站协议因此共用同一套经过验证的流状态机，而不是各写一份 SSE 拼装。

// cursorEmitter 把一次上游流消费成出站字节；返回累计 usage 与端流错误。
type cursorEmitter func(kind, text string) error

func consumeCursorStream(stream *cursorRunStream, emit cursorEmitter) (cursor.TokenUsage, string) {
	return cursor.ConsumeAssistantStream(stream.Body, func(ev cursor.StreamEvent) error {
		switch ev.Type {
		case "text", "thinking":
			return emit(ev.Type, ev.Text)
		}
		return nil
	})
}

func collectCursorAssistant(stream *cursorRunStream) (text, thinking string, usage cursor.TokenUsage, connectErr string) {
	var textBuf, thinkingBuf strings.Builder
	usage, connectErr = consumeCursorStream(stream, func(kind, payload string) error {
		if kind == "thinking" {
			thinkingBuf.WriteString(payload)
		} else {
			textBuf.WriteString(payload)
		}
		return nil
	})
	return textBuf.String(), thinkingBuf.String(), usage, connectErr
}

func cursorCompletionID() string {
	return "chatcmpl-cursor-" + time.Now().Format("20060102150405")
}

func cursorTextChunk(id, model, text, thinking string) *apicompat.ChatCompletionsChunk {
	chunk := &apicompat.ChatCompletionsChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{Index: 0}},
	}
	if thinking != "" {
		chunk.Choices[0].Delta.ReasoningContent = &thinking
	}
	if text != "" {
		chunk.Choices[0].Delta.Content = &text
	}
	return chunk
}

func cursorUsageChunk(id, model string, usage cursor.TokenUsage) *apicompat.ChatCompletionsChunk {
	chatUsage := chatUsageFromCursor(usage)
	if chatUsage == nil {
		return nil
	}
	return &apicompat.ChatCompletionsChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{},
		Usage:   chatUsage,
	}
}

func cursorChatCompletion(id, model, text, thinking string, usage cursor.TokenUsage) *apicompat.ChatCompletionsResponse {
	content, _ := json.Marshal(text)
	return &apicompat.ChatCompletionsResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []apicompat.ChatChoice{{
			Index: 0,
			Message: apicompat.ChatMessage{
				Role:             "assistant",
				Content:          content,
				ReasoningContent: thinking,
			},
			FinishReason: "stop",
		}},
		Usage: chatUsageFromCursor(usage),
	}
}

func startCursorSSE(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Writer.Flush()
}

// markCursorFirstToken 记录首字时间，只在第一次增量时落一次。
func markCursorFirstToken(firstTokenMs **int, startTime time.Time) {
	if *firstTokenMs != nil {
		return
	}
	ms := int(time.Since(startTime).Milliseconds())
	*firstTokenMs = &ms
}

// ── Chat Completions ────────────────────────────────────────────────────────

func (s *GatewayService) streamCursorAsChatCompletions(c *gin.Context, stream *cursorRunStream, run cursorRunRequest, includeUsage bool) (*ForwardResult, error) {
	startCursorSSE(c)
	completionID := cursorCompletionID()
	var firstTokenMs *int

	writeChunk := func(chunk *apicompat.ChatCompletionsChunk) {
		if chunk == nil {
			return
		}
		payload, err := json.Marshal(chunk)
		if err != nil {
			return
		}
		fmt.Fprintf(c.Writer, "data: %s\n\n", payload)
		c.Writer.Flush()
	}

	usage, connectErr := consumeCursorStream(stream, func(kind, payload string) error {
		markCursorFirstToken(&firstTokenMs, run.StartTime)
		text, thinking := payload, ""
		if kind == "thinking" {
			text, thinking = "", payload
		}
		writeChunk(cursorTextChunk(completionID, run.RequestModel, text, thinking))
		return nil
	})

	// 上游中途报错时必须把错误发出去：补一个 finish_reason=stop 会把被截断的
	// 生成伪装成正常结束，客户端拿到半截答案却以为完整。
	if connectErr != "" {
		_, errType, message := classifyCursorConnectError(connectErr)
		errPayload, _ := json.Marshal(map[string]any{
			"error": map[string]string{"type": errType, "message": message},
		})
		fmt.Fprintf(c.Writer, "data: %s\n\n", errPayload)
	} else {
		stopChunk := cursorTextChunk(completionID, run.RequestModel, "", "")
		stop := "stop"
		stopChunk.Choices[0].FinishReason = &stop
		writeChunk(stopChunk)
		if includeUsage {
			writeChunk(cursorUsageChunk(completionID, run.RequestModel, usage))
		}
	}
	fmt.Fprint(c.Writer, "data: [DONE]\n\n")
	c.Writer.Flush()

	return cursorForwardResult(run, stream.UpstreamModel, firstTokenMs, usage), nil
}

func (s *GatewayService) bufferCursorAsChatCompletions(c *gin.Context, stream *cursorRunStream, run cursorRunRequest) (*ForwardResult, error) {
	text, thinking, usage, connectErr := collectCursorAssistant(stream)
	if connectErr != "" && text == "" {
		status, errType, message := classifyCursorConnectError(connectErr)
		writeGatewayCCError(c, status, errType, message)
		return nil, fmt.Errorf("cursor chat: %s", message)
	}
	c.JSON(http.StatusOK, cursorChatCompletion(cursorCompletionID(), run.RequestModel, text, thinking, usage))
	return cursorForwardResult(run, stream.UpstreamModel, nil, usage), nil
}

// ── Anthropic /v1/messages ──────────────────────────────────────────────────

func (s *GatewayService) streamCursorAsAnthropic(c *gin.Context, stream *cursorRunStream, run cursorRunRequest) (*ForwardResult, error) {
	startCursorSSE(c)
	state := apicompat.NewChatCompletionsToAnthropicStreamState(run.RequestModel)
	completionID := cursorCompletionID()
	var firstTokenMs *int

	writeEvents := func(events []apicompat.AnthropicStreamEvent) {
		for _, event := range events {
			if sse, err := apicompat.ResponsesAnthropicEventToSSE(event); err == nil {
				fmt.Fprint(c.Writer, sse)
			}
		}
		c.Writer.Flush()
	}

	usage, connectErr := consumeCursorStream(stream, func(kind, payload string) error {
		markCursorFirstToken(&firstTokenMs, run.StartTime)
		text, thinking := payload, ""
		if kind == "thinking" {
			text, thinking = "", payload
		}
		writeEvents(apicompat.ChatCompletionsChunkToAnthropicEvents(cursorTextChunk(completionID, run.RequestModel, text, thinking), state))
		return nil
	})

	// error 事件在 Anthropic SSE 里是终止事件：发出它就不再补 message_stop，
	// 否则客户端会把截断的输出当成完整回答。
	if connectErr != "" {
		_, errType, message := classifyCursorConnectError(connectErr)
		fmt.Fprint(c.Writer, buildAnthropicStreamErrorSSE(errType, message))
		c.Writer.Flush()
		return cursorForwardResult(run, stream.UpstreamModel, firstTokenMs, usage), nil
	}
	if chunk := cursorUsageChunk(completionID, run.RequestModel, usage); chunk != nil {
		writeEvents(apicompat.ChatCompletionsChunkToAnthropicEvents(chunk, state))
	}
	writeEvents(apicompat.FinalizeChatCompletionsAnthropicStream(state))
	return cursorForwardResult(run, stream.UpstreamModel, firstTokenMs, usage), nil
}

func (s *GatewayService) bufferCursorAsAnthropic(c *gin.Context, stream *cursorRunStream, run cursorRunRequest) (*ForwardResult, error) {
	text, thinking, usage, connectErr := collectCursorAssistant(stream)
	if connectErr != "" && text == "" {
		status, errType, message := classifyCursorConnectError(connectErr)
		writeAnthropicError(c, status, errType, message)
		return nil, fmt.Errorf("cursor anthropic: %s", message)
	}
	ccResp := cursorChatCompletion(cursorCompletionID(), run.RequestModel, text, thinking, usage)
	c.JSON(http.StatusOK, apicompat.ChatCompletionsResponseToAnthropic(ccResp, run.RequestModel))
	return cursorForwardResult(run, stream.UpstreamModel, nil, usage), nil
}

// ── OpenAI /v1/responses ────────────────────────────────────────────────────

func (s *GatewayService) streamCursorAsResponses(c *gin.Context, stream *cursorRunStream, run cursorRunRequest) (*ForwardResult, error) {
	startCursorSSE(c)
	state := apicompat.NewChatCompletionsToResponsesStreamState(run.RequestModel)
	completionID := cursorCompletionID()
	var firstTokenMs *int

	writeEvents := func(events []apicompat.ResponsesStreamEvent) {
		for _, event := range events {
			if sse, err := apicompat.ResponsesEventToSSE(event); err == nil {
				fmt.Fprint(c.Writer, sse)
			}
		}
		c.Writer.Flush()
	}

	usage, connectErr := consumeCursorStream(stream, func(kind, payload string) error {
		markCursorFirstToken(&firstTokenMs, run.StartTime)
		text, thinking := payload, ""
		if kind == "thinking" {
			text, thinking = "", payload
		}
		writeEvents(apicompat.ChatCompletionsChunkToResponsesEvents(cursorTextChunk(completionID, run.RequestModel, text, thinking), state))
		return nil
	})

	// 同上：error 事件取代本该发出的 response.completed。
	if connectErr != "" {
		_, errType, message := classifyCursorConnectError(connectErr)
		payload, _ := json.Marshal(map[string]string{"code": errType, "message": message})
		fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", payload)
		c.Writer.Flush()
		return cursorForwardResult(run, stream.UpstreamModel, firstTokenMs, usage), nil
	}
	if chunk := cursorUsageChunk(completionID, run.RequestModel, usage); chunk != nil {
		writeEvents(apicompat.ChatCompletionsChunkToResponsesEvents(chunk, state))
	}
	writeEvents(apicompat.FinalizeChatCompletionsResponsesStream(state))
	return cursorForwardResult(run, stream.UpstreamModel, firstTokenMs, usage), nil
}

func (s *GatewayService) bufferCursorAsResponses(c *gin.Context, stream *cursorRunStream, run cursorRunRequest) (*ForwardResult, error) {
	text, thinking, usage, connectErr := collectCursorAssistant(stream)
	if connectErr != "" && text == "" {
		status, errType, message := classifyCursorConnectError(connectErr)
		writeResponsesError(c, status, errType, message)
		return nil, fmt.Errorf("cursor responses: %s", message)
	}
	ccResp := cursorChatCompletion(cursorCompletionID(), run.RequestModel, text, thinking, usage)
	c.JSON(http.StatusOK, apicompat.ChatCompletionsResponseToResponses(ccResp, run.RequestModel, nil, nil, false, nil))
	return cursorForwardResult(run, stream.UpstreamModel, nil, usage), nil
}
