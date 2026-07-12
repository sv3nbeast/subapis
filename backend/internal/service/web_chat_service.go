package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	webChatRecentMessageLimit   = 40
	webChatContextMaxChars      = 60000
	webChatMessageMaxChars      = 20000
	webChatSystemPromptMaxChars = 8000
	webChatSessionTitleMaxChars = 200
)

var (
	ErrWebChatDisabled               = infraerrors.NotFound("WEB_CHAT_DISABLED", "web chat is disabled")
	ErrWebChatSessionNotFound        = infraerrors.NotFound("WEB_CHAT_SESSION_NOT_FOUND", "web chat session not found")
	ErrWebChatMessageNotFound        = infraerrors.NotFound("WEB_CHAT_MESSAGE_NOT_FOUND", "web chat message not found")
	ErrWebChatNoGroups               = infraerrors.Forbidden("WEB_CHAT_NO_GROUPS", "no available groups for web chat")
	ErrWebChatInvalidGroup           = infraerrors.Forbidden("WEB_CHAT_INVALID_GROUP", "group is not available for web chat")
	ErrWebChatInvalidModel           = infraerrors.BadRequest("WEB_CHAT_INVALID_MODEL", "model is not available for this group")
	ErrWebChatEmptyMessage           = infraerrors.BadRequest("WEB_CHAT_EMPTY_MESSAGE", "message cannot be empty")
	ErrWebChatMessageTooLong         = infraerrors.BadRequest("WEB_CHAT_MESSAGE_TOO_LONG", "message is too long")
	ErrWebChatSystemPromptTooLong    = infraerrors.BadRequest("WEB_CHAT_SYSTEM_PROMPT_TOO_LONG", "system prompt is too long")
	ErrWebChatInvalidTemperature     = infraerrors.BadRequest("WEB_CHAT_INVALID_TEMPERATURE", "temperature must be between 0 and 2")
	ErrWebChatInvalidMaxOutputTokens = infraerrors.BadRequest("WEB_CHAT_INVALID_MAX_OUTPUT_TOKENS", "max output tokens must be between 1 and 32768")
	ErrWebChatInvalidTitle           = infraerrors.BadRequest("WEB_CHAT_INVALID_TITLE", "title must not be empty or exceed 200 characters")
	ErrWebChatSessionBusy            = infraerrors.Conflict("WEB_CHAT_SESSION_BUSY", "this web chat session is already generating a response")
	ErrWebChatProjectsDisabled       = infraerrors.NotFound("WEB_CHAT_PROJECTS_DISABLED", "web chat projects are disabled")
	ErrWebChatTemplatesDisabled      = infraerrors.NotFound("WEB_CHAT_TEMPLATES_DISABLED", "web chat templates are disabled")
	ErrWebChatHistoryDisabled        = infraerrors.NotFound("WEB_CHAT_HISTORY_DISABLED", "web chat history is disabled")
	ErrWebChatProjectNotFound        = infraerrors.NotFound("WEB_CHAT_PROJECT_NOT_FOUND", "web chat project not found")
	ErrWebChatTemplateNotFound       = infraerrors.NotFound("WEB_CHAT_TEMPLATE_NOT_FOUND", "web chat template not found")
	ErrWebChatInvalidProject         = infraerrors.BadRequest("WEB_CHAT_INVALID_PROJECT", "invalid web chat project")
	ErrWebChatInvalidTemplate        = infraerrors.BadRequest("WEB_CHAT_INVALID_TEMPLATE", "invalid web chat template")
	ErrWebChatTemplateLimit          = infraerrors.Conflict("WEB_CHAT_TEMPLATE_LIMIT", "personal template limit reached")
)

