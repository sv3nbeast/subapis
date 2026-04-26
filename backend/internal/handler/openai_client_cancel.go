package handler

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const httpStatusClientClosedRequest = 499

func isOpenAIForwardClientCanceled(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if service.IsOpenAIClientCanceledError(err) {
		return true
	}
	if c == nil || c.Request == nil || c.Request.Context().Err() == nil {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func markOpenAIClientClosedRequest(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(service.OpsSkipErrorLogKey, true)
	if c.Writer == nil || c.Writer.Written() {
		return
	}
	c.Status(httpStatusClientClosedRequest)
}
