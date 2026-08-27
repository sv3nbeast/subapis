package kiro

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const nianzsKiroPromptTooLongMessage = "prompt is too long"

// KiroEventDiagnosticSink receives metadata only. Payload content and
// exception messages are deliberately excluded from KiroEventDiagnostic.
type KiroEventDiagnosticSink func(KiroEventDiagnostic)

type KiroEventDiagnostic struct {
	BodyAttempt             int
	EventType               string
	MessageType             string
	ExceptionType           string
	DecodeStatus            string
	PayloadBytes            int
	PayloadHash             string
	TopLevelKeys            []string
	NestedKeys              []string
	StopReason              string
	ContentBytes            int
	ToolUseCount            int
	HasUsage                bool
	HasSemanticCandidate    bool
	FrameCount              int
	DecodedFrameCount       int
	SemanticCandidateFrames int
	HasCompletionEvidence   bool
	ObservedEventTypes      []string
}

type UpstreamExceptionError struct {
	ExceptionType string
	Message       string
}

func (e *UpstreamExceptionError) Error() string {
	if e == nil {
		return ""
	}
	exceptionType := strings.TrimSpace(e.ExceptionType)
	if exceptionType == "" {
		exceptionType = "Exception"
	}
	if message := strings.TrimSpace(e.Message); message != "" {
		return fmt.Sprintf("kiro upstream exception: %s: %s", exceptionType, message)
	}
	return fmt.Sprintf("kiro upstream exception: %s", exceptionType)
}

// ContextLimitError preserves the Anthropic error wording recognized by
// Claude Code's reactive compaction path.
type ContextLimitError struct {
	Reason          string
	Message         string
	ResponseStarted bool
}

func (e *ContextLimitError) Error() string              { return nianzsKiroPromptTooLongMessage }
func (e *ContextLimitError) ClientErrorType() string    { return "invalid_request_error" }
func (e *ContextLimitError) ClientErrorMessage() string { return nianzsKiroPromptTooLongMessage }

func IsContextLimit(err error) bool {
	var target *ContextLimitError
	return errors.As(err, &target)
}

func (m *eventStreamMessage) ExceptionError() error {
	if m == nil || !strings.EqualFold(strings.TrimSpace(m.MessageType), "exception") {
		return nil
	}
	message := extractNianzsKiroExceptionMessage(m.Payload)
	exceptionType := strings.TrimSpace(m.ExceptionType)
	if exceptionType == "" {
		exceptionType = "Exception"
	}
	if isNianzsKiroContextLimitSignal(exceptionType, message) {
		return &ContextLimitError{Reason: exceptionType, Message: message}
	}
	return &UpstreamExceptionError{ExceptionType: exceptionType, Message: message}
}

func contextLimitErrorFromNianzsEvent(eventType string, event map[string]any) error {
	if !strings.EqualFold(strings.TrimSpace(eventType), "invalidStateEvent") || len(event) == 0 {
		return nil
	}
	state := nestedEvent(event, "invalidStateEvent")
	reason := firstNianzsKiroStringField(state, "reason", "code", "errorCode")
	message := firstNianzsKiroStringField(state, "message", "error", "detail")
	if reason == "" {
		reason = firstNianzsKiroStringField(event, "reason", "code", "errorCode")
	}
	if message == "" {
		message = firstNianzsKiroStringField(event, "message", "error", "detail")
	}
	if !isNianzsKiroContextLimitSignal(reason, message) {
		return nil
	}
	return &ContextLimitError{Reason: reason, Message: message}
}

func markNianzsKiroContextResponseStarted(err error, started bool) error {
	if !started || err == nil {
		return err
	}
	var contextErr *ContextLimitError
	if errors.As(err, &contextErr) {
		contextErr.ResponseStarted = true
	}
	return err
}

func isNianzsKiroContextLimitSignal(values ...string) bool {
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		switch {
		case strings.Contains(normalized, "content_length_exceeds_threshold"),
			strings.Contains(normalized, "contentlengthexceeded"),
			strings.Contains(normalized, "content length exceeds threshold"),
			strings.Contains(normalized, "context_length_exceeded"),
			strings.Contains(normalized, "prompt is too long"),
			strings.Contains(normalized, "input length and max_tokens exceed context limit"),
			strings.Contains(normalized, "input exceeds the context window"),
			strings.Contains(normalized, "maximum prompt length exceeded"):
			return true
		}
	}
	return false
}

