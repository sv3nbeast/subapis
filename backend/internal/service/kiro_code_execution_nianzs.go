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
	"os"
	"strings"
	"time"

	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
)

const (
	nianzsKiroCodeExecutionSocketEnv = "KIRO_CODE_EXECUTION_SOCKET"
	nianzsKiroMaxCodeExecutionTurns  = 4
	nianzsKiroCodeWorkerMaxResponse  = 1 << 20
)

var nianzsErrKiroCodeExecutionFallback = errors.New("kiro code execution fallback")

type nianzsKiroCodeExecutionRunner interface {
	Execute(context.Context, string) (nianzskiro.CodeExecutionResult, error)
}

type nianzsKiroCodeExecutionUnixRunner struct {
	socketPath string
}

type nianzsKiroCodeExecutionHTTPError struct {
	Response *http.Response
}

func (e *nianzsKiroCodeExecutionHTTPError) Error() string {
	if e == nil || e.Response == nil {
		return "upstream code execution http error"
	}
	return fmt.Sprintf("upstream code execution http error: %d", e.Response.StatusCode)
}

type nianzsKiroCodeExecution struct {
	ResponseBody []byte
	Usage        ClaudeUsage
	RequestID    string
}

const nianzsKiroCodeExecutionMaxPendingSSE = 8 << 20

// nianzsKiroCodeExecutionTurnWriter translates one already-Anthropic-shaped
// Kiro turn incrementally. Ordinary text/thinking frames are forwarded as soon
// as they arrive; only the small JSON argument of the code tool is retained.
// This preserves server-tool streaming semantics without buffering a complete
// model response before the first client-visible token.
type nianzsKiroCodeExecutionTurnWriter struct {
	ctx              context.Context
	out              io.Writer
	runner           nianzsKiroCodeExecutionRunner
	indexOffset      int
	extraOffset      int
	priorExecCount   int
	priorUsage       nianzskiro.Usage
	emitMessageStart bool
	pending          bytes.Buffer

	toolInputIndex int
	toolUseID      string
	serverToolID   string
	toolInput      strings.Builder
	toolInputBad   bool
	call           nianzskiro.CodeExecutionCall
	result         nianzskiro.CodeExecutionResult
	hasCall        bool
	executed       bool
	sawMessageStop bool
	maxIndex       int
}

func newNianzsKiroCodeExecutionTurnWriter(ctx context.Context, out io.Writer, runner nianzsKiroCodeExecutionRunner, indexOffset, priorExecCount int, priorUsage nianzskiro.Usage, emitMessageStart bool) *nianzsKiroCodeExecutionTurnWriter {
	return &nianzsKiroCodeExecutionTurnWriter{
		ctx:              ctx,
		out:              out,
		runner:           runner,
		indexOffset:      indexOffset,
		priorExecCount:   priorExecCount,
		priorUsage:       priorUsage,
		emitMessageStart: emitMessageStart,
		toolInputIndex:   -1,
		maxIndex:         indexOffset - 1,
	}
}

func (w *nianzsKiroCodeExecutionTurnWriter) Write(payload []byte) (int, error) {
	if w == nil || w.out == nil {
		return 0, errors.New("upstream code execution stream writer unavailable")
	}
	if w.pending.Len()+len(payload) > nianzsKiroCodeExecutionMaxPendingSSE {
		return 0, errors.New("upstream code execution SSE frame too large")
	}
	_, _ = w.pending.Write(payload)
	for {
		raw := w.pending.Bytes()
		boundary := bytes.Index(raw, []byte("\n\n"))
		if boundary < 0 {
			break
		}
		frame := append([]byte(nil), raw[:boundary]...)
		w.pending.Next(boundary + 2)
		if err := w.processFrame(frame); err != nil {
			return 0, err
		}
	}
	return len(payload), nil
}

