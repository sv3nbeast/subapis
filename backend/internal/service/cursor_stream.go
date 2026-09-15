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
// toolCall 非 nil 表示一次参数已完整的工具调用。
type cursorEmitter func(kind, text string, toolCall *apicompat.ChatToolCall) error

// consumeCursorStream 消费一次上游流。工具调用在这里聚合：Cursor 分片给参数，
// 只有拼完才交给 emit——客户端无法处理半截 JSON 参数。
func consumeCursorStream(stream *cursorRunStream, emit cursorEmitter) (cursor.TokenUsage, string, *cursorToolAggregator) {
	tools := newCursorToolAggregator()
	usage, connectErr := cursor.ConsumeAssistantStream(stream.Body, func(ev cursor.StreamEvent) error {
		switch ev.Type {
		case "text", "thinking":
			return emit(ev.Type, ev.Text, nil)
		case "tool_call":
			if done := tools.Observe(ev.Tool); done != nil {
				return emit("tool_call", "", done)
			}
		}
		return nil
	})
	// 上游常在 turn_ended 前就停发工具事件而不给 end：残留的调用必须补交，
	// 否则客户端会等一个永远不到来的工具调用。
	for _, pending := range tools.Pending() {
		call := pending
		if err := emit("tool_call", "", &call); err != nil {
			break
		}
	}
	return usage, connectErr, tools
}

func collectCursorAssistant(stream *cursorRunStream) (text, thinking string, toolCalls []apicompat.ChatToolCall, usage cursor.TokenUsage, connectErr string) {
	var textBuf, thinkingBuf strings.Builder
	usage, connectErr, _ = consumeCursorStream(stream, func(kind, payload string, call *apicompat.ChatToolCall) error {
		switch {
		case call != nil:
			toolCalls = append(toolCalls, *call)
		case kind == "thinking":
			thinkingBuf.WriteString(payload)
		default:
			textBuf.WriteString(payload)
		}
		return nil
	})
	return textBuf.String(), thinkingBuf.String(), toolCalls, usage, connectErr
}

func cursorCompletionID() string {
	return "chatcmpl-cursor-" + time.Now().Format("20060102150405")
}

func cursorTextChunk(id, model, text, thinking string) *apicompat.ChatCompletionsChunk {
	chunk := cursorEmptyChunk(id, model)
	if thinking != "" {
		chunk.Choices[0].Delta.ReasoningContent = &thinking
	}
	if text != "" {
		chunk.Choices[0].Delta.Content = &text
	}
	return chunk
}

// cursorToolChunk 把一次完整的工具调用包成 chunk。参数一次性给全，因为聚合
// 已经完成——apicompat 的桥接器据此产出 tool_use / tool_calls。
func cursorToolChunk(id, model string, call apicompat.ChatToolCall) *apicompat.ChatCompletionsChunk {
	chunk := cursorEmptyChunk(id, model)
	chunk.Choices[0].Delta.ToolCalls = []apicompat.ChatToolCall{call}
	return chunk
}

func cursorEmptyChunk(id, model string) *apicompat.ChatCompletionsChunk {
	return &apicompat.ChatCompletionsChunk{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []apicompat.ChatChunkChoice{{Index: 0}},
	}
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

func cursorChatCompletion(id, model, text, thinking string, toolCalls []apicompat.ChatToolCall, usage cursor.TokenUsage) *apicompat.ChatCompletionsResponse {
	content, _ := json.Marshal(text)
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
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
				ToolCalls:        toolCalls,
			},
			FinishReason: finishReason,
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

	usage, connectErr, tools := consumeCursorStream(stream, func(kind, payload string, call *apicompat.ChatToolCall) error {
		markCursorFirstToken(&firstTokenMs, run.StartTime)
		if call != nil {
			writeChunk(cursorToolChunk(completionID, run.RequestModel, *call))
			return nil
		}
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
		// 有工具调用的轮次必须报 tool_calls：客户端据此决定是执行工具还是收尾。
		stop := "stop"
		if tools.HasCalls() {
			stop = "tool_calls"
		}
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
	text, thinking, toolCalls, usage, connectErr := collectCursorAssistant(stream)
	if connectErr != "" && text == "" && len(toolCalls) == 0 {
		status, errType, message := classifyCursorConnectError(connectErr)
		writeGatewayCCError(c, status, errType, message)
		return nil, fmt.Errorf("cursor chat: %s", message)
	}
	c.JSON(http.StatusOK, cursorChatCompletion(cursorCompletionID(), run.RequestModel, text, thinking, toolCalls, usage))
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

	usage, connectErr, _ := consumeCursorStream(stream, func(kind, payload string, call *apicompat.ChatToolCall) error {
		markCursorFirstToken(&firstTokenMs, run.StartTime)
		if call != nil {
			writeEvents(apicompat.ChatCompletionsChunkToAnthropicEvents(cursorToolChunk(completionID, run.RequestModel, *call), state))
			return nil
		}
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
	text, thinking, toolCalls, usage, connectErr := collectCursorAssistant(stream)
	if connectErr != "" && text == "" && len(toolCalls) == 0 {
		status, errType, message := classifyCursorConnectError(connectErr)
		writeAnthropicError(c, status, errType, message)
		return nil, fmt.Errorf("cursor anthropic: %s", message)
	}
	ccResp := cursorChatCompletion(cursorCompletionID(), run.RequestModel, text, thinking, toolCalls, usage)
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

	usage, connectErr, _ := consumeCursorStream(stream, func(kind, payload string, call *apicompat.ChatToolCall) error {
		markCursorFirstToken(&firstTokenMs, run.StartTime)
		if call != nil {
			writeEvents(apicompat.ChatCompletionsChunkToResponsesEvents(cursorToolChunk(completionID, run.RequestModel, *call), state))
			return nil
		}
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
	text, thinking, toolCalls, usage, connectErr := collectCursorAssistant(stream)
	if connectErr != "" && text == "" && len(toolCalls) == 0 {
		status, errType, message := classifyCursorConnectError(connectErr)
		writeResponsesError(c, status, errType, message)
		return nil, fmt.Errorf("cursor responses: %s", message)
	}
	ccResp := cursorChatCompletion(cursorCompletionID(), run.RequestModel, text, thinking, toolCalls, usage)
	c.JSON(http.StatusOK, apicompat.ChatCompletionsResponseToResponses(ccResp, run.RequestModel, nil, nil, false, nil))
	return cursorForwardResult(run, stream.UpstreamModel, nil, usage), nil
}
