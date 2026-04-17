package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
)

var ErrUpstreamResponseBodyTooLarge = errors.New("upstream response body too large")

const defaultUpstreamResponseReadMaxBytes int64 = 8 * 1024 * 1024

func resolveUpstreamResponseReadLimit(cfg *config.Config) int64 {
	if cfg != nil && cfg.Gateway.UpstreamResponseReadMaxBytes > 0 {
		return cfg.Gateway.UpstreamResponseReadMaxBytes
	}
	return defaultUpstreamResponseReadMaxBytes
}

func readUpstreamResponseBodyLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	if reader == nil {
		return nil, errors.New("response body is nil")
	}
	if maxBytes <= 0 {
		maxBytes = defaultUpstreamResponseReadMaxBytes
	}

	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%w: limit=%d", ErrUpstreamResponseBodyTooLarge, maxBytes)
	}
	return body, nil
}

// TooLargeWriter writes an API-format-specific error when the upstream body is too large.
type TooLargeWriter func(c *gin.Context)

// ReadUpstreamResponseBody reads a non-streaming upstream response body using the configured size limit.
// On ErrUpstreamResponseBodyTooLarge it also records an ops upstream error and lets the caller
// provide an API-format-specific error writer.
func ReadUpstreamResponseBody(reader io.Reader, cfg *config.Config, c *gin.Context, onTooLarge TooLargeWriter) ([]byte, error) {
	maxBytes := resolveUpstreamResponseReadLimit(cfg)
	body, err := readUpstreamResponseBodyLimited(reader, maxBytes)
	if err != nil {
		if errors.Is(err, ErrUpstreamResponseBodyTooLarge) {
			if c != nil {
				setOpsUpstreamError(c, http.StatusBadGateway, "upstream response too large", "")
			}
			if onTooLarge != nil {
				onTooLarge(c)
			}
		}
		return nil, err
	}
	return body, nil
}

func anthropicTooLargeError(c *gin.Context) {
	if c == nil {
		return
	}
	c.JSON(http.StatusBadGateway, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    "upstream_error",
			"message": "Upstream response too large",
		},
	})
}

func openAITooLargeError(c *gin.Context) {
	if c == nil {
		return
	}
	c.JSON(http.StatusBadGateway, gin.H{
		"error": gin.H{
			"type":    "upstream_error",
			"message": "Upstream response too large",
		},
	})
}
