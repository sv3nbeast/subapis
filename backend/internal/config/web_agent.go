package config

// Optional isolated file-task runtime. Enabling it never bypasses the existing
// web-chat feature flag or user/group/billing permissions.
type WebAgentConfig struct {
	Enabled       bool   `mapstructure:"enabled"`
	RendererURL   string `mapstructure:"renderer_url"`
	RendererToken string `mapstructure:"renderer_token" json:"-"`
	StoragePath   string `mapstructure:"storage_path"`
}
