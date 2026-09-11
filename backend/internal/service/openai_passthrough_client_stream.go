package service

import "github.com/gin-gonic/gin"

// openAIPassthroughClientStreamKey pins the inbound request's stream
// preference across recursive passthrough attempts of the same request.
const openAIPassthroughClientStreamKey = "openai_passthrough_client_stream"

// resolveOpenAIPassthroughClientStream returns the stream mode the client asked
// for. The first passthrough entry receives the inbound request's stream flag
// and pins it on the gin context; later entries for the same request (rejected
// field retry, compact fallback, agent identity recovery) arrive with a body
// whose stream flag already describes the upstream transport, so they reuse the
// pinned value instead of re-deriving it from that body.
func resolveOpenAIPassthroughClientStream(c *gin.Context, reqStream bool) bool {
	if c == nil {
		return reqStream
	}
	if raw, ok := c.Get(openAIPassthroughClientStreamKey); ok {
		if pinned, isBool := raw.(bool); isBool {
			return pinned
		}
	}
	c.Set(openAIPassthroughClientStreamKey, reqStream)
	return reqStream
}