func (w *nianzsKiroCodeExecutionTurnWriter) Finish() error {
	if w.pending.Len() > 0 {
		frame := bytes.TrimSpace(w.pending.Bytes())
		w.pending.Reset()
		if len(frame) > 0 {
			if err := w.processFrame(frame); err != nil {
				return err
			}
		}
	}
	if w.hasCall && !w.executed {
		return errors.New("upstream code execution tool call ended without a result")
	}
	if !w.hasCall && !w.sawMessageStop {
		return errors.New("upstream code execution stream ended before message_stop")
	}
	return nil
}

func (w *nianzsKiroCodeExecutionTurnWriter) nextIndex() int {
	if w.maxIndex < w.indexOffset {
		return w.indexOffset
	}
	return w.maxIndex + 1
}

func (w *nianzsKiroCodeExecutionTurnWriter) processFrame(frame []byte) error {
	if len(bytes.TrimSpace(frame)) == 0 {
		return nil
	}
	var eventName string
	var dataLine []byte
	for _, line := range bytes.Split(frame, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		switch {
		case bytes.HasPrefix(line, []byte("event: ")):
			eventName = strings.TrimSpace(string(bytes.TrimPrefix(line, []byte("event: "))))
		case bytes.HasPrefix(line, []byte("data: ")):
			dataLine = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data: ")))
		}
	}
	if len(dataLine) == 0 {
		_, err := w.out.Write(append(append([]byte(nil), frame...), '\n', '\n'))
		return err
	}
	var event map[string]any
	if err := json.Unmarshal(dataLine, &event); err != nil {
		_, writeErr := w.out.Write(append(append([]byte(nil), frame...), '\n', '\n'))
		return writeErr
	}
	typeName, _ := event["type"].(string)
	if typeName == "" {
		typeName = eventName
	}

	switch typeName {
	case "message_start":
		if !w.emitMessageStart {
			return nil
		}
		w.emitMessageStart = false
		return w.writeEvent(typeName, event)
	case "content_block_start":
		return w.handleContentBlockStart(event)
	case "content_block_delta":
		return w.handleContentBlockDelta(event)
	case "content_block_stop":
		return w.handleContentBlockStop(event)
	case "message_delta":
		if w.hasCall {
			return nil
		}
		nianzsAddPriorKiroUsage(event, w.priorUsage)
		if w.priorExecCount > 0 {
			nianzsAddCodeExecutionUsage(event, w.priorExecCount)
		}
		return w.writeEvent(typeName, event)
	case "message_stop":
		if w.hasCall {
			return nil
		}
		w.sawMessageStop = true
		return w.writeEvent(typeName, event)
	default:
		return w.writeEvent(typeName, event)
	}
}

func (w *nianzsKiroCodeExecutionTurnWriter) handleContentBlockStart(event map[string]any) error {
	index := nianzsJSONInt(event["index"], -1)
	block, _ := event["content_block"].(map[string]any)
	blockType, _ := block["type"].(string)
	name, _ := block["name"].(string)
	adjusted := index + w.indexOffset + w.extraOffset
	event["index"] = adjusted
	w.observeIndex(adjusted)
	if blockType != "tool_use" || !strings.EqualFold(strings.TrimSpace(name), "code_execution") {
		return w.writeEvent("content_block_start", event)
	}
	if w.hasCall {
		return errors.New("upstream emitted multiple parallel code execution calls")
	}
	w.hasCall = true
	w.toolInputIndex = index
	w.toolUseID, _ = block["id"].(string)
	w.serverToolID = "srvtoolu_" + nianzskiro.GenerateToolUseID()
	block["type"] = "server_tool_use"
	block["id"] = w.serverToolID
	block["name"] = "code_execution"
	block["input"] = map[string]any{}
	// caller is part of Anthropic client tool_use blocks. Native server tools
	// such as code_execution do not expose it on content_block_start.
	delete(block, "caller")
	return w.writeEvent("content_block_start", event)
}

