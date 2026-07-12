package service

import (
	"encoding/json"
	"time"
)

const (
	WebChatMessageRoleUser      = "user"
	WebChatMessageRoleAssistant = "assistant"

	WebChatMessageStatusStreaming = "streaming"
	WebChatMessageStatusCompleted = "completed"
	WebChatMessageStatusError     = "error"
	WebChatMessageStatusPartial   = "partial"
)

type WebChatModelPricing struct {
	BillingMode       string            `json:"billing_mode"`
	InputPrice        *float64          `json:"input_price,omitempty"`
	OutputPrice       *float64          `json:"output_price,omitempty"`
	CacheWritePrice   *float64          `json:"cache_write_price,omitempty"`
	CacheWrite5mPrice *float64          `json:"cache_write_5m_price,omitempty"`
	CacheWrite1hPrice *float64          `json:"cache_write_1h_price,omitempty"`
	CacheReadPrice    *float64          `json:"cache_read_price,omitempty"`
	ImageOutputPrice  *float64          `json:"image_output_price,omitempty"`
	PerRequestPrice   *float64          `json:"per_request_price,omitempty"`
	Intervals         []PricingInterval `json:"intervals,omitempty"`
}

type WebChatModelOption struct {
	Name    string               `json:"name"`
	Pricing *WebChatModelPricing `json:"pricing,omitempty"`
}

type WebChatGroupOption struct {
	ID               int64                `json:"id"`
	Name             string               `json:"name"`
	Platform         string               `json:"platform"`
	SubscriptionType string               `json:"subscription_type"`
	RateMultiplier   float64              `json:"rate_multiplier"`
	Models           []WebChatModelOption `json:"models"`
}

type WebChatOptions struct {
	Enabled          bool                  `json:"enabled"`
	Groups           []WebChatGroupOption  `json:"groups"`
	DefaultGroupID   *int64                `json:"default_group_id,omitempty"`
	DefaultModel     string                `json:"default_model,omitempty"`
	ProjectsEnabled  bool                  `json:"projects_enabled"`
	TemplatesEnabled bool                  `json:"templates_enabled"`
	HistoryEnabled   bool                  `json:"history_enabled"`
	FilesEnabled     bool                  `json:"files_enabled"`
	FileFormats      []string              `json:"file_formats,omitempty"`
	FileLimits       WebChatDocumentLimits `json:"file_limits"`
}

type WebChatSession struct {
	ID                  int64      `json:"id"`
	UserID              int64      `json:"user_id"`
	GroupID             int64      `json:"group_id"`
	GroupName           string     `json:"group_name,omitempty"`
	Platform            string     `json:"platform,omitempty"`
	Model               string     `json:"model"`
	Title               string     `json:"title"`
	PinnedAt            *time.Time `json:"pinned_at,omitempty"`
	SystemPrompt        string     `json:"system_prompt"`
	Temperature         *float64   `json:"temperature,omitempty"`
	MaxOutputTokens     int        `json:"max_output_tokens"`
	ProjectID           *int64     `json:"project_id,omitempty"`
	ProjectName         string     `json:"project_name,omitempty"`
	DefaultTemplateID   *int64     `json:"default_template_id,omitempty"`
	ActiveLeafMessageID *int64     `json:"active_leaf_message_id,omitempty"`
	KnowledgeEnabled    bool       `json:"knowledge_enabled"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	DeletedAt           *time.Time `json:"deleted_at,omitempty"`
}

type WebChatMessage struct {
	ID                  int64           `json:"id"`
	SessionID           int64           `json:"session_id"`
	UserID              int64           `json:"user_id"`
	Role                string          `json:"role"`
	Content             string          `json:"content"`
	Status              string          `json:"status"`
	ErrorMessage        string          `json:"error_message,omitempty"`
	RequestID           string          `json:"request_id,omitempty"`
	InputTokens         int64           `json:"input_tokens"`
	OutputTokens        int64           `json:"output_tokens"`
	CacheReadTokens     int64           `json:"cache_read_tokens"`
	CacheCreationTokens int64           `json:"cache_creation_tokens"`
	LogicalID           int64           `json:"logical_id"`
	ParentMessageID     *int64          `json:"parent_message_id,omitempty"`
	VersionIndex        int             `json:"version_index"`
	VersionCount        int             `json:"version_count"`
	VersionReason       string          `json:"version_reason"`
	TemplateID          *int64          `json:"template_id,omitempty"`
	Sources             []WebChatSource `json:"sources"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

type WebChatCreateSessionRequest struct {
	GroupID           int64
	Model             string
	ProjectID         *int64
	DefaultTemplateID *int64
}

type WebChatSendMessageRequest struct {
	Content          string
	GroupID          int64
	Model            string
	TemplateID       *int64
	KnowledgeEnabled *bool
	DocumentIDs      []int64
}

type WebChatPatchSessionRequest struct {
	Title                *string
	Pinned               *bool
	SystemPrompt         *string
	Temperature          *float64
	TemperatureSet       bool
	MaxOutputTokens      *int
	ProjectID            *int64
	ProjectIDSet         bool
	DefaultTemplateID    *int64
	DefaultTemplateIDSet bool
	KnowledgeEnabled     *bool
}

type WebChatProject struct {
	ID                int64     `json:"id"`
	UserID            int64     `json:"user_id"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	Color             string    `json:"color"`
	SortOrder         int       `json:"sort_order"`
	DefaultGroupID    *int64    `json:"default_group_id,omitempty"`
	DefaultModel      string    `json:"default_model,omitempty"`
	DefaultTemplateID *int64    `json:"default_template_id,omitempty"`
	SessionCount      int       `json:"session_count"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type WebChatProjectInput struct {
	Name              string `json:"name"`
	Description       string `json:"description"`
	Color             string `json:"color"`
	SortOrder         int    `json:"sort_order"`
	DefaultGroupID    *int64 `json:"default_group_id"`
	DefaultModel      string `json:"default_model"`
	DefaultTemplateID *int64 `json:"default_template_id"`
}

type WebChatTemplateVariable struct {
	Name         string `json:"name"`
	Label        string `json:"label"`
	Required     bool   `json:"required"`
	DefaultValue string `json:"default_value"`
	Type         string `json:"type"`
}

type WebChatTemplate struct {
	ID               int64           `json:"id"`
	Scope            string          `json:"scope"`
	UserID           *int64          `json:"user_id,omitempty"`
	SourceTemplateID *int64          `json:"source_template_id,omitempty"`
	Name             string          `json:"name"`
	Category         string          `json:"category"`
	Description      string          `json:"description"`
	Body             string          `json:"body"`
	Variables        json.RawMessage `json:"variables"`
	Language         string          `json:"language"`
	Enabled          bool            `json:"enabled"`
	SortOrder        int             `json:"sort_order"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type WebChatTemplateInput struct {
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
	Body        string          `json:"body"`
	Variables   json.RawMessage `json:"variables"`
	Language    string          `json:"language"`
	Enabled     bool            `json:"enabled"`
	SortOrder   int             `json:"sort_order"`
}

type WebChatUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
}

type WebChatGeneration struct {
	Session          *WebChatSession
	APIKey           *APIKey
	Messages         []OpenAIChatMessage
	AssistantMessage *WebChatMessage
	Sources          []WebChatSource
}
