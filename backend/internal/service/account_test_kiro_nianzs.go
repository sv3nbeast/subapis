// Source-faithful integration of the Kiro account-test path from
// github.com/nianzs/sub2api at d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
	"github.com/gin-gonic/gin"
)

func (s *AccountTestService) testKiroAccountConnectionNianzs(c *gin.Context, account *Account, modelID string) error {
	ctx := c.Request.Context()

	testModelID := strings.TrimSpace(modelID)
	if testModelID == "" {
		testModelID = "claude-sonnet-4-6"
	}
	if mappedModel := account.GetMappedModel(testModelID); strings.TrimSpace(mappedModel) != "" {
		testModelID = mappedModel
	}

	if !nianzsIsKiroDirectModeAccount(account) {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Unsupported Kiro account type: %s", account.Type))
	}

	// API Key 账号:api_key 即长期 Bearer Token,无需 token provider 刷新。
	// OAuth 账号:经 kiroTokenProvider 获取(必要时刷新)access_token。
	var accessToken string
	if account.Type == AccountTypeAPIKey {
		accessToken = nianzsFirstKiroCredential(account, "kiro_api_key", "kiroApiKey", "api_key")
		if accessToken == "" {
			return s.sendErrorAndEnd(c, "No API key available")
		}
	} else {
		if s.nianzsKiroTokenProvider == nil {
			return s.sendErrorAndEnd(c, "Kiro token provider not configured")
		}
		token, err := s.nianzsKiroTokenProvider.GetAccessToken(ctx, account)
		if err != nil {
			return s.sendErrorAndEnd(c, fmt.Sprintf("Failed to get Kiro access token: %s", err.Error()))
		}
		accessToken = token
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	payload, err := createTestPayload(testModelID)
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to create test payload")
	}
	payloadBytes, _ := json.Marshal(payload)

	s.sendEvent(c, TestEvent{Type: "test_start", Model: testModelID})

	resp, err := s.executeKiroTestUpstreamNianzs(ctx, account, payloadBytes, testModelID, accessToken)
	if err != nil {
		return s.sendErrorAndEnd(c, fmt.Sprintf("Request failed: %s", err.Error()))
	}

	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return s.sendErrorAndEnd(c, formatKiroTestErrorNianzs(resp.StatusCode, body, testModelID, account))
	}

	pr, pw := io.Pipe()
	go func() {
		defer func() { _ = resp.Body.Close() }()
		_, streamErr := nianzskiro.StreamEventStreamAsAnthropicWithContext(ctx, resp.Body, pw, testModelID, nianzsEstimateKiroInputTokens(ctx, payloadBytes), nianzskiro.KiroRequestContext{})
		if streamErr != nil {
			_ = pw.CloseWithError(streamErr)
			return
		}
		_ = pw.Close()
	}()

	return s.processClaudeStream(c, pr)
}

func formatKiroTestErrorNianzs(statusCode int, body []byte, requestedModel string, account *Account) string {
	return fmt.Sprintf("API returned %d: %s", statusCode, string(body))
}

func (s *AccountTestService) executeKiroTestUpstreamNianzs(ctx context.Context, account *Account, anthropicBody []byte, mappedModel, token string) (*http.Response, error) {
	modelID := nianzskiro.MapModel(mappedModel)
	currentToken := token
	// 测试连接走 Q endpoint，Q endpoint 不需要 profileArn（凭据中的占位符 ARN 会导致 403）
	profileArn := ""
	preparedBody := nianzsPrepareKiroPayloadBodyForRequestModel(anthropicBody, mappedModel)
	buildResult, err := nianzskiro.BuildKiroPayloadWithContext(preparedBody, modelID, profileArn, "AI_EDITOR", nil)
	if err != nil {
		return nil, err
	}
	payload := buildResult.Payload

	// 账号连通性测试默认走 AWS Q endpoint（group 级 q/krs 选择作用于真实流量）。
	endpoints := nianzsBuildKiroEndpoints(account, KiroEndpointModeQ)
	proxyURL := nianzsKiroProxyURL(account)
	tlsProfile := s.tlsFPProfileService.ResolveTLSProfile(account)
	accountKey := nianzsBuildKiroAccountKey(account)
	maxRetries := 2
	for idx, endpoint := range endpoints {
		for attempt := 0; attempt <= maxRetries; attempt++ {
			req, err := nianzsNewKiroJSONRequest(ctx, endpoint.URL, payload, currentToken, accountKey, nianzsBuildKiroMachineID(account), endpoint.AmzTarget, account)
			if err != nil {
				return nil, err
			}

			resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
			if err != nil {
				return nil, err
			}

			if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode >= 500 && resp.StatusCode < 600) {
				if idx+1 < len(endpoints) {
					_ = resp.Body.Close()
					break
				}
				return resp, nil
			}

			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				respBody, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, readErr
				}

				if s.nianzsKiroTokenProvider != nil && (resp.StatusCode == http.StatusUnauthorized || nianzsIsKiroTokenErrorBody(respBody)) && attempt < maxRetries {
					refreshedToken, refreshErr := s.nianzsKiroTokenProvider.ForceRefreshAccessToken(ctx, account)
					if refreshErr == nil && strings.TrimSpace(refreshedToken) != "" {
						currentToken = refreshedToken
						accountKey = nianzsBuildKiroAccountKey(account)
						buildResult, err = nianzskiro.BuildKiroPayloadWithContext(preparedBody, modelID, profileArn, "AI_EDITOR", nil)
						if err != nil {
							return nil, err
						}
						payload = buildResult.Payload
						continue
					}
				}

				nianzsResetHTTPResponseBody(resp, respBody)
				return resp, nil
			}

			if resp.StatusCode == http.StatusBadRequest {
				respBody, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, readErr
				}
				nianzsResetHTTPResponseBody(resp, respBody)
				return resp, nil
			}

			return resp, nil
		}
	}

	return nil, fmt.Errorf("kiro upstream endpoints exhausted")
}
