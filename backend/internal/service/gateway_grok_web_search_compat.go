package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// DoGrokNativeResponsesJSON forwards a non-streaming Responses request used by
// the web-search endpoint. It deliberately reuses the normal Grok URL,
// credential and proxy path so search requests receive the same account policy
// as regular Grok traffic.
func (s *GatewayService) DoGrokNativeResponsesJSON(ctx context.Context, account *Account, body []byte) ([]byte, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, errors.New("http upstream not configured")
	}
	if account == nil || !account.IsGrok() {
		return nil, errors.New("grok account required")
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, &UpstreamFailoverError{StatusCode: http.StatusUnauthorized, Reason: GatewayFailureReason("grok_search_token")}
	}
	targetURL, err := buildGrokResponsesURL(account, s.cfg, s.settingService)
	if err != nil {
		return nil, err
	}
	if !json.Valid(body) {
		return nil, errors.New("invalid request body")
	}
	if strings.TrimSpace(gjson.GetBytes(body, "model").String()) == "" {
		if patched, patchErr := sjson.SetBytes(body, "model", "grok-4.5"); patchErr == nil {
			body = patched
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build grok responses request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", defaultGrokUpstreamUserAgent())
	applyGrokCLIHeaders(req.Header)
	account.ApplyHeaderOverrides(req.Header)
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, &UpstreamFailoverError{StatusCode: http.StatusBadGateway, Reason: GatewayFailureReason("grok_search_transport")}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusPaymentRequired || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: data}
		}
		return nil, fmt.Errorf("grok upstream %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}