func extractNianzsKiroExceptionMessage(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return strings.TrimSpace(string(payload))
	}
	for _, key := range []string{"message", "Message", "error", "Error"} {
		if message, ok := body[key].(string); ok && strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
	}
	return strings.TrimSpace(string(payload))
}

type nianzsEventStreamHeaders struct {
	EventType     string
	MessageType   string
	ExceptionType string
}

func parseNianzsEventStreamHeaders(headers []byte) nianzsEventStreamHeaders {
	var values nianzsEventStreamHeaders
	for offset := 0; offset < len(headers); {
		nameLen := int(headers[offset])
		offset++
		if offset+nameLen > len(headers) {
			break
		}
		name := string(headers[offset : offset+nameLen])
		offset += nameLen
		if offset >= len(headers) {
			break
		}
		valueType := headers[offset]
		offset++
		if valueType == 7 {
			if offset+2 > len(headers) {
				break
			}
			valueLen := int(binary.BigEndian.Uint16(headers[offset : offset+2]))
			offset += 2
			if offset+valueLen > len(headers) {
				break
			}
			value := string(headers[offset : offset+valueLen])
			offset += valueLen
			switch name {
			case ":event-type":
				values.EventType = value
			case ":message-type":
				values.MessageType = value
			case ":exception-type":
				values.ExceptionType = value
			}
			continue
		}
		next, ok := skipHeaderValue(headers, offset, valueType)
		if !ok {
			break
		}
		offset = next
	}
	return values
}

func decodeNianzsKiroEventPayload(payload []byte) (map[string]any, string) {
	if len(payload) == 0 {
		return nil, "empty_payload"
	}
	var event map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&event); err != nil {
		return nil, "invalid_json"
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != nil {
		if errors.Is(err, io.EOF) {
			return event, "decoded"
		}
		return nil, "trailing_json"
	}
	return nil, "trailing_json"
}

type nianzsKiroEventDiagnosticState struct {
	sink                    KiroEventDiagnosticSink
	bodyAttempt             int
	frameCount              int
	decodedFrameCount       int
	semanticCandidateFrames int
	hasCompletionEvidence   bool
	observedEventTypes      map[string]struct{}
}

// nianzsKiroSemanticTailState recognizes the completion shape emitted by both
// Amazon Q and KRS for some long-context turns: at least one assistant or
// enabled-reasoning response frame, followed by contextUsageEvent, followed by
// a frame-aligned clean EOF. The context-usage marker must occur after the last
// response frame so an earlier progress update cannot turn a later truncated
// response into a success.
type nianzsKiroSemanticTailState struct {
	sawResponseCandidate        bool
	contextUsageAfterLastOutput bool
	allowReasoning              bool
}

// nianzsKiroIncompleteStreamState classifies only evidence already decoded
// from the EventStream. In particular, context metadata with no assistant,
// reasoning, or tool candidate is request-shaped and deterministic across
// credentials; a transport EOF with no decoded frames remains a distinct
// transient/account failover signal.
type nianzsKiroIncompleteStreamState struct {
	decodedFrames      int
	sawContextUsage    bool
	sawSemantic        bool
	sawNonMetadataOnly bool
}

func (s *nianzsKiroIncompleteStreamState) observe(msg *eventStreamMessage, event map[string]any, decodeStatus string) {
	if s == nil || msg == nil || decodeStatus != "decoded" {
		return
	}
	s.decodedFrames++
	if nianzsKiroDiagnosticHasSemanticCandidate(msg, event) {
		s.sawSemantic = true
	}
	switch strings.TrimSpace(msg.EventType) {
	case "contextUsageEvent":
		s.sawContextUsage = true
	case "messageMetadataEvent", "metadataEvent":
		// Metadata frames may accompany context usage but cannot complete a turn.
	default:
		s.sawNonMetadataOnly = true
	}
}

func (s *nianzsKiroIncompleteStreamState) reason() IncompleteStreamReason {
	if s == nil || s.decodedFrames == 0 {
		return IncompleteStreamReasonEmptyEOF
	}
	if s.sawContextUsage && !s.sawSemantic && !s.sawNonMetadataOnly {
		return IncompleteStreamReasonMetadataOnlyEOF
	}
	return IncompleteStreamReasonMissingTerminal
}

