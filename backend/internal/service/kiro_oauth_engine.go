package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/config"
	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
)

// KiroOAuthOperations is the account-facing Kiro OAuth surface shared by the
// retained legacy implementation and the engine selector.
type KiroOAuthOperations interface {
	GenerateAuthURL(context.Context, *KiroGenerateAuthURLInput) (*KiroAuthURLResult, error)
	GenerateIDCAuthURL(context.Context, *KiroGenerateIDCAuthURLInput) (*KiroIDCAuthURLResult, error)
	ExchangeCode(context.Context, *KiroExchangeCodeInput) (*KiroTokenInfo, error)
	RefreshToken(context.Context, *KiroRefreshTokenInput) (*KiroTokenInfo, error)
	RefreshAccountToken(context.Context, *Account) (*KiroTokenInfo, error)
	ImportToken(*KiroImportTokenInput) (*KiroTokenInfo, error)
	BuildAccountCredentials(*KiroTokenInfo) map[string]any
	DefaultModels() []kiropkg.Model
}

func (s *KiroOAuthService) DefaultModels() []kiropkg.Model {
	return append([]kiropkg.Model(nil), kiropkg.DefaultModels...)
}

// KiroOAuthEngineService keeps Kiro account authorization, import, manual
// refresh and background refresh on the same engine as gateway traffic. OAuth
// operations have no group context, so only the global engine selector applies;
// group canaries intentionally keep using the legacy authorization flow.
type KiroOAuthEngineService struct {
	legacy *KiroOAuthService
	nianzs *NianzsKiroOAuthService
	cfg    *config.Config
}

func NewKiroOAuthEngineService(legacy *KiroOAuthService, nianzs *NianzsKiroOAuthService, cfg *config.Config) *KiroOAuthEngineService {
	return &KiroOAuthEngineService{legacy: legacy, nianzs: nianzs, cfg: cfg}
}

func (s *KiroOAuthEngineService) Engine() KiroEngine {
	if s != nil && s.cfg != nil && normalizeKiroEngine(s.cfg.Gateway.KiroEngine) == KiroEngineNianzs {
		return KiroEngineNianzs
	}
	return KiroEngineLegacy
}

func (s *KiroOAuthEngineService) useNianzs() bool {
	return s.Engine() == KiroEngineNianzs && s.nianzs != nil
}

func (s *KiroOAuthEngineService) GenerateAuthURL(ctx context.Context, input *KiroGenerateAuthURLInput) (*KiroAuthURLResult, error) {
	if !s.useNianzs() {
		return s.legacy.GenerateAuthURL(ctx, input)
	}
	result, err := s.nianzs.GenerateAuthURL(ctx, &NianzsKiroGenerateAuthURLInput{ProxyID: input.ProxyID, Provider: input.Provider})
	if err != nil {
		return nil, err
	}
	return &KiroAuthURLResult{AuthURL: result.AuthURL, SessionID: result.SessionID, State: result.State}, nil
}

func (s *KiroOAuthEngineService) GenerateIDCAuthURL(ctx context.Context, input *KiroGenerateIDCAuthURLInput) (*KiroIDCAuthURLResult, error) {
	if !s.useNianzs() {
		return s.legacy.GenerateIDCAuthURL(ctx, input)
	}
	result, err := s.nianzs.GenerateIDCAuthURL(ctx, &NianzsKiroGenerateIDCAuthURLInput{ProxyID: input.ProxyID, StartURL: input.StartURL, Region: input.Region})
	if err != nil {
		return nil, err
	}
	return &KiroIDCAuthURLResult{
		AuthURL:   result.AuthURL,
		SessionID: result.SessionID,
		State:     result.State,
		ClientID:  result.ClientID,
		Region:    result.Region,
		StartURL:  result.StartURL,
	}, nil
}

func (s *KiroOAuthEngineService) ExchangeCode(ctx context.Context, input *KiroExchangeCodeInput) (*KiroTokenInfo, error) {
	if !s.useNianzs() {
		return s.legacy.ExchangeCode(ctx, input)
	}
	result, err := s.nianzs.ExchangeCode(ctx, &NianzsKiroExchangeCodeInput{
		SessionID:    input.SessionID,
		State:        input.State,
		Code:         input.Code,
		CallbackPath: input.CallbackPath,
		LoginOption:  input.LoginOption,
		ProxyID:      input.ProxyID,
	})
	if err != nil {
		return nil, err
	}
	return nianzsTokenInfoToLegacy(result), nil
}

