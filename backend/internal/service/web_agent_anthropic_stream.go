package service

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// File rendering needs a complete document, not partial model output. Consume
// one streaming generation, bounded in bytes and by the caller's HTTP context.
// Do not publish partial content, retry, or equate EOF with message_stop.
func readWebAgentAnthropicStream(body io.Reader, out *WebAgentModelOutput) error {
	limited := &io.LimitedReader{R: body, N: webAgentModelMaxResponseBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), webAgentModelMaxResponseBytes+1)
	var data []string
	var eventName string
	started, ended := false, false
	open := -1
	var kinds []string
	var texts []strings.Builder
	stopReason := ""
	usage := map[string]int64{}
	invalid := func() error {
		return webAgentFailure("model_response_invalid", errors.New("invalid task message stream"))
	}
	mergeUsage := func(raw map[string]json.RawMessage) error {
		for _, key := range []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
			if value, ok := raw[key]; ok {
				var n int64
				if json.Unmarshal(value, &n) != nil || n < 0 {
					return invalid()
				}
				usage[key] = n
			}
		}
		return nil
	}
	consume := func() error {
		if len(data) == 0 {
			return nil
		}
		var evt struct {
			Type    string `json:"type"`
			Index   *int   `json:"index"`
			Message struct {
				Type    string                     `json:"type"`
				Content []json.RawMessage          `json:"content"`
				Usage   map[string]json.RawMessage `json:"usage"`
			} `json:"message"`
			Block struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content_block"`
			Delta struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage map[string]json.RawMessage `json:"usage"`
		}
		if json.Unmarshal([]byte(strings.Join(data, "\n")), &evt) != nil || (eventName != "" && eventName != evt.Type) {
			return invalid()
		}
		switch evt.Type {
		case "ping":
			return nil
		case "error":
			return webAgentFailure("model_stream_error", errors.New("gateway reported a task stream error"))
		case "message_start":
			if started || evt.Message.Type != "message" || len(evt.Message.Content) != 0 {
				return invalid()
			}
			started = true
			if err := mergeUsage(evt.Message.Usage); err != nil {
				return err
			}
		case "content_block_start":
			if !started || ended || open != -1 || evt.Index == nil || *evt.Index != len(kinds) {
				return invalid()
			}
			switch evt.Block.Type {
			case "text", "thinking", "redacted_thinking":
			default:
				return webAgentFailure("model_unexpected_tool", errors.New("unrequested task content block"))
			}
			open = *evt.Index
			kinds = append(kinds, evt.Block.Type)
			texts = append(texts, strings.Builder{})
			if evt.Block.Type == "text" {
				texts[open].WriteString(evt.Block.Text)
			}
		case "content_block_delta":
			if !started || ended || open < 0 || evt.Index == nil || *evt.Index != open {
				return invalid()
			}
			if kinds[open] == "text" && evt.Delta.Type == "text_delta" {
				texts[open].WriteString(evt.Delta.Text)
			} else if kinds[open] != "thinking" || (evt.Delta.Type != "thinking_delta" && evt.Delta.Type != "signature_delta") {
				return invalid()
			}
		case "content_block_stop":
			if open < 0 || evt.Index == nil || *evt.Index != open {
				return invalid()
			}
			open = -1
		case "message_delta":
			if !started || ended || open != -1 {
				return invalid()
			}
			ended = true
			stopReason = evt.Delta.StopReason
			if err := mergeUsage(evt.Usage); err != nil {
				return err
			}
		case "message_stop":
			if !started || !ended || open != -1 {
				return invalid()
			}
			blocks := make([]map[string]string, 0, len(kinds))
			for i, k := range kinds {
				if k == "text" {
					blocks = append(blocks, map[string]string{"type": "text", "text": texts[i].String()})
				}
			}
			response := map[string]any{"type": "message", "stop_reason": stopReason, "content": blocks}
			if len(usage) > 0 {
				response["usage"] = usage
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				return err
			}
			return parseWebAgentModelResponse(encoded, true, out)
		default:
			return invalid()
		}
		return nil
	}
	for scanner.Scan() {
		if limited.N <= 0 {
			return webAgentFailure("output_budget_exceeded", errors.New("task stream exceeds byte budget"))
		}
		line := scanner.Text()
		if line == "" {
			terminal := eventName == "message_stop" && len(data) > 0
			if !terminal && len(data) > 0 {
				var kind struct {
					Type string `json:"type"`
				}
				_ = json.Unmarshal([]byte(strings.Join(data, "\n")), &kind)
				terminal = kind.Type == "message_stop"
			}
			if err := consume(); err != nil {
				return err
			}
			if terminal {
				return nil
			}
			data = nil
			eventName = ""
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		} else if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}