func (w *nianzsKiroCodeExecutionTurnWriter) handleContentBlockDelta(event map[string]any) error {
	index := nianzsJSONInt(event["index"], -1)
	adjusted := index + w.indexOffset + w.extraOffset
	event["index"] = adjusted
	w.observeIndex(adjusted)
	if w.hasCall && index == w.toolInputIndex {
		delta, _ := event["delta"].(map[string]any)
		if deltaType, _ := delta["type"].(string); deltaType == "input_json_delta" {
			fragment, _ := delta["partial_json"].(string)
			if w.toolInput.Len()+len(fragment) > 128<<10 {
				w.toolInputBad = true
			} else if !w.toolInputBad {
				_, _ = w.toolInput.WriteString(fragment)
			}
		}
	}
	return w.writeEvent("content_block_delta", event)
}

func (w *nianzsKiroCodeExecutionTurnWriter) handleContentBlockStop(event map[string]any) error {
	index := nianzsJSONInt(event["index"], -1)
	adjusted := index + w.indexOffset + w.extraOffset
	event["index"] = adjusted
	w.observeIndex(adjusted)
	if err := w.writeEvent("content_block_stop", event); err != nil {
		return err
	}
	if !w.hasCall || index != w.toolInputIndex || w.executed {
		return nil
	}

	var args struct {
		Code string `json:"code"`
	}
	if w.toolInputBad || json.Unmarshal([]byte(w.toolInput.String()), &args) != nil || strings.TrimSpace(args.Code) == "" {
		w.result = nianzskiro.CodeExecutionResult{ReturnCode: 1, ErrorCode: "invalid_tool_input"}
	} else {
		w.call = nianzskiro.CodeExecutionCall{ToolUseID: w.toolUseID, Code: args.Code, Index: index}
		result, err := w.runner.Execute(w.ctx, args.Code)
		if err != nil {
			result = nianzsKiroUnavailableCodeExecutionResult(err)
		}
		w.result = result
	}
	if w.call.ToolUseID == "" {
		w.call = nianzskiro.CodeExecutionCall{ToolUseID: w.toolUseID, Code: args.Code, Index: index}
	}
	resultIndex := adjusted + 1
	if err := nianzsWriteSSEChunks(w.out, nianzskiro.GenerateCodeExecutionResultEvents(w.serverToolID, w.result, resultIndex)); err != nil {
		return err
	}
	w.observeIndex(resultIndex)
	w.extraOffset++
	w.executed = true
	return nil
}

func (w *nianzsKiroCodeExecutionTurnWriter) observeIndex(index int) {
	if index > w.maxIndex {
		w.maxIndex = index
	}
}

func (w *nianzsKiroCodeExecutionTurnWriter) writeEvent(eventName string, event map[string]any) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w.out, "event: "+eventName+"\ndata: "+string(payload)+"\n\n")
	return err
}

func nianzsAddCodeExecutionUsage(event map[string]any, count int) {
	usage, _ := event["usage"].(map[string]any)
	if usage == nil {
		usage = map[string]any{}
		event["usage"] = usage
	}
	serverUsage, _ := usage["server_tool_use"].(map[string]any)
	if serverUsage == nil {
		serverUsage = map[string]any{}
		usage["server_tool_use"] = serverUsage
	}
	serverUsage["code_execution_requests"] = count
}

