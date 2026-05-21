package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	webChatRecentMessageLimit = 40
	webChatContextMaxChars    = 60000
	webChatMessageMaxChars    = 20000
)

var (
	ErrWebChatDisabled        = infraerrors.NotFound("WEB_CHAT_DISABLED", "web chat is disabled")
	ErrWebChatSessionNotFound = infraerrors.NotFound("WEB_CHAT_SESSION_NOT_FOUND", "web chat session not found")
	ErrWebChatMessageNotFound = infraerrors.NotFound("WEB_CHAT_MESSAGE_NOT_FOUND", "web chat message not found")
	ErrWebChatNoGroups        = infraerrors.Forbidden("WEB_CHAT_NO_GROUPS", "no available groups for web chat")
	ErrWebChatInvalidGroup    = infraerrors.Forbidden("WEB_CHAT_INVALID_GROUP", "group is not available for web chat")
	ErrWebChatInvalidModel    = infraerrors.BadRequest("WEB_CHAT_INVALID_MODEL", "model is not available for this group")
	ErrWebChatEmptyMessage    = infraerrors.BadRequest("WEB_CHAT_EMPTY_MESSAGE", "message cannot be empty")
	ErrWebChatMessageTooLong  = infraerrors.BadRequest("WEB_CHAT_MESSAGE_TOO_LONG", "message is too long")
)

type WebChatRepository interface {
	CreateSession(ctx context.Context, session *WebChatSession) error
	ListSessions(ctx context.Context, userID int64) ([]WebChatSession, error)
	GetSession(ctx context.Context, userID, sessionID int64) (*WebChatSession, error)
	UpdateSessionTarget(ctx context.Context, userID, sessionID, groupID int64, model string) error
	DeleteSession(ctx context.Context, userID, sessionID int64) error
	CreateMessage(ctx context.Context, message *WebChatMessage) error
	UpdateMessageStatus(ctx context.Context, messageID int64, content, status, errorMessage string) error
	TouchSession(ctx context.Context, sessionID int64, title string) error
	ListMessages(ctx context.Context, userID, sessionID int64) ([]WebChatMessage, error)
	RecentMessages(ctx context.Context, userID, sessionID int64, limit int) ([]WebChatMessage, error)
}

type WebChatAPIKeyRepository interface {
	EnsureWebChatKey(ctx context.Context, userID, groupID int64, groupName, key string) (*APIKey, bool, error)
}

type webChatAPIKeyManager interface {
	GetAvailableGroups(ctx context.Context, userID int64) ([]Group, error)
	GenerateKey() (string, error)
}

type webChatModelCatalog interface {
	ListDisplayModelsForGroup(ctx context.Context, groupID int64, platform string) []SupportedModel
}

type webChatRuntimeReader interface {
	GetWebChatRuntime(ctx context.Context) WebChatRuntime
}

type WebChatService struct {
	repo           WebChatRepository
	webChatKeyRepo WebChatAPIKeyRepository
	apiKeyService  webChatAPIKeyManager
	channelService webChatModelCatalog
	settingService webChatRuntimeReader
}

func NewWebChatService(
	repo WebChatRepository,
	webChatKeyRepo WebChatAPIKeyRepository,
	apiKeyService webChatAPIKeyManager,
	channelService webChatModelCatalog,
	settingService webChatRuntimeReader,
) *WebChatService {
	return &WebChatService{
		repo:           repo,
		webChatKeyRepo: webChatKeyRepo,
		apiKeyService:  apiKeyService,
		channelService: channelService,
		settingService: settingService,
	}
}

func (s *WebChatService) FeatureEnabled(ctx context.Context) bool {
	if s == nil || s.settingService == nil {
		return false
	}
	return s.settingService.GetWebChatRuntime(ctx).Enabled
}

func (s *WebChatService) Options(ctx context.Context, userID int64) (*WebChatOptions, error) {
	if !s.FeatureEnabled(ctx) {
		return &WebChatOptions{Enabled: false, Groups: []WebChatGroupOption{}}, nil
	}

	groups, err := s.webChatGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	options := &WebChatOptions{
		Enabled: true,
		Groups:  groups,
	}
	for i := range groups {
		if len(groups[i].Models) == 0 {
			continue
		}
		gid := groups[i].ID
		options.DefaultGroupID = &gid
		options.DefaultModel = groups[i].Models[0].Name
		break
	}
	return options, nil
}