type WebChatRepository interface {
	CreateSession(ctx context.Context, session *WebChatSession) error
	ListSessions(ctx context.Context, userID int64, query string) ([]WebChatSession, error)
	GetSession(ctx context.Context, userID, sessionID int64) (*WebChatSession, error)
	UpdateSession(ctx context.Context, userID, sessionID int64, req WebChatPatchSessionRequest) (*WebChatSession, error)
	UpdateSessionTarget(ctx context.Context, userID, sessionID, groupID int64, model string) error
	DeleteSession(ctx context.Context, userID, sessionID int64) error
	CreateTurn(ctx context.Context, userID, sessionID int64, content, title string, templateID *int64) (*WebChatMessage, *WebChatMessage, error)
	RegenerateTurn(ctx context.Context, userID, sessionID, messageID int64) (*WebChatMessage, error)
	ReviseTurn(ctx context.Context, userID, sessionID, messageID int64, content, title string) (*WebChatMessage, *WebChatMessage, error)
	UpdateMessageResult(ctx context.Context, userID, messageID int64, content, status, errorMessage, requestID string, usage WebChatUsage) (*WebChatMessage, error)
	ListMessages(ctx context.Context, userID, sessionID int64) ([]WebChatMessage, error)
	RecentMessages(ctx context.Context, userID, sessionID int64, limit int) ([]WebChatMessage, error)
	ListMessageVersions(ctx context.Context, userID, sessionID, messageID int64) ([]WebChatMessage, error)
	ActivateMessageVersion(ctx context.Context, userID, sessionID, messageID int64) error
	ListProjects(ctx context.Context, userID int64) ([]WebChatProject, error)
	CreateProject(ctx context.Context, project *WebChatProject) error
	UpdateProject(ctx context.Context, userID, projectID int64, input WebChatProjectInput) (*WebChatProject, error)
	DeleteProject(ctx context.Context, userID, projectID int64) error
	ListTemplates(ctx context.Context, userID int64, includeDisabledSystem bool) ([]WebChatTemplate, error)
	GetTemplate(ctx context.Context, userID, templateID int64, allowSystem bool) (*WebChatTemplate, error)
	CreateTemplate(ctx context.Context, template *WebChatTemplate) error
	UpdateTemplate(ctx context.Context, userID, templateID int64, input WebChatTemplateInput, system bool) (*WebChatTemplate, error)
	DeleteTemplate(ctx context.Context, userID, templateID int64, system bool) error
	CountPersonalTemplates(ctx context.Context, userID int64) (int, error)
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
	documents      *WebChatDocumentService
}