func newNianzsKiroSemanticTailState(requestCtx KiroRequestContext) *nianzsKiroSemanticTailState {
	return &nianzsKiroSemanticTailState{allowReasoning: requestCtx.ThinkingEnabled}
}

func (s *nianzsKiroSemanticTailState) observe(msg *eventStreamMessage, event map[string]any, decodeStatus string) {
	if s == nil {
		return
	}
	if decodeStatus != "decoded" || msg == nil {
		// Do not synthesize a terminal event across an undecodable payload.
		s.contextUsageAfterLastOutput = false
		return
	}
	eventType := strings.TrimSpace(msg.EventType)
	if nianzsKiroSemanticTailResponseCandidate(eventType, event, s.allowReasoning) {
		s.sawResponseCandidate = true
		s.contextUsageAfterLastOutput = false
		return
	}
	if strings.EqualFold(eventType, "contextUsageEvent") && s.sawResponseCandidate {
		s.contextUsageAfterLastOutput = true
		return
	}
	if s.contextUsageAfterLastOutput {
		// contextUsageEvent must be the final decoded frame. A later provider
		// event needs its own explicit terminal evidence or the turn stays
		// incomplete.
		s.contextUsageAfterLastOutput = false
	}
}

func (s *nianzsKiroSemanticTailState) canCompleteAtCleanEOF() bool {
	return s != nil && s.sawResponseCandidate && s.contextUsageAfterLastOutput
}

func nianzsKiroSemanticTailResponseCandidate(eventType string, event map[string]any, allowReasoning bool) bool {
	switch strings.TrimSpace(eventType) {
	case "assistantResponseEvent":
		assistant := nestedEvent(event, "assistantResponseEvent")
		if assistant == nil {
			assistant = event
		}
		return getString(assistant, "content") != "" || len(readToolUses(assistant, event)) > 0
	case "toolUseEvent":
		// A stopped toolUseEvent is already explicit completion evidence. Never
		// promote an unfinished streaming tool fragment merely because context
		// usage follows it; doing so could synthesize a truncated tool call.
		return false
	case "reasoningContentEvent":
		if !allowReasoning {
			return false
		}
		reasoning := nestedEvent(event, "reasoningContentEvent")
		if reasoning == nil {
			reasoning = event
		}
		return getString(reasoning, "text") != "" || getString(reasoning, "redactedContent") != ""
	default:
		return false
	}
}

func newNianzsKiroEventDiagnosticState(requestCtx KiroRequestContext) *nianzsKiroEventDiagnosticState {
	if requestCtx.EventDiagnosticSink == nil {
		return nil
	}
	return &nianzsKiroEventDiagnosticState{
		sink:               requestCtx.EventDiagnosticSink,
		bodyAttempt:        requestCtx.BodyAttempt,
		observedEventTypes: make(map[string]struct{}),
	}
}

func (s *nianzsKiroEventDiagnosticState) observe(msg *eventStreamMessage, event map[string]any, decodeStatus string, completionEvidence bool) {
	if s == nil || s.sink == nil {
		return
	}
	s.frameCount++
	eventType := "<empty>"
	if msg != nil && strings.TrimSpace(msg.EventType) != "" {
		eventType = strings.TrimSpace(msg.EventType)
	}
	s.observedEventTypes[eventType] = struct{}{}
	if decodeStatus == "decoded" || decodeStatus == "context_limit" || decodeStatus == "exception" {
		s.decodedFrameCount++
	}
	if nianzsKiroDiagnosticHasSemanticCandidate(msg, event) {
		s.semanticCandidateFrames++
	}
	if completionEvidence {
		s.hasCompletionEvidence = true
	}
	if !shouldEmitNianzsKiroFrameDiagnostic(msg, decodeStatus) {
		return
	}
	diagnostic := buildNianzsKiroEventDiagnostic(s.bodyAttempt, msg, event, decodeStatus)
	s.sink(diagnostic)
}

