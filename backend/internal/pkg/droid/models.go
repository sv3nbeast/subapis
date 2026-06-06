package droid

type Model struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

var DefaultModels = []Model{
	{ID: "claude-sonnet-4-20250514", Type: "model", DisplayName: "Claude Sonnet 4", CreatedAt: "2025-05-14T00:00:00Z"},
	{ID: "claude-opus-4-20250514", Type: "model", DisplayName: "Claude Opus 4", CreatedAt: "2025-05-14T00:00:00Z"},
	{ID: "gpt-5", Type: "model", DisplayName: "GPT-5", CreatedAt: "2025-08-07T00:00:00Z"},
	{ID: "gpt-5-2025-08-07", Type: "model", DisplayName: "GPT-5", CreatedAt: "2025-08-07T00:00:00Z"},
}

func DefaultModelIDs() []string {
	ids := make([]string, len(DefaultModels))
	for i, model := range DefaultModels {
		ids[i] = model.ID
	}
	return ids
}