func nianzsAddPriorKiroUsage(event map[string]any, prior nianzskiro.Usage) {
	if prior == (nianzskiro.Usage{}) {
		return
	}
	usage, _ := event["usage"].(map[string]any)
	if usage == nil {
		usage = map[string]any{}
		event["usage"] = usage
	}
	addInt := func(name string, previous int) {
		if previous > 0 {
			usage[name] = nianzsJSONInt(usage[name], 0) + previous
		}
	}
	addInt("input_tokens", prior.InputTokens)
	addInt("output_tokens", prior.OutputTokens)
	addInt("cache_read_input_tokens", prior.CacheReadInputTokens)
	addInt("cache_creation_input_tokens", prior.CacheCreationInputTokens)
	if prior.CacheCreation5mInputTokens > 0 || prior.CacheCreation1hInputTokens > 0 {
		cacheCreation, _ := usage["cache_creation"].(map[string]any)
		if cacheCreation == nil {
			cacheCreation = map[string]any{}
			usage["cache_creation"] = cacheCreation
		}
		cacheCreation["ephemeral_5m_input_tokens"] = nianzsJSONInt(cacheCreation["ephemeral_5m_input_tokens"], 0) + prior.CacheCreation5mInputTokens
		cacheCreation["ephemeral_1h_input_tokens"] = nianzsJSONInt(cacheCreation["ephemeral_1h_input_tokens"], 0) + prior.CacheCreation1hInputTokens
	}
	if prior.KiroCredits > 0 {
		current, _ := usage["_sub2api_kiro_credits"].(float64)
		usage["_sub2api_kiro_credits"] = current + prior.KiroCredits
	}
}

func nianzsSumKiroUsage(left, right nianzskiro.Usage) nianzskiro.Usage {
	return nianzskiro.Usage{
		InputTokens:                left.InputTokens + right.InputTokens,
		OutputTokens:               left.OutputTokens + right.OutputTokens,
		TotalTokens:                left.TotalTokens + right.TotalTokens,
		CacheReadInputTokens:       left.CacheReadInputTokens + right.CacheReadInputTokens,
		CacheCreationInputTokens:   left.CacheCreationInputTokens + right.CacheCreationInputTokens,
		CacheCreation5mInputTokens: left.CacheCreation5mInputTokens + right.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: left.CacheCreation1hInputTokens + right.CacheCreation1hInputTokens,
		KiroCredits:                left.KiroCredits + right.KiroCredits,
	}
}

func nianzsApplyKiroUsageToClaudeResponse(body []byte, total nianzskiro.Usage) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	usage, _ := payload["usage"].(map[string]any)
	if usage == nil {
		usage = map[string]any{}
		payload["usage"] = usage
	}
	usage["input_tokens"] = total.InputTokens
	usage["output_tokens"] = total.OutputTokens
	usage["cache_read_input_tokens"] = total.CacheReadInputTokens
	if total.CacheCreationInputTokens > 0 {
		usage["cache_creation_input_tokens"] = total.CacheCreationInputTokens
	} else {
		delete(usage, "cache_creation_input_tokens")
	}
	if total.CacheCreation5mInputTokens > 0 || total.CacheCreation1hInputTokens > 0 {
		usage["cache_creation"] = map[string]any{
			"ephemeral_5m_input_tokens": total.CacheCreation5mInputTokens,
			"ephemeral_1h_input_tokens": total.CacheCreation1hInputTokens,
		}
	} else {
		delete(usage, "cache_creation")
	}
	return json.Marshal(payload)
}

func nianzsJSONInt(value any, fallback int) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	}
	return fallback
}

func (s *GatewayService) nianzsKiroCodeExecutionRunnerForRequest() nianzsKiroCodeExecutionRunner {
	if s != nil && s.kiroCodeExecutionRunner != nil {
		return s.kiroCodeExecutionRunner
	}
	socketPath := strings.TrimSpace(os.Getenv(nianzsKiroCodeExecutionSocketEnv))
	if socketPath == "" {
		return nil
	}
	return &nianzsKiroCodeExecutionUnixRunner{socketPath: socketPath}
}

