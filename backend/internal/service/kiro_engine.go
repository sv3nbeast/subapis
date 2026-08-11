package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// KiroEngine identifies the implementation that owns the complete Kiro
// request path for a request. The value is resolved once at request ingress and
// must not change during a request; switching engines after upstream work has
// started could replay tool calls or bill the same turn twice.
type KiroEngine string

const (
	KiroEngineLegacy KiroEngine = config.KiroEngineLegacy
	KiroEngineNianzs KiroEngine = config.KiroEngineNianzs
)

func normalizeKiroEngine(value string) KiroEngine {
	if strings.EqualFold(strings.TrimSpace(value), config.KiroEngineNianzs) {
		return KiroEngineNianzs
	}
	return KiroEngineLegacy
}

// KiroEngineForGroup resolves the configured engine. In legacy mode the
// allowlist provides a reversible group-level canary. In nianzs mode every Kiro
// request uses the pinned nianzs implementation.
func (s *GatewayService) KiroEngineForGroup(groupID *int64) KiroEngine {
	if s == nil || s.cfg == nil {
		return KiroEngineLegacy
	}
	if normalizeKiroEngine(s.cfg.Gateway.KiroEngine) == KiroEngineNianzs {
		return KiroEngineNianzs
	}
	if groupID == nil || *groupID <= 0 {
		return KiroEngineLegacy
	}
	for _, candidate := range s.cfg.Gateway.KiroNianzsGroupIDs {
		if candidate == *groupID {
			return KiroEngineNianzs
		}
	}
	return KiroEngineLegacy
}

func (s *GatewayService) useNianzsKiroEngine(groupID *int64) bool {
	return s.KiroEngineForGroup(groupID) == KiroEngineNianzs
}

func (s *GatewayService) recordKiroEngine(c *gin.Context, groupID *int64, account *Account) KiroEngine {
	engine := s.KiroEngineForGroup(groupID)
	if c != nil {
		c.Set(OpsKiroEngineKey, string(engine))
	}
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	logger.L().Debug("kiro.engine_selected",
		zap.String("engine", string(engine)),
		zap.Int64("group_id", derefGroupID(groupID)),
		zap.Int64("account_id", accountID),
	)
	return engine
}

func (s *GatewayService) useNianzsKiroScheduler(ctx context.Context, groupID *int64) bool {
	if !s.useNianzsKiroEngine(groupID) || groupID == nil || *groupID <= 0 {
		return false
	}
	if group := s.groupFromContext(ctx, *groupID); group != nil {
		return group.Platform == PlatformKiro
	}
	if s.groupRepo == nil {
		return false
	}
	group, err := s.resolveGroupByID(ctx, *groupID)
	return err == nil && group != nil && group.Platform == PlatformKiro
}
