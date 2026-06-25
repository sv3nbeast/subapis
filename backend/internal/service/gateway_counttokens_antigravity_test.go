package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayService_ForwardCountTokens_AntigravityReturnsEstimatedTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-opus-4-6","messages":[{"role":"user","content":[{"type":"text","text":"hello world"}]}]}`)), PlatformAnthropic)
	require.NoError(t, err)

	svc := &GatewayService{}
	account := &Account{
		ID:       1,
		Platform: PlatformAntigravity,
		Type:     AccountTypeOAuth,
	}

	err = svc.ForwardCountTokens(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"input_tokens":3}`, rec.Body.String())
}
