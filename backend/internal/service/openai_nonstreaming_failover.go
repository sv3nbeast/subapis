package service

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) nonStreamingTerminalFailureFailover(
	c *gin.Context,
	resp *http.Response,
	account *Account,
	passthrough bool,
	terminalType string,
	payload []byte,
	message string,
	canonicalModel ...string,
) *UpstreamFailoverError {
	if account == nil || IsResponseCommitted(c) {
		return nil
	}
	shouldFailover := openAIStreamFailedEventShouldFailover(payload, message)
	if terminalType == "error" {
		shouldFailover = openAIStreamErrorEventShouldFailover(payload, message)
	}
	if !shouldFailover {
		return nil
	}
	var headers http.Header
	upstreamRequestID := ""
	if resp != nil {
		headers = resp.Header
		upstreamRequestID = strings.TrimSpace(resp.Header.Get("x-request-id"))
	}
	return s.newOpenAIStreamFailoverErrorWithModel(c, account, passthrough, upstreamRequestID, payload, message, firstNonEmpty(canonicalModel...), headers)
}