func (s *KiroOAuthEngineService) RefreshToken(ctx context.Context, input *KiroRefreshTokenInput) (*KiroTokenInfo, error) {
	if !s.useNianzs() {
		return s.legacy.RefreshToken(ctx, input)
	}
	result, err := s.nianzs.RefreshToken(ctx, &NianzsKiroRefreshTokenInput{
		RefreshToken:  input.RefreshToken,
		AuthMethod:    input.AuthMethod,
		Provider:      input.Provider,
		ClientID:      input.ClientID,
		ClientSecret:  input.ClientSecret,
		StartURL:      input.StartURL,
		Region:        input.Region,
		ProfileArn:    input.ProfileArn,
		IssuerURL:     input.IssuerURL,
		TokenEndpoint: input.TokenEndpoint,
		Scopes:        input.Scopes,
		ProxyID:       input.ProxyID,
	})
	if err != nil {
		return nil, err
	}
	return nianzsTokenInfoToLegacy(result), nil
}

func (s *KiroOAuthEngineService) RefreshAccountToken(ctx context.Context, account *Account) (*KiroTokenInfo, error) {
	if !s.useNianzs() {
		return s.legacy.RefreshAccountToken(ctx, account)
	}
	result, err := s.nianzs.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}
	return nianzsTokenInfoToLegacy(result), nil
}

func (s *KiroOAuthEngineService) ImportToken(input *KiroImportTokenInput) (*KiroTokenInfo, error) {
	if !s.useNianzs() {
		return s.legacy.ImportToken(input)
	}
	result, err := s.nianzs.ImportToken(&NianzsKiroImportTokenInput{
		TokenJSON:              input.TokenJSON,
		DeviceRegistrationJSON: input.DeviceRegistrationJSON,
	})
	if err != nil {
		return nil, err
	}
	return nianzsTokenInfoToLegacy(result), nil
}

func (s *KiroOAuthEngineService) BuildAccountCredentials(tokenInfo *KiroTokenInfo) map[string]any {
	if !s.useNianzs() {
		return s.legacy.BuildAccountCredentials(tokenInfo)
	}
	return s.nianzs.BuildAccountCredentials(legacyTokenInfoToNianzs(tokenInfo))
}

func (s *KiroOAuthEngineService) DefaultModels() []kiropkg.Model {
	if !s.useNianzs() {
		return s.legacy.DefaultModels()
	}
	models := make([]kiropkg.Model, 0, len(nianzskiro.DefaultModels))
	for _, model := range nianzskiro.DefaultModels {
		models = append(models, kiropkg.Model{
			ID: model.ID, Type: model.Type, DisplayName: model.DisplayName, CreatedAt: model.CreatedAt,
		})
	}
	return models
}

func nianzsTokenInfoToLegacy(info *NianzsKiroTokenInfo) *KiroTokenInfo {
	if info == nil {
		return nil
	}
	return &KiroTokenInfo{
		AuthURL: info.AuthURL, SessionID: info.SessionID, State: info.State,
		AccessToken: info.AccessToken, RefreshToken: info.RefreshToken,
		ProfileArn: info.ProfileArn, ExpiresAt: info.ExpiresAt,
		AuthMethod: info.AuthMethod, Provider: info.Provider,
		ClientID: info.ClientID, ClientSecret: info.ClientSecret,
		ClientIDHash: info.ClientIDHash, Email: info.Email,
		StartURL: info.StartURL, Region: info.Region, IssuerURL: info.IssuerURL,
		TokenEndpoint: info.TokenEndpoint, Scopes: info.Scopes,
	}
}

func legacyTokenInfoToNianzs(info *KiroTokenInfo) *NianzsKiroTokenInfo {
	if info == nil {
		return nil
	}
	return &NianzsKiroTokenInfo{
		AuthURL: info.AuthURL, SessionID: info.SessionID, State: info.State,
		AccessToken: info.AccessToken, RefreshToken: info.RefreshToken,
		ProfileArn: info.ProfileArn, ExpiresAt: info.ExpiresAt,
		AuthMethod: info.AuthMethod, Provider: info.Provider,
		ClientID: info.ClientID, ClientSecret: info.ClientSecret,
		ClientIDHash: info.ClientIDHash, Email: info.Email,
		StartURL: info.StartURL, Region: info.Region, IssuerURL: info.IssuerURL,
		TokenEndpoint: info.TokenEndpoint, Scopes: info.Scopes,
	}
}
