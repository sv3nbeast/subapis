package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

// CursorOAuthService 驱动 Cursor 的浏览器登录：生成授权地址，然后由前端轮询
// 直到用户在浏览器完成登录。Cursor 不下发 telemetry id，登录成功时由这里
// 生成一对并写入凭证，供 x-cursor-checksum 使用。
type CursorOAuthService struct {
	sessions  *cursor.SessionStore
	proxyRepo ProxyRepository
}

func NewCursorOAuthService(proxyRepo ProxyRepository) *CursorOAuthService {
	return &CursorOAuthService{
		sessions:  cursor.NewSessionStore(),
		proxyRepo: proxyRepo,
	}
}

// CursorAuthURLResult 是一次登录尝试的入口信息。
type CursorAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
}

// CursorTokenInfo 是登录完成后的凭证。Pending 为 true 表示用户还没在浏览器
// 里完成授权，前端应继续轮询。
type CursorTokenInfo struct {
	Pending      bool   `json:"pending,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	MachineID    string `json:"machine_id,omitempty"`
	MacMachineID string `json:"mac_machine_id,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	Email        string `json:"email,omitempty"`
}

// GenerateAuthURL 开启一次登录尝试。
func (s *CursorOAuthService) GenerateAuthURL(ctx context.Context, proxyID *int64) (*CursorAuthURLResult, error) {
	verifier, challenge, err := cursor.GeneratePKCE()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "CURSOR_OAUTH_PKCE_FAILED", "生成 PKCE 失败: %v", err)
	}
	sessionID, err := cursor.GenerateSessionID()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "CURSOR_OAUTH_SESSION_FAILED", "生成会话失败: %v", err)
	}
	proxyURL, err := s.resolveProxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}

	loginUUID := uuid.NewString()
	s.sessions.Set(sessionID, &cursor.OAuthSession{
		UUID:      loginUUID,
		Verifier:  verifier,
		Challenge: challenge,
		ProxyURL:  proxyURL,
		CreatedAt: time.Now(),
	})
	return &CursorAuthURLResult{
		AuthURL:   cursor.BuildLoginURL(challenge, loginUUID),
		SessionID: sessionID,
	}, nil
}

// Poll 查询一次登录结果。成功后会话即作废，凭证只能被取走一次。
func (s *CursorOAuthService) Poll(ctx context.Context, sessionID string, proxyID *int64) (*CursorTokenInfo, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_SESSION_REQUIRED", "session_id 不能为空")
	}
	session, ok := s.sessions.Get(sessionID)
	if !ok {
		return nil, infraerrors.New(http.StatusBadRequest, "CURSOR_OAUTH_SESSION_NOT_FOUND", "授权会话不存在或已过期，请重新发起授权")
	}
	// 授权过程中前端可能换了代理选择；以最新的为准。
	if proxyID != nil {
		proxyURL, err := s.resolveProxyURL(ctx, proxyID)
		if err != nil {
			return nil, err
		}
		session.ProxyURL = proxyURL
	}

	tokens, pending, err := cursor.PollSession(ctx, session)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "CURSOR_OAUTH_POLL_FAILED", "轮询授权结果失败: %v", err)
	}
	if pending {
		return &CursorTokenInfo{Pending: true}, nil
	}
	s.sessions.Delete(sessionID)

	info, err := s.buildTokenInfo(ctx, tokens, session.ProxyURL)
	if err != nil {
		return nil, err
	}
	return info, nil
}

// buildTokenInfo 把上游令牌补成一份完整的账号凭证。
func (s *CursorOAuthService) buildTokenInfo(ctx context.Context, tokens *cursor.TokenResponse, proxyURL string) (*CursorTokenInfo, error) {
	machineID, err := cursor.GenerateTelemetryID()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "CURSOR_OAUTH_TELEMETRY_FAILED", "生成设备标识失败: %v", err)
	}
	macMachineID, err := cursor.GenerateTelemetryID()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "CURSOR_OAUTH_TELEMETRY_FAILED", "生成设备标识失败: %v", err)
	}

	info := &CursorTokenInfo{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		MachineID:    machineID,
		MacMachineID: macMachineID,
		UserID:       cursor.UserID(tokens.AccessToken),
	}
	if expiry := cursor.AccessTokenExpiry(tokens.AccessToken); expiry != nil {
		info.ExpiresAt = expiry.Format(time.RFC3339)
	}
	// 邮箱只用于在管理端标识账号，取不到不影响授权本身。
	info.Email = cursor.AccountEmail(ctx, tokens.AccessToken, proxyURL)
	return info, nil
}

// BuildAccountCredentials 把授权结果转成待持久化的账号凭证。
func (s *CursorOAuthService) BuildAccountCredentials(info *CursorTokenInfo) map[string]any {
	if info == nil {
		return map[string]any{}
	}
	credentials := map[string]any{
		"access_token":   info.AccessToken,
		"machine_id":     info.MachineID,
		"mac_machine_id": info.MacMachineID,
	}
	for key, value := range map[string]string{
		"refresh_token": info.RefreshToken,
		"expires_at":    info.ExpiresAt,
		"user_id":       info.UserID,
		"email":         info.Email,
	} {
		if strings.TrimSpace(value) != "" {
			credentials[key] = value
		}
	}
	return credentials
}

func (s *CursorOAuthService) resolveProxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil || s.proxyRepo == nil {
		return "", nil
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
	if err != nil {
		return "", infraerrors.Newf(http.StatusBadRequest, "CURSOR_OAUTH_PROXY_NOT_FOUND", "代理不存在: %v", err)
	}
	if proxy == nil {
		return "", nil
	}
	return proxy.URL(), nil
}
