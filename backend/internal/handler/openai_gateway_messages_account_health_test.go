package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldPenalizeOpenAIMessagesAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(nil)
	require.True(t, shouldPenalizeOpenAIMessagesAccount(c))

	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonContextLimit)
	require.False(t, shouldPenalizeOpenAIMessagesAccount(c), "client-owned context limits must not lower account health")
}