func (r *nianzsKiroCodeExecutionUnixRunner) Execute(ctx context.Context, code string) (nianzskiro.CodeExecutionResult, error) {
	if r == nil || strings.TrimSpace(r.socketPath) == "" {
		return nianzskiro.CodeExecutionResult{}, errors.New("execution worker socket is not configured")
	}
	payload, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		return nianzskiro.CodeExecutionResult{}, err
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	transport := &http.Transport{
		DisableCompression: true,
		DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(dialCtx, "unix", r.socketPath)
		},
		ResponseHeaderTimeout: 20 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/execute", bytes.NewReader(payload))
	if err != nil {
		return nianzskiro.CodeExecutionResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nianzskiro.CodeExecutionResult{}, fmt.Errorf("execution worker request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	limited := io.LimitReader(resp.Body, nianzsKiroCodeWorkerMaxResponse+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nianzskiro.CodeExecutionResult{}, err
	}
	if len(body) > nianzsKiroCodeWorkerMaxResponse {
		return nianzskiro.CodeExecutionResult{}, errors.New("execution worker response too large")
	}
	if resp.StatusCode != http.StatusOK {
		return nianzskiro.CodeExecutionResult{}, fmt.Errorf("execution worker status %d", resp.StatusCode)
	}
	var result nianzskiro.CodeExecutionResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nianzskiro.CodeExecutionResult{}, fmt.Errorf("decode execution worker response: %w", err)
	}
	return result, nil
}

func nianzsKiroUnavailableCodeExecutionResult(err error) nianzskiro.CodeExecutionResult {
	message := "execution worker unavailable"
	if err != nil {
		message += ": " + err.Error()
	}
	return nianzskiro.CodeExecutionResult{
		Stderr:     message,
		ReturnCode: 1,
		ErrorCode:  "unavailable",
	}
}

func (s *GatewayService) streamKiroCodeExecutionAsAnthropicNianzs(
	ctx context.Context,
	account *Account,
	parsed *ParsedRequest,
	anthropicBody []byte,
	mappedModel, requestModel, token string,
	inputTokens int,
	headers http.Header,
	w io.Writer,
	plan *nianzsKiroCacheEmulationPlan,
	runner nianzsKiroCodeExecutionRunner,
	initialResponse *http.Response,
	initialRequestCtx nianzskiro.KiroRequestContext,
) error {
	currentBody, err := nianzskiro.ReplaceLegacyCodeExecutionTool(anthropicBody)
	if err != nil {
		return nianzsErrKiroCodeExecutionFallback
	}
	if initialResponse == nil {
		return errors.New("upstream code execution returned no response")
	}
	nextIndex := 0
	executionCount := 0
	var priorUsage nianzskiro.Usage

	for turn := 0; turn < nianzsKiroMaxCodeExecutionTurns; turn++ {
		resp := initialResponse
		requestCtx := initialRequestCtx
		if turn > 0 {
			var err error
			resp, requestCtx, err = s.executeKiroUpstreamWithParsedNianzs(ctx, account, parsed, currentBody, mappedModel, requestModel, token, headers)
			if err != nil {
				return err
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				_ = resp.Body.Close()
				return fmt.Errorf("upstream code execution continuation returned status %d", resp.StatusCode)
			}
		}
		if turn == 0 {
			requestCtx.CacheEmulationUsage = plan.result().toKiroUsage()
		} else {
			requestCtx.CacheEmulationUsage = nil
		}
		requestCtx.EstimatedInputTokens = inputTokens
		requestCtx.RequireTerminalEvent = true
		requestCtx.EmitProtocolPing = turn == 0 && requestCtx.EmitProtocolPing
		turnWriter := newNianzsKiroCodeExecutionTurnWriter(ctx, w, runner, nextIndex, executionCount, priorUsage, turn == 0)
		streamResult, streamErr := func() (*nianzskiro.StreamResult, error) {
			defer func() { _ = resp.Body.Close() }()
			return nianzskiro.StreamEventStreamAsAnthropicWithContext(
				ctx, resp.Body, turnWriter, requestModel, inputTokens, requestCtx,
			)
		}()
		if streamErr != nil {
			return streamErr
		}
		if err := turnWriter.Finish(); err != nil {
			return err
		}
		if turn == 0 {
			plan.commit()
		}
		if !turnWriter.hasCall {
			return nil
		}
		priorUsage = nianzsSumKiroUsage(priorUsage, streamResult.Usage)
		nextIndex = turnWriter.nextIndex()
		executionCount++
		currentBody, err = nianzskiro.InjectCodeExecutionResultClaude(currentBody, turnWriter.call, turnWriter.result)
		if err != nil {
			return nianzsErrKiroCodeExecutionFallback
		}
	}
	return errors.New("upstream code execution exceeded maximum turns")
}

