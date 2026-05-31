package service

import "time"

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
	Enabled        bool                 `json:"enabled"`
	Groups         []WebChatGroupOption `json:"groups"`
	DefaultGroupID *int64               `json:"default_group_id,omitempty"`
	DefaultModel   string               `json:"default_model,omitempty"`
}

type WebChatSession struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
	GroupID   int64      `json:"group_id"`
	GroupName string     `json:"group_name,omitempty"`
	Platform  string     `json:"platform,omitempty"`
	Model     string     `json:"model"`
	Title     string     `json:"title"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

type WebChatMessage struct {
	ID           int64     `json:"id"`
	SessionID    int64     `json:"session_id"`
	UserID       int64     `json:"user_id"`
	Role         string    `json:"role"`
	Content      string    `json:"content"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type WebChatCreateSessionRequest struct {
	GroupID int64
	Model   string
}

type WebChatSendMessageRequest struct {
	Content string
	GroupID int64
	Model   string
}