func (s *WebChatService) SetDocumentService(documents *WebChatDocumentService) {
	s.documents = documents
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

func (s *WebChatService) runtime(ctx context.Context) WebChatRuntime {
	if s == nil || s.settingService == nil {
		return WebChatRuntime{}
	}
	return s.settingService.GetWebChatRuntime(ctx)
}

func (s *WebChatService) FeatureEnabled(ctx context.Context) bool {
	if s == nil || s.settingService == nil {
		return false
	}
	return s.runtime(ctx).Enabled
}

func (s *WebChatService) Options(ctx context.Context, userID int64) (*WebChatOptions, error) {
	runtime := s.runtime(ctx)
	if !runtime.Enabled {
		return &WebChatOptions{Enabled: false, Groups: []WebChatGroupOption{}}, nil
	}

	groups, err := s.webChatGroups(ctx, userID)
	if err != nil {
		return nil, err
	}
	options := &WebChatOptions{
		Enabled:          true,
		Groups:           groups,
		ProjectsEnabled:  runtime.ProjectsEnabled,
		TemplatesEnabled: runtime.TemplatesEnabled,
		HistoryEnabled:   runtime.HistoryEnabled,
		FilesEnabled:     runtime.FilesEnabled,
		FileFormats:      []string{"pdf", "docx", "txt", "md", "csv"},
	}
	if s.documents != nil {
		options.FileLimits = s.documents.Limits(ctx)
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
	if req.ProjectID != nil {
		if !s.runtime(ctx).ProjectsEnabled {
			return nil, ErrWebChatProjectsDisabled
		}
		project, err := s.projectByID(ctx, userID, *req.ProjectID)
		if err != nil {
			return nil, err
		}
		if req.GroupID <= 0 && project.DefaultGroupID != nil {
			req.GroupID = *project.DefaultGroupID
		}
		if strings.TrimSpace(req.Model) == "" {
			req.Model = project.DefaultModel
		}
		if req.DefaultTemplateID == nil {
			req.DefaultTemplateID = project.DefaultTemplateID
		}
	}
	if req.DefaultTemplateID != nil && !s.runtime(ctx).TemplatesEnabled {
		return nil, ErrWebChatTemplatesDisabled
	}
	group, model, err := s.validateGroupModel(ctx, userID, req.GroupID, req.Model)
	if err != nil {
		return nil, err
	}

	session := &WebChatSession{
		UserID:            userID,
		GroupID:           group.ID,
		Model:             model.Name,
		Title:             "",
		MaxOutputTokens:   8192,
		KnowledgeEnabled:  true,
		ProjectID:         req.ProjectID,
		DefaultTemplateID: req.DefaultTemplateID,
	}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return nil, fmt.Errorf("create web chat session: %w", err)
	}
	session.GroupName = group.Name
	session.Platform = group.Platform
	return session, nil
}

func (s *WebChatService) ListSessions(ctx context.Context, userID int64, query string) ([]WebChatSession, error) {
	if !s.FeatureEnabled(ctx) {
		return nil, ErrWebChatDisabled
	}
	return s.repo.ListSessions(ctx, userID, strings.TrimSpace(query))
}

func (s *WebChatService) UpdateSession(ctx context.Context, userID, sessionID int64, req WebChatPatchSessionRequest) (*WebChatSession, error) {
	if !s.FeatureEnabled(ctx) {
		return nil, ErrWebChatDisabled
	}
	if req.Title != nil {
		v := strings.TrimSpace(*req.Title)
		if v == "" || utf8.RuneCountInString(v) > webChatSessionTitleMaxChars {
			return nil, ErrWebChatInvalidTitle
		}
		*req.Title = v
	}
	if req.SystemPrompt != nil {
		v := strings.TrimSpace(*req.SystemPrompt)
		if utf8.RuneCountInString(v) > webChatSystemPromptMaxChars {
			return nil, ErrWebChatSystemPromptTooLong
		}
		*req.SystemPrompt = v
	}
	if req.TemperatureSet && req.Temperature != nil && (*req.Temperature < 0 || *req.Temperature > 2) {
		return nil, ErrWebChatInvalidTemperature
	}
	if req.MaxOutputTokens != nil && (*req.MaxOutputTokens < 1 || *req.MaxOutputTokens > 32768) {
		return nil, ErrWebChatInvalidMaxOutputTokens
	}
	if req.ProjectIDSet {
		if !s.runtime(ctx).ProjectsEnabled {
			return nil, ErrWebChatProjectsDisabled
		}
		if req.ProjectID != nil {
			if _, err := s.projectByID(ctx, userID, *req.ProjectID); err != nil {
				return nil, err
			}
		}
	}
	if req.DefaultTemplateIDSet {
		if !s.runtime(ctx).TemplatesEnabled {
			return nil, ErrWebChatTemplatesDisabled
		}
		if req.DefaultTemplateID != nil {
			if _, err := s.repo.GetTemplate(ctx, userID, *req.DefaultTemplateID, false); err != nil {
				return nil, ErrWebChatTemplateNotFound
			}
		}
	}
	if req.KnowledgeEnabled != nil && !s.runtime(ctx).FilesEnabled && *req.KnowledgeEnabled {
		return nil, ErrWebChatFilesDisabled
	}
	return s.repo.UpdateSession(ctx, userID, sessionID, req)
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
	if s.documents != nil && s.documents.repo != nil {
		if err := s.documents.repo.MarkSessionDocumentsDeleting(ctx, userID, sessionID); err != nil {
			return err
		}
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

func (s *WebChatService) PrepareSend(ctx context.Context, userID, sessionID int64, req WebChatSendMessageRequest) (*WebChatGeneration, error) {
	if !s.FeatureEnabled(ctx) {
		return nil, ErrWebChatDisabled
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, ErrWebChatEmptyMessage
	}
	if utf8.RuneCountInString(content) > webChatMessageMaxChars {
		return nil, ErrWebChatMessageTooLong
	}

	session, err := s.repo.GetSession(ctx, userID, sessionID)
	if err != nil {
		return nil, err
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
		return nil, err
	}

	if group.ID != session.GroupID || model.Name != session.Model {
		if err := s.repo.UpdateSessionTarget(ctx, userID, session.ID, group.ID, model.Name); err != nil {
			return nil, fmt.Errorf("update web chat session target: %w", err)
		}
		session.GroupID = group.ID
		session.Model = model.Name
	}

	managedKey, err := s.ensureManagedKey(ctx, userID, group)
	if err != nil {
		return nil, err
	}
	if req.TemplateID != nil {
		if !s.runtime(ctx).TemplatesEnabled {
			return nil, ErrWebChatTemplatesDisabled
		}
		if _, err := s.repo.GetTemplate(ctx, userID, *req.TemplateID, false); err != nil {
			return nil, ErrWebChatTemplateNotFound
		}
	}
	userMessage, assistantMessage, err := s.repo.CreateTurn(ctx, userID, session.ID, content, buildWebChatTitle(content), req.TemplateID)
	if err != nil {
		return nil, err
	}

	messages, err := s.buildContextMessages(ctx, userID, session.ID)
	if err != nil {
		_, _ = s.FailAssistantMessage(context.WithoutCancel(ctx), userID, assistantMessage.ID, "", err.Error(), "", WebChatUsage{})
		return nil, err
	}
	knowledgeEnabled := session.KnowledgeEnabled
	if req.KnowledgeEnabled != nil {
		knowledgeEnabled = *req.KnowledgeEnabled
	}
	var sources []WebChatSource
	if s.documents != nil {
		var knowledge string
		sources, knowledge, err = s.documents.PrepareKnowledge(ctx, userID, session, userMessage.ID, assistantMessage.ID, content, req.DocumentIDs, knowledgeEnabled)
		if err != nil {
			_, _ = s.FailAssistantMessage(context.WithoutCancel(ctx), userID, assistantMessage.ID, "", err.Error(), "", WebChatUsage{})
			return nil, err
		}
		if knowledge != "" {
			appendKnowledgeToLastUser(messages, knowledge)
		}
	}
	session.GroupName = group.Name
	session.Platform = group.Platform
	return &WebChatGeneration{Session: session, APIKey: managedKey, Messages: messages, AssistantMessage: assistantMessage, Sources: sources}, nil
}

func (s *WebChatService) CompleteAssistantMessage(ctx context.Context, userID, messageID int64, content, requestID string, usage WebChatUsage) (*WebChatMessage, error) {
	return s.repo.UpdateMessageResult(ctx, userID, messageID, content, WebChatMessageStatusCompleted, "", requestID, usage)
}

func (s *WebChatService) FailAssistantMessage(ctx context.Context, userID, messageID int64, content, errorMessage, requestID string, usage WebChatUsage) (*WebChatMessage, error) {
	status := WebChatMessageStatusError
	if strings.TrimSpace(content) != "" {
		status = WebChatMessageStatusPartial
	}
	return s.repo.UpdateMessageResult(ctx, userID, messageID, content, status, errorMessage, requestID, usage)
}

func (s *WebChatService) PrepareRegenerate(ctx context.Context, userID, sessionID, messageID int64) (*WebChatGeneration, error) {
	return s.prepareBranchGeneration(ctx, userID, sessionID, messageID, "", false)
}

func (s *WebChatService) PrepareRevise(ctx context.Context, userID, sessionID, messageID int64, content string) (*WebChatGeneration, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, ErrWebChatEmptyMessage
	}
	if utf8.RuneCountInString(content) > webChatMessageMaxChars {
		return nil, ErrWebChatMessageTooLong
	}
	return s.prepareBranchGeneration(ctx, userID, sessionID, messageID, content, true)
}

func (s *WebChatService) prepareBranchGeneration(ctx context.Context, userID, sessionID, messageID int64, content string, revise bool) (*WebChatGeneration, error) {
	if !s.FeatureEnabled(ctx) {
		return nil, ErrWebChatDisabled
	}
	session, err := s.repo.GetSession(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	group, _, err := s.validateGroupModel(ctx, userID, session.GroupID, session.Model)
	if err != nil {
		return nil, err
	}
	key, err := s.ensureManagedKey(ctx, userID, group)
	if err != nil {
		return nil, err
	}
	var assistant *WebChatMessage
	var requestedDocuments []int64
	if s.documents != nil && s.documents.repo != nil {
		requestedDocuments, _ = s.documents.repo.MessageDocumentIDs(ctx, userID, messageID)
	}
	if revise {
		_, assistant, err = s.repo.ReviseTurn(ctx, userID, sessionID, messageID, content, buildWebChatTitle(content))
	} else {
		assistant, err = s.repo.RegenerateTurn(ctx, userID, sessionID, messageID)
	}
	if err != nil {
		return nil, err
	}
	messages, err := s.buildContextMessages(ctx, userID, sessionID)
	if err != nil {
		_, _ = s.FailAssistantMessage(context.WithoutCancel(ctx), userID, assistant.ID, "", err.Error(), "", WebChatUsage{})
		return nil, err
	}
	var sources []WebChatSource
	if s.documents != nil && session.KnowledgeEnabled {
		query := lastUserContent(messages)
		var knowledge string
		sources, knowledge, err = s.documents.PrepareKnowledge(ctx, userID, session, 0, assistant.ID, query, requestedDocuments, true)
		if err != nil {
			_, _ = s.FailAssistantMessage(context.WithoutCancel(ctx), userID, assistant.ID, "", err.Error(), "", WebChatUsage{})
			return nil, err
		}
		if knowledge != "" {
			appendKnowledgeToLastUser(messages, knowledge)
		}
	}
	return &WebChatGeneration{Session: session, APIKey: key, Messages: messages, AssistantMessage: assistant, Sources: sources}, nil
}

func lastUserContent(messages []OpenAIChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == WebChatMessageRoleUser {
			return messages[i].Content
		}
	}
	return ""
}
func appendKnowledgeToLastUser(messages []OpenAIChatMessage, knowledge string) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == WebChatMessageRoleUser {
			messages[i].Content += knowledge
			return
		}
	}
}

