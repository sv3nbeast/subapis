package service

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResolveUpstreamResponseReadLimit(t *testing.T) {
	t.Run("use default when config missing", func(t *testing.T) {
		require.Equal(t, defaultUpstreamResponseReadMaxBytes, resolveUpstreamResponseReadLimit(nil))
	})

	t.Run("use configured value", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Gateway.UpstreamResponseReadMaxBytes = 1234
		require.Equal(t, int64(1234), resolveUpstreamResponseReadLimit(cfg))
	})
}

func TestReadUpstreamResponseBodyLimited(t *testing.T) {
	t.Run("within limit", func(t *testing.T) {
		body, err := readUpstreamResponseBodyLimited(bytes.NewReader([]byte("ok")), 2)
		require.NoError(t, err)
		require.Equal(t, []byte("ok"), body)
	})

	t.Run("exceeds limit", func(t *testing.T) {
		body, err := readUpstreamResponseBodyLimited(bytes.NewReader([]byte("toolong")), 3)
		require.Nil(t, body)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrUpstreamResponseBodyTooLarge))
	})
}

func TestReadUpstreamResponseBody_TooLargeWritesResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	cfg := &config.Config{}
	cfg.Gateway.UpstreamResponseReadMaxBytes = 3

	called := false
	body, err := ReadUpstreamResponseBody(bytes.NewReader([]byte("toolong")), cfg, c, func(c *gin.Context) {
		called = true
		c.JSON(http.StatusBadGateway, gin.H{"error": "too large"})
	})

	require.Nil(t, body)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUpstreamResponseBodyTooLarge))
	require.True(t, called)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "too large")
}
