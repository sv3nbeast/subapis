package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMarkClientClosedRequestUses499WithoutErrorBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	markClientClosedRequest(c)

	require.Equal(t, httpStatusClientClosedRequest, c.Writer.Status())
	require.Empty(t, recorder.Body.String())
	skip, exists := c.Get(service.OpsSkipErrorLogKey)
	require.True(t, exists)
	require.Equal(t, true, skip)
}