func (s *WebChatService) ListMessageVersions(ctx context.Context, userID, sessionID, messageID int64) ([]WebChatMessage, error) {
	if !s.runtime(ctx).HistoryEnabled {
		return nil, ErrWebChatHistoryDisabled
	}
	if _, err := s.repo.GetSession(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	return s.repo.ListMessageVersions(ctx, userID, sessionID, messageID)
}

func (s *WebChatService) ActivateMessageVersion(ctx context.Context, userID, sessionID, messageID int64) error {
	if !s.runtime(ctx).HistoryEnabled {
		return ErrWebChatHistoryDisabled
	}
	return s.repo.ActivateMessageVersion(ctx, userID, sessionID, messageID)
}

func (s *WebChatService) ListProjects(ctx context.Context, userID int64) ([]WebChatProject, error) {
	if !s.runtime(ctx).ProjectsEnabled {
		return nil, ErrWebChatProjectsDisabled
	}
	return s.repo.ListProjects(ctx, userID)
}

func (s *WebChatService) CreateProject(ctx context.Context, userID int64, input WebChatProjectInput) (*WebChatProject, error) {
	if !s.runtime(ctx).ProjectsEnabled {
		return nil, ErrWebChatProjectsDisabled
	}
	if err := s.validateProjectInput(ctx, userID, &input); err != nil {
		return nil, err
	}
	project := &WebChatProject{UserID: userID, Name: input.Name, Description: input.Description, Color: input.Color, SortOrder: input.SortOrder, DefaultGroupID: input.DefaultGroupID, DefaultModel: input.DefaultModel, DefaultTemplateID: input.DefaultTemplateID}
	if err := s.repo.CreateProject(ctx, project); err != nil {
		return nil, err
	}
	return project, nil
}

func (s *WebChatService) UpdateProject(ctx context.Context, userID, projectID int64, input WebChatProjectInput) (*WebChatProject, error) {
	if !s.runtime(ctx).ProjectsEnabled {
		return nil, ErrWebChatProjectsDisabled
	}
	if _, err := s.projectByID(ctx, userID, projectID); err != nil {
		return nil, err
	}
	if err := s.validateProjectInput(ctx, userID, &input); err != nil {
		return nil, err
	}
	return s.repo.UpdateProject(ctx, userID, projectID, input)
}

func (s *WebChatService) DeleteProject(ctx context.Context, userID, projectID int64) error {
	if !s.runtime(ctx).ProjectsEnabled {
		return ErrWebChatProjectsDisabled
	}
	if _, err := s.projectByID(ctx, userID, projectID); err != nil {
		return err
	}
	if s.documents != nil && s.documents.repo != nil {
		if err := s.documents.repo.MarkProjectDocumentsDeleting(ctx, userID, projectID); err != nil {
			return err
		}
	}
	return s.repo.DeleteProject(ctx, userID, projectID)
}

func (s *WebChatService) projectByID(ctx context.Context, userID, projectID int64) (*WebChatProject, error) {
	items, err := s.repo.ListProjects(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == projectID {
			return &items[i], nil
		}
	}
	return nil, ErrWebChatProjectNotFound
}

func (s *WebChatService) validateProjectInput(ctx context.Context, userID int64, input *WebChatProjectInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.DefaultModel = strings.TrimSpace(input.DefaultModel)
	input.Color = strings.TrimSpace(input.Color)
	if utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 120 || utf8.RuneCountInString(input.Description) > 500 {
		return ErrWebChatInvalidProject
	}
	if input.Color == "" {
		input.Color = "#14b8a6"
	}
	if ok, _ := regexp.MatchString(`^#[0-9a-fA-F]{6}$`, input.Color); !ok {
		return ErrWebChatInvalidProject
	}
	if input.DefaultGroupID != nil {
		if _, _, err := s.validateGroupModel(ctx, userID, *input.DefaultGroupID, input.DefaultModel); err != nil {
			return err
		}
	}
	if input.DefaultGroupID == nil && input.DefaultModel != "" {
		return ErrWebChatInvalidProject
	}
	if input.DefaultTemplateID != nil {
		if !s.runtime(ctx).TemplatesEnabled {
			return ErrWebChatTemplatesDisabled
		}
		if _, err := s.repo.GetTemplate(ctx, userID, *input.DefaultTemplateID, false); err != nil {
			return ErrWebChatTemplateNotFound
		}
	}
	return nil
}

func (s *WebChatService) ListTemplates(ctx context.Context, userID int64, admin bool) ([]WebChatTemplate, error) {
	if !admin && !s.runtime(ctx).TemplatesEnabled {
		return nil, ErrWebChatTemplatesDisabled
	}
	return s.repo.ListTemplates(ctx, userID, admin)
}

func (s *WebChatService) CreatePersonalTemplate(ctx context.Context, userID int64, input WebChatTemplateInput) (*WebChatTemplate, error) {
	return s.createTemplate(ctx, userID, input, false, nil)
}
func (s *WebChatService) CreateSystemTemplate(ctx context.Context, input WebChatTemplateInput) (*WebChatTemplate, error) {
	if err := validateWebChatTemplateInput(&input); err != nil {
		return nil, err
	}
	t := &WebChatTemplate{Scope: "system", Name: input.Name, Category: input.Category, Description: input.Description, Body: input.Body, Variables: input.Variables, Language: input.Language, Enabled: input.Enabled, SortOrder: input.SortOrder}
	if err := s.repo.CreateTemplate(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}
func (s *WebChatService) CopyTemplate(ctx context.Context, userID, templateID int64) (*WebChatTemplate, error) {
	if !s.runtime(ctx).TemplatesEnabled {
		return nil, ErrWebChatTemplatesDisabled
	}
	source, err := s.repo.GetTemplate(ctx, userID, templateID, false)
	if err != nil {
		return nil, ErrWebChatTemplateNotFound
	}
	input := WebChatTemplateInput{Name: source.Name, Category: source.Category, Description: source.Description, Body: source.Body, Variables: source.Variables, Language: source.Language, Enabled: true, SortOrder: source.SortOrder}
	return s.createTemplate(ctx, userID, input, false, &templateID)
}
func (s *WebChatService) createTemplate(ctx context.Context, userID int64, input WebChatTemplateInput, system bool, source *int64) (*WebChatTemplate, error) {
	if !s.runtime(ctx).TemplatesEnabled {
		return nil, ErrWebChatTemplatesDisabled
	}
	if err := validateWebChatTemplateInput(&input); err != nil {
		return nil, err
	}
	scope := "personal"
	var owner *int64 = &userID
	if system {
		scope = "system"
		owner = nil
	} else {
		n, err := s.repo.CountPersonalTemplates(ctx, userID)
		if err != nil {
			return nil, err
		}
		if n >= 100 {
			return nil, ErrWebChatTemplateLimit
		}
	}
	t := &WebChatTemplate{Scope: scope, UserID: owner, SourceTemplateID: source, Name: input.Name, Category: input.Category, Description: input.Description, Body: input.Body, Variables: input.Variables, Language: input.Language, Enabled: input.Enabled, SortOrder: input.SortOrder}
	if err := s.repo.CreateTemplate(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}
func (s *WebChatService) UpdateTemplate(ctx context.Context, userID, templateID int64, input WebChatTemplateInput, system bool) (*WebChatTemplate, error) {
	if !system && !s.runtime(ctx).TemplatesEnabled {
		return nil, ErrWebChatTemplatesDisabled
	}
	if err := validateWebChatTemplateInput(&input); err != nil {
		return nil, err
	}
	return s.repo.UpdateTemplate(ctx, userID, templateID, input, system)
}
func (s *WebChatService) DeleteTemplate(ctx context.Context, userID, templateID int64, system bool) error {
	if !system && !s.runtime(ctx).TemplatesEnabled {
		return ErrWebChatTemplatesDisabled
	}
	return s.repo.DeleteTemplate(ctx, userID, templateID, system)
}

var webChatTemplatePlaceholder = regexp.MustCompile(`\{\{\s*([a-zA-Z][a-zA-Z0-9_]*)\s*\}\}`)

func validateWebChatTemplateInput(input *WebChatTemplateInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Category = strings.TrimSpace(input.Category)
	input.Description = strings.TrimSpace(input.Description)
	input.Body = strings.TrimSpace(input.Body)
	input.Language = strings.TrimSpace(input.Language)
	if input.Language == "" {
		input.Language = "zh-CN"
	}
	if utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 120 || utf8.RuneCountInString(input.Description) > 500 || utf8.RuneCountInString(input.Body) < 1 || utf8.RuneCountInString(input.Body) > 20000 {
		return ErrWebChatInvalidTemplate
	}
	var variables []WebChatTemplateVariable
	if len(input.Variables) == 0 {
		input.Variables = json.RawMessage("[]")
	}
	if err := json.Unmarshal(input.Variables, &variables); err != nil || len(variables) > 20 {
		return ErrWebChatInvalidTemplate
	}
	defined := map[string]bool{}
	for i := range variables {
		variables[i].Name = strings.TrimSpace(variables[i].Name)
		variables[i].Label = strings.TrimSpace(variables[i].Label)
		if ok, _ := regexp.MatchString(`^[a-zA-Z][a-zA-Z0-9_]*$`, variables[i].Name); !ok || defined[variables[i].Name] || variables[i].Label == "" || (variables[i].Type != "singleline" && variables[i].Type != "multiline") {
			return ErrWebChatInvalidTemplate
		}
		defined[variables[i].Name] = true
	}
	for _, match := range webChatTemplatePlaceholder.FindAllStringSubmatch(input.Body, -1) {
		if !defined[match[1]] {
			return ErrWebChatInvalidTemplate
		}
	}
	encoded, _ := json.Marshal(variables)
	input.Variables = encoded
	return nil
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
		BillingMode:       mode,
		InputPrice:        pricing.InputPrice,
		OutputPrice:       pricing.OutputPrice,
		CacheWritePrice:   pricing.CacheWritePrice,
		CacheWrite5mPrice: pricing.CacheWrite5mPrice,
		CacheWrite1hPrice: pricing.CacheWrite1hPrice,
		CacheReadPrice:    pricing.CacheReadPrice,
		ImageOutputPrice:  pricing.ImageOutputPrice,
		PerRequestPrice:   pricing.PerRequestPrice,
		Intervals:         pricing.Intervals,
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