func (s *nianzsKiroEventDiagnosticState) finish(status string) {
	if s == nil || s.sink == nil {
		return
	}
	s.sink(KiroEventDiagnostic{
		BodyAttempt:             s.bodyAttempt,
		EventType:               "__stream_summary__",
		DecodeStatus:            status,
		FrameCount:              s.frameCount,
		DecodedFrameCount:       s.decodedFrameCount,
		SemanticCandidateFrames: s.semanticCandidateFrames,
		HasCompletionEvidence:   s.hasCompletionEvidence,
		ObservedEventTypes:      sortedNianzsKiroStringSet(s.observedEventTypes),
	})
}

func (s *nianzsKiroEventDiagnosticState) acceptSemanticTailCompletion() {
	if s != nil {
		s.hasCompletionEvidence = true
	}
}

func shouldEmitNianzsKiroFrameDiagnostic(msg *eventStreamMessage, decodeStatus string) bool {
	if decodeStatus != "decoded" || msg == nil || strings.EqualFold(strings.TrimSpace(msg.MessageType), "exception") {
		return true
	}
	switch strings.TrimSpace(msg.EventType) {
	case "assistantResponseEvent", "reasoningContentEvent", "toolUseEvent",
		"messageMetadataEvent", "metadataEvent", "supplementaryWebLinksEvent",
		"usageEvent", "meteringEvent", "messageStopEvent", "message_stop":
		return false
	default:
		return true
	}
}

func nianzsKiroDiagnosticHasSemanticCandidate(msg *eventStreamMessage, event map[string]any) bool {
	if msg == nil {
		return false
	}
	switch strings.TrimSpace(msg.EventType) {
	case "assistantResponseEvent":
		assistant := nestedEvent(event, "assistantResponseEvent")
		if assistant == nil {
			assistant = event
		}
		return getString(assistant, "content") != "" || len(readToolUses(assistant, event)) > 0
	case "reasoningContentEvent":
		return true
	case "toolUseEvent":
		return true
	default:
		return false
	}
}

func buildNianzsKiroEventDiagnostic(bodyAttempt int, msg *eventStreamMessage, event map[string]any, decodeStatus string) KiroEventDiagnostic {
	diagnostic := KiroEventDiagnostic{BodyAttempt: bodyAttempt, DecodeStatus: decodeStatus}
	if msg == nil {
		return diagnostic
	}
	diagnostic.EventType = strings.TrimSpace(msg.EventType)
	diagnostic.MessageType = strings.TrimSpace(msg.MessageType)
	diagnostic.ExceptionType = strings.TrimSpace(msg.ExceptionType)
	diagnostic.PayloadBytes = len(msg.Payload)
	if len(msg.Payload) > 0 {
		hash := sha256.Sum256(msg.Payload)
		diagnostic.PayloadHash = fmt.Sprintf("%x", hash[:8])
	}
	diagnostic.TopLevelKeys = sortedNianzsKiroJSONKeys(event)
	nested := nestedEvent(event, diagnostic.EventType)
	diagnostic.NestedKeys = sortedNianzsKiroJSONKeys(nested)
	diagnostic.StopReason = readStopReason(event)
	if stopReason := readStopReason(nested); stopReason != "" {
		diagnostic.StopReason = stopReason
	}

	switch diagnostic.EventType {
	case "assistantResponseEvent":
		assistant := nested
		if assistant == nil {
			assistant = event
		}
		diagnostic.ContentBytes = len(getString(assistant, "content"))
		diagnostic.ToolUseCount = len(readToolUses(assistant, event))
	case "toolUseEvent":
		diagnostic.ToolUseCount = 1
	case "reasoningContentEvent":
		reasoning := nested
		if reasoning == nil {
			reasoning = event
		}
		diagnostic.ContentBytes = len(getString(reasoning, "text"))
	}
	diagnostic.HasUsage = hasNianzsKiroDiagnosticUsage(event, nested)
	diagnostic.HasSemanticCandidate = diagnostic.ContentBytes > 0 || diagnostic.ToolUseCount > 0 || diagnostic.EventType == "reasoningContentEvent"
	return diagnostic
}

func sortedNianzsKiroJSONKeys(values map[string]any) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedNianzsKiroStringSet(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func hasNianzsKiroDiagnosticUsage(values ...map[string]any) bool {
	for _, value := range values {
		for _, key := range []string{"tokenUsage", "usage", "credits", "creditUsage", "contextUsagePercentage"} {
			if _, ok := value[key]; ok {
				return true
			}
		}
	}
	return false
}

func firstNianzsKiroStringField(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