func (s *WebChatService) CreateSession(ctx context.Context, userID int64, req WebChatCreateSessionRequest) (*WebChatSession, error) {
	if !s.FeatureEnabled(ctx) {
		return nil, ErrWebChatDisabled
	}
	group, model, err := s.validateGroupModel(ctx, userID, req.GroupID, req.Model)
	if err != nil {
		return nil, err
	}

	session := &WebChatSession{
		UserID:  userID,
		GroupID: group.ID,
		Model:   model.Name,
		Title:   buildWebChatTitle(model.Name),
	}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("create web chat session: %w", err)
	}
	session.GroupName = group.Name
	session.Platform = group.Platform
	return session, nil
}

func (s *WebChatService) ListSessions(ctx context.Context, userID int64) ([]WebChatSession, error) {
	if !s.FeatureEnabled(ctx) {
		return nil, ErrWebChatDisabled
	}
	return s.repo.ListSessions(ctx, userID)
}

func (s *WebChatService) GetSession(ctx context.Context, userID, sessionID int64) (*WebChatSession, error) {
	if !s.FeatureEnabled(ctx) {
		return nil, ErrWebChatDisabled
	}
	return s.repo.GetSession(ctx, userID, sessionID)
}

func (s *WebChatService) DeleteSession(ctx context.Context, userID, sessionID int64) error {
	if !s.FeatureEnabled(ctx) {
		return ErrWebChatDisabled
	}
	return s.repo.DeleteSession(ctx, userID, sessionID)
}

func (s *WebChatService) ListMessages(ctx context.Context, userID, sessionID int64) ([]WebChatMessage, error) {
	if !s.FeatureEnabled(ctx) {
		return nil, ErrWebChatDisabled
	}
	if _, err := s.repo.GetSession(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	return s.repo.ListMessages(ctx, userID, sessionID)
}

func (s *WebChatService) PrepareSend(ctx context.Context, userID, sessionID int64, req WebChatSendMessageRequest) (*WebChatSession, *APIKey, []OpenAIChatMessage, *WebChatMessage, error) {
	if !s.FeatureEnabled(ctx) {
		return nil, nil, nil, nil, ErrWebChatDisabled
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, nil, nil, nil, ErrWebChatEmptyMessage
	}
	if utf8.RuneCountInString(content) > webChatMessageMaxChars {
		return nil, nil, nil, nil, ErrWebChatMessageTooLong
	}

	session, err := s.repo.GetSession(ctx, userID, sessionID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	groupID := req.GroupID
	if groupID <= 0 {
		groupID = session.GroupID
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		modelName = session.Model
	}
	group, model, err := s.validateGroupModel(ctx, userID, groupID, modelName)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	if group.ID != session.GroupID || model.Name != session.Model {
		if err := s.repo.UpdateSessionTarget(ctx, userID, session.ID, group.ID, model.Name); err != nil {
			return nil, nil, nil, nil, fmt.Errorf("update web chat session target: %w", err)
		}
		session.GroupID = group.ID
		session.Model = model.Name
	}

	managedKey, err := s.ensureManagedKey(ctx, userID, group)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	userMessage := &WebChatMessage{
		SessionID: session.ID,
		UserID:    userID,
		Role:      WebChatMessageRoleUser,
		Content:   content,
		Status:    WebChatMessageStatusCompleted,
	}
	if err := s.repo.CreateMessage(ctx, userMessage); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("create web chat user message: %w", err)
	}

	assistantMessage := &WebChatMessage{
		SessionID: session.ID,
		UserID:    userID,
		Role:      WebChatMessageRoleAssistant,
		Status:    WebChatMessageStatusStreaming,
	}
	if err := s.repo.CreateMessage(ctx, assistantMessage); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("create web chat assistant message: %w", err)
	}

	title := buildWebChatTitle(content)
	_ = s.repo.TouchSession(ctx, session.ID, title)

	messages, err := s.buildContextMessages(ctx, userID, session.ID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	session.GroupName = group.Name
	session.Platform = group.Platform
	return session, managedKey, messages, assistantMessage, nil
}

func (s *WebChatService) CompleteAssistantMessage(ctx context.Context, messageID int64, content string) error {
	return s.repo.UpdateMessageStatus(ctx, messageID, content, WebChatMessageStatusCompleted, "")
}

func (s *WebChatService) FailAssistantMessage(ctx context.Context, messageID int64, content, errorMessage string) error {
	status := WebChatMessageStatusError
	if strings.TrimSpace(content) != "" {
		status = WebChatMessageStatusPartial
	}
	return s.repo.UpdateMessageStatus(ctx, messageID, content, status, errorMessage)
}

func (s *WebChatService) webChatGroups(ctx context.Context, userID int64) ([]WebChatGroupOption, error) {
	if s.apiKeyService == nil {
		return nil, ErrWebChatNoGroups
	}
	groups, err := s.apiKeyService.GetAvailableGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]WebChatGroupOption, 0, len(groups))
	for i := range groups {
		g := groups[i]
		supported := s.displayModels(ctx, g.ID, g.Platform)
		models := make([]WebChatModelOption, 0, len(supported))
		for _, model := range supported {
			if strings.TrimSpace(model.Name) == "" {
				continue
			}
			models = append(models, WebChatModelOption{
				Name:    model.Name,
				Pricing: webChatPricingFromChannel(model.Pricing),
			})
		}
		out = append(out, WebChatGroupOption{
			ID:               g.ID,
			Name:             g.Name,
			Platform:         g.Platform,
			SubscriptionType: g.SubscriptionType,
			RateMultiplier:   g.RateMultiplier,
			Models:           models,
		})
	}
	return out, nil
}