func (s *GatewayService) executeKiroCodeExecutionNianzs(
	ctx context.Context,
	account *Account,
	parsed *ParsedRequest,
	group *Group,
	anthropicBody []byte,
	mappedModel, requestModel, token string,
	headers http.Header,
	runner nianzsKiroCodeExecutionRunner,
) (*nianzsKiroCodeExecution, error) {
	currentBody, err := nianzskiro.ReplaceLegacyCodeExecutionTool(anthropicBody)
	if err != nil {
		return nil, nianzsErrKiroCodeExecutionFallback
	}
	inputTokens := nianzsEstimateKiroInputTokens(ctx, anthropicBody)
	indicators := make([]nianzskiro.CodeExecutionIndicator, 0, 2)
	requestID := ""
	plan := s.prepareKiroCacheEmulationUsageNianzs(ctx, account, group, anthropicBody, mappedModel, inputTokens)
	var priorUsage nianzskiro.Usage

	for turn := 0; turn < nianzsKiroMaxCodeExecutionTurns; turn++ {
		resp, requestCtx, err := s.executeKiroUpstreamWithParsedNianzs(ctx, account, parsed, currentBody, mappedModel, requestModel, token, headers)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &nianzsKiroCodeExecutionHTTPError{Response: resp}
		}
		if requestID == "" {
			requestID = nianzsBuildKiroRequestID(resp)
		}
		if turn == 0 {
			requestCtx.CacheEmulationUsage = plan.result().toKiroUsage()
		} else {
			requestCtx.CacheEmulationUsage = nil
		}
		requestCtx.EstimatedInputTokens = inputTokens
		requestCtx.RequireTerminalEvent = true
		parsedResponse, parseErr := func() (*nianzskiro.ParseResult, error) {
			defer func() { _ = resp.Body.Close() }()
			return nianzskiro.ParseNonStreamingEventStreamWithContext(resp.Body, requestModel, requestCtx)
		}()
		if parseErr != nil {
			return nil, parseErr
		}
		if turn == 0 {
			plan.commit()
		}

		call, before, after, hasCall := nianzskiro.ExtractCodeExecutionTurnFromResponse(parsedResponse.ResponseBody)
		if !hasCall {
			totalUsage := nianzsSumKiroUsage(priorUsage, parsedResponse.Usage)
			finalBody, injectErr := nianzskiro.InjectCodeExecutionIndicatorsInResponse(parsedResponse.ResponseBody, indicators)
			if injectErr != nil {
				return nil, injectErr
			}
			finalBody, injectErr = nianzsApplyKiroUsageToClaudeResponse(finalBody, totalUsage)
			if injectErr != nil {
				return nil, injectErr
			}
			return &nianzsKiroCodeExecution{
				ResponseBody: finalBody,
				Usage:        nianzsKiroUsageToClaude(totalUsage, inputTokens),
				RequestID:    requestID,
			}, nil
		}
		priorUsage = nianzsSumKiroUsage(priorUsage, parsedResponse.Usage)

		result, runErr := runner.Execute(ctx, call.Code)
		if runErr != nil {
			result = nianzsKiroUnavailableCodeExecutionResult(runErr)
		}
		serverToolUseID := "srvtoolu_" + nianzskiro.GenerateToolUseID()
		indicators = append(indicators, nianzskiro.CodeExecutionIndicator{
			ServerToolUseID: serverToolUseID,
			Code:            call.Code,
			Result:          result,
			Before:          before,
			After:           after,
		})
		currentBody, err = nianzskiro.InjectCodeExecutionResultClaude(currentBody, call, result)
		if err != nil {
			return nil, nianzsErrKiroCodeExecutionFallback
		}
	}
	return nil, errors.New("upstream code execution exceeded maximum turns")
}
