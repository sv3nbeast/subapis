package service

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"
)

const openAIHTTPResponseOwnerContextKey = "openai_http_response_owner"

type openAIHTTPResponseOwner struct {
	userID   int64
	apiKeyID int64
}

func SetOpenAIHTTPResponseOwner(c *gin.Context, userID, apiKeyID int64) {
	if c == nil || userID <= 0 || apiKeyID <= 0 {
		return
	}
	c.Set(openAIHTTPResponseOwnerContextKey, openAIHTTPResponseOwner{userID: userID, apiKeyID: apiKeyID})
}

func (s *OpenAIGatewayService) ValidateOpenAIHTTPResponseOwner(ctx context.Context, groupID int64, responseID string, userID, apiKeyID int64) (bool, error) {
	if s == nil || strings.TrimSpace(responseID) == "" || userID <= 0 || apiKeyID <= 0 {
		return false, nil
	}
	ownerUserID, ownerAPIKeyID, found, err := s.getOpenAIWSStateStore().GetHTTPResponseOwner(ctx, groupID, responseID)
	if err != nil || !found {
		return false, err
	}
	return ownerUserID == userID || (ownerUserID <= 0 && ownerAPIKeyID == apiKeyID), nil
}

func (s *OpenAIGatewayService) BindOpenAIHTTPResponseOwner(ctx context.Context, groupID int64, responseID string, userID, apiKeyID int64) error {
	if s == nil {
		return nil
	}
	return s.getOpenAIWSStateStore().BindHTTPResponseOwner(ctx, groupID, responseID, userID, apiKeyID, s.openAIWSResponseStickyTTL())
}
