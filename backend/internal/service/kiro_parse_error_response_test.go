package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWriteNianzsKiroParseErrorKeepsCauseInternal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	account := &Account{ID: 42, Name: "provider-account", Platform: PlatformKiro}

	writeNianzsKiroParseError(context, account, errors.New("upstream event stream parse failed: class=unexpected_eof"))

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Upstream response could not be completed")
	require.NotContains(t, strings.ToLower(recorder.Body.String()), "kiro")
	rawEvents, ok := context.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "parse_error", events[0].Kind)
	require.Contains(t, events[0].Message, "class=unexpected_eof")
}
