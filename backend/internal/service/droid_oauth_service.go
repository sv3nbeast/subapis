package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	droidpkg "github.com/Wei-Shaw/sub2api/internal/pkg/droid"
	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
)

type DroidOAuthService struct {
	sessionStore *droidpkg.SessionStore
	proxyRepo    ProxyRepository
}

func NewDroidOAuthService(proxyRepo ProxyRepository) *DroidOAuthService {
	return &DroidOAuthService{
		sessionStore: droidpkg.NewSessionStore(),
		proxyRepo:    proxyRepo,
	}
}

type DroidAuthURLResult struct {
	SessionID               string `json:"session_id"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	UserCode                string `json:"user_code"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type DroidTokenInfo struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
}

type DroidGenerateAuthURLInput struct {
	ProxyID *int64
}

type DroidExchangeCodeInput struct {
	SessionID string
	ProxyID   *int64
}

type DroidRefreshTokenInput struct {
	RefreshToken string
	ProxyID      *int64
}

func (s *DroidOAuthService) GenerateAuthURL(ctx context.Context, input *DroidGenerateAuthURLInput) (*DroidAuthURLResult, error) {
	proxyURL, _ := s.resolveProxyURL(ctx, input.ProxyID)
	deviceAuth, err := droidpkg.StartDeviceAuthorization(ctx, proxyURL)
	if err != nil {
		return nil, err
	}

	sessionID := kiropkg.GenerateSessionID()
	s.sessionStore.Set(sessionID, &droidpkg.DeviceAuthSession{
		State:      sessionID,
		DeviceCode: deviceAuth.DeviceCode,
		ProxyURL:   proxyURL,
		CreatedAt:  time.Now(),
	})

	return &DroidAuthURLResult{
		SessionID:               sessionID,
		VerificationURI:         deviceAuth.VerificationURI,
		VerificationURIComplete: deviceAuth.VerificationURIComplete,
		UserCode:                deviceAuth.UserCode,
		ExpiresIn:               deviceAuth.ExpiresIn,
		Interval:                deviceAuth.Interval,
	}, nil
}

func (s *DroidOAuthService) ExchangeCode(ctx context.Context, input *DroidExchangeCodeInput) (*DroidTokenInfo, error) {
	session, ok := s.sessionStore.Get(strings.TrimSpace(input.SessionID))
	if !ok {
		return nil, fmt.Errorf("session not found or expired")
	}

	proxyURL := session.ProxyURL
	if input.ProxyID != nil {
		proxyURL, _ = s.resolveProxyURL(ctx, input.ProxyID)
	}

	token, err := droidpkg.PollDeviceAuthorization(ctx, proxyURL, session.DeviceCode)
	if err != nil {
		return nil, err
	}
	s.sessionStore.Delete(strings.TrimSpace(input.SessionID))
	return toDroidTokenInfo(token), nil
}

func (s *DroidOAuthService) RefreshToken(ctx context.Context, input *DroidRefreshTokenInput) (*DroidTokenInfo, error) {
	proxyURL, _ := s.resolveProxyURL(ctx, input.ProxyID)
	token, err := droidpkg.RefreshToken(ctx, proxyURL, input.RefreshToken)
	if err != nil {
		return nil, err
	}
	return toDroidTokenInfo(token), nil
}

func (s *DroidOAuthService) RefreshAccountToken(ctx context.Context, account *Account) (*DroidTokenInfo, error) {
	if account == nil || account.Platform != PlatformDroid || account.Type != AccountTypeOAuth {
		return nil, fmt.Errorf("not a droid oauth account")
	}
	return s.RefreshToken(ctx, &DroidRefreshTokenInput{
		RefreshToken: account.GetCredential("refresh_token"),
		ProxyID:      account.ProxyID,
	})
}

func (s *DroidOAuthService) BuildAccountCredentials(tokenInfo *DroidTokenInfo) map[string]any {
	if tokenInfo == nil {
		return map[string]any{}
	}
	creds := map[string]any{}
	if strings.TrimSpace(tokenInfo.AccessToken) != "" {
		creds["access_token"] = tokenInfo.AccessToken
	}
	if strings.TrimSpace(tokenInfo.RefreshToken) != "" {
		creds["refresh_token"] = tokenInfo.RefreshToken
	}
	if strings.TrimSpace(tokenInfo.ExpiresAt) != "" {
		creds["expires_at"] = tokenInfo.ExpiresAt
	}
	if strings.TrimSpace(tokenInfo.TokenType) != "" {
		creds["token_type"] = tokenInfo.TokenType
	}
	return creds
}

func (s *DroidOAuthService) resolveProxyURL(ctx context.Context, proxyID *int64) (string, error) {
	if proxyID == nil || s.proxyRepo == nil {
		return "", nil
	}
	proxy, err := s.proxyRepo.GetByID(ctx, *proxyID)
	if err != nil || proxy == nil {
		return "", err
	}
	return proxy.URL(), nil
}

func toDroidTokenInfo(token *droidpkg.TokenData) *DroidTokenInfo {
	if token == nil {
		return nil
	}
	expiresAt := strings.TrimSpace(token.ExpiresAt)
	if expiresAt == "" {
		expiresIn := token.ExpiresIn
		if expiresIn <= 0 {
			expiresIn = 8 * 3600
		}
		expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	tokenType := strings.TrimSpace(token.TokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	return &DroidTokenInfo{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    expiresAt,
		TokenType:    tokenType,
	}
}
