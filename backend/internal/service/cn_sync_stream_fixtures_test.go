//go:build unit

package service

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

type openAIChatStreamReadErrorCloser struct {
	payload []byte
	err     error
}

func (r *openAIChatStreamReadErrorCloser) Read(p []byte) (int, error) {
	if len(r.payload) > 0 {
		n := copy(p, r.payload)
		r.payload = r.payload[n:]
		return n, nil
	}
	return 0, r.err
}
func (r *openAIChatStreamReadErrorCloser) Close() error { return nil }

func grokMessagesSSECompletedResponse(responseID string, cachedTokens int) *http.Response {
	body := fmt.Sprintf(`data: {"type":"response.completed","response":{"id":%q,"object":"response","model":"grok-4.3","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7,"input_tokens_details":{"cached_tokens":%d}}}}

data: [DONE]

`, responseID, cachedTokens)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
}