func (s *WebChatService) validateGroupModel(ctx context.Context, userID, groupID int64, model string) (*WebChatGroupOption, *WebChatModelOption, error) {
	model = strings.TrimSpace(model)
	if groupID <= 0 {
		return nil, nil, ErrWebChatInvalidGroup
	}
	if model == "" {
		return nil, nil, ErrWebChatInvalidModel
	}
	groups, err := s.webChatGroups(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	for i := range groups {
		if groups[i].ID != groupID {
			continue
		}
		for j := range groups[i].Models {
			if strings.EqualFold(groups[i].Models[j].Name, model) {
				return &groups[i], &groups[i].Models[j], nil
			}
		}
		return nil, nil, ErrWebChatInvalidModel
	}
	return nil, nil, ErrWebChatInvalidGroup
}

func (s *WebChatService) displayModels(ctx context.Context, groupID int64, platform string) []SupportedModel {
	if s.channelService == nil {
		return nil
	}
	models := s.channelService.ListDisplayModelsForGroup(ctx, groupID, platform)
	out := make([]SupportedModel, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		name := strings.TrimSpace(model.Name)
		if name == "" || strings.Contains(name, "*") {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		model.Name = name
		out = append(out, model)
	}
	return out
}

func webChatPricingFromChannel(pricing *ChannelModelPricing) *WebChatModelPricing {
	if pricing == nil {
		return nil
	}
	mode := string(pricing.BillingMode)
	if mode == "" {
		mode = string(BillingModeToken)
	}
	return &WebChatModelPricing{
		BillingMode:      mode,
		InputPrice:       pricing.InputPrice,
		OutputPrice:      pricing.OutputPrice,
		CacheWritePrice:  pricing.CacheWritePrice,
		CacheReadPrice:   pricing.CacheReadPrice,
		ImageOutputPrice: pricing.ImageOutputPrice,
		PerRequestPrice:  pricing.PerRequestPrice,
		Intervals:        pricing.Intervals,
	}
}

func (s *WebChatService) ensureManagedKey(ctx context.Context, userID int64, group *WebChatGroupOption) (*APIKey, error) {
	if s.webChatKeyRepo == nil || s.apiKeyService == nil {
		return nil, infraerrors.InternalServer("WEB_CHAT_KEY_UNAVAILABLE", "web chat key manager is unavailable")
	}
	key, err := s.apiKeyService.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("generate web chat api key: %w", err)
	}
	apiKey, _, err := s.webChatKeyRepo.EnsureWebChatKey(ctx, userID, group.ID, group.Name, key)
	if err != nil {
		return nil, fmt.Errorf("ensure web chat api key: %w", err)
	}
	return apiKey, nil
}

type OpenAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (s *WebChatService) buildContextMessages(ctx context.Context, userID, sessionID int64) ([]OpenAIChatMessage, error) {
	items, err := s.repo.RecentMessages(ctx, userID, sessionID, webChatRecentMessageLimit)
	if err != nil {
		return nil, fmt.Errorf("list web chat context: %w", err)
	}
	total := 0
	keptReverse := make([]OpenAIChatMessage, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		msg := items[i]
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		chars := utf8.RuneCountInString(content)
		if total+chars > webChatContextMaxChars {
			if total == 0 {
				content = truncateRunes(content, webChatContextMaxChars)
				keptReverse = append(keptReverse, OpenAIChatMessage{
					Role:    msg.Role,
					Content: content,
				})
			}
			break
		}
		total += chars
		keptReverse = append(keptReverse, OpenAIChatMessage{
			Role:    msg.Role,
			Content: content,
		})
	}
	out := make([]OpenAIChatMessage, 0, len(keptReverse))
	for i := len(keptReverse) - 1; i >= 0; i-- {
		out = append(out, keptReverse[i])
	}
	return out, nil
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return string(runes[:max])
}

func buildWebChatTitle(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return "New chat"
	}
	runes := []rune(value)
	if len(runes) > 36 {
		return string(runes[:36]) + "..."
	}
	return value
}
