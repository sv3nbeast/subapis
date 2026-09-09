package service

import (
	"context"
	"time"
)

// A prompt template is not an independently executing agent. This metadata
// records its origin; the submitted Prompt is the user-approved expanded text.
type WebAgentTemplateSnapshot struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *WebChatService) usableTemplate(ctx context.Context, userID, id int64) (*WebChatTemplate, error) {
	if !s.runtime(ctx).TemplatesEnabled {
		return nil, ErrWebChatTemplatesDisabled
	}
	if id <= 0 {
		return nil, ErrWebChatInvalidTemplate
	}
	template, err := s.repo.GetTemplate(ctx, userID, id, false)
	if err != nil {
		return nil, err
	}
	if template == nil || template.ID != id || !template.Enabled ||
		!(template.Scope == "system" || template.Scope == "personal" && template.UserID != nil && *template.UserID == userID) {
		return nil, ErrWebChatTemplateNotFound
	}
	return template, nil
}
