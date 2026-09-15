package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const webChatCatalogKey = "web_chat_model_catalog"

var webChatCatalogIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,60}$`)

// Capabilities an entry may advertise. An empty list means chat, so catalogs
// published before image support keep working untouched.
const (
	WebChatCapabilityChat  = "chat"
	WebChatCapabilityImage = "image"
)

// A catalog entry is a single, explicit route. It never grants group access.
type WebChatCatalogEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Brand       string `json:"brand"`
	Description string `json:"description"`
	GroupID     int64  `json:"group_id"`
	Model       string `json:"model"`
	Enabled     bool   `json:"enabled"`
	Recommended bool   `json:"recommended"`
	SortOrder   int    `json:"sort_order"`
	// Declared by an administrator rather than inferred from the model name, so
	// a newly named image model needs a catalog edit, not a release.
	Capabilities []string `json:"capabilities,omitempty"`
}

// An entry with no declared capability is a chat model, which is what every
// catalog contained before image tasks existed.
func (e WebChatCatalogEntry) capabilities() []string {
	if len(e.Capabilities) == 0 {
		return []string{WebChatCapabilityChat}
	}
	return e.Capabilities
}

func (e WebChatCatalogEntry) supports(capability string) bool {
	for _, c := range e.capabilities() {
		if c == capability {
			return true
		}
	}
	return false
}

type WebChatCatalogConfig struct {
	Entries []WebChatCatalogEntry `json:"entries"`
}

type WebChatCatalogOption struct {
	Capabilities []string `json:"capabilities"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Brand        string   `json:"brand"`
	Description  string   `json:"description"`
	Model        string   `json:"model"`
	BillingType  string   `json:"billing_type"`
	Recommended  bool     `json:"recommended"`
}

type webChatCatalogWriter interface {
	SaveWebChatCatalog(context.Context, WebChatCatalogConfig) error
}

func (s *SettingService) ReadWebChatCatalog(ctx context.Context) (WebChatCatalogConfig, error) {
	values, err := s.settingRepo.GetMultiple(ctx, []string{webChatCatalogKey})
	if err != nil {
		return WebChatCatalogConfig{}, err
	}
	cfg := WebChatCatalogConfig{Entries: []WebChatCatalogEntry{}}
	if raw := values[webChatCatalogKey]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

type webChatRuntimeContextKey struct{}

func (s *WebChatService) withRuntime(ctx context.Context) context.Context {
	if _, ok := ctx.Value(webChatRuntimeContextKey{}).(WebChatRuntime); ok {
		return ctx
	}
	return context.WithValue(ctx, webChatRuntimeContextKey{}, s.runtime(ctx))
}

// Route-specific IDs make stale browser selections fail closed after rerouting.
func (e WebChatCatalogEntry) selectionID() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", e.ID, e.GroupID, e.Model)))
	return fmt.Sprintf("%s-%x", e.ID, h[:8])
}

func (s *SettingService) SaveWebChatCatalog(ctx context.Context, cfg WebChatCatalogConfig) error {
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, webChatCatalogKey, string(data))
}

func (s *WebChatService) AdminModelCatalog(ctx context.Context) (WebChatCatalogConfig, error) {
	if reader, ok := s.settingService.(interface {
		ReadWebChatCatalog(context.Context) (WebChatCatalogConfig, error)
	}); ok {
		return reader.ReadWebChatCatalog(ctx)
	}
	if cfg := s.runtime(ctx).Catalog; cfg != nil {
		return *cfg, nil
	}
	return WebChatCatalogConfig{Entries: []WebChatCatalogEntry{}}, nil
}

// Validation is configuration-only: it does not issue billable model requests.
func (s *WebChatService) ValidateModelCatalog(ctx context.Context, cfg WebChatCatalogConfig) error {
	if len(cfg.Entries) > 100 {
		return infraerrors.BadRequest("WEB_CHAT_CATALOG_INVALID", "at most 100 models are allowed")
	}
	seen := map[string]bool{}
	routes := map[string]bool{}
	recommended := 0
	for _, e := range cfg.Entries {
		if !webChatCatalogIDPattern.MatchString(e.ID) || seen[e.ID] || strings.TrimSpace(e.Name) == "" || len(e.Name) > 100 || len(e.Description) > 300 || len(e.Brand) > 40 || e.GroupID <= 0 || strings.TrimSpace(e.Model) == "" || len(e.Model) > 200 {
			return infraerrors.BadRequest("WEB_CHAT_CATALOG_INVALID", "invalid or duplicate catalog entry: "+e.ID)
		}
		seen[e.ID] = true
		image := false
		for _, c := range e.capabilities() {
			switch c {
			case WebChatCapabilityChat:
			case WebChatCapabilityImage:
				image = true
			default:
				return infraerrors.BadRequest("WEB_CHAT_CATALOG_INVALID", "unknown capability on entry: "+e.ID)
			}
		}
		// Reject a mismatch at save time: an image model on the chat path is
		// refused before account selection, and a chat model on the images path
		// fails upstream. Either way the user would only see it after sending.
		if image != isOpenAIImageGenerationModel(e.Model) {
			if image {
				return infraerrors.BadRequest("WEB_CHAT_CATALOG_MODEL_INVALID", "model does not generate images: "+e.ID)
			}
			return infraerrors.BadRequest("WEB_CHAT_CATALOG_MODEL_INVALID", "image model must declare the image capability: "+e.ID)
		}
		route := fmt.Sprintf("%d:%s", e.GroupID, e.Model)
		if routes[route] {
			return infraerrors.BadRequest("WEB_CHAT_CATALOG_INVALID", "duplicate model route: "+e.ID)
		}
		routes[route] = true
		if e.Enabled && e.Recommended {
			recommended++
		}
		if !e.Enabled {
			continue
		}
		if s.catalogGroups == nil {
			return ErrWebChatInvalidGroup
		}
		g, err := s.catalogGroups.GetByID(ctx, e.GroupID)
		if err != nil || g == nil || g.Status != StatusActive || g.ClaudeCodeOnly {
			return infraerrors.BadRequest("WEB_CHAT_CATALOG_GROUP_INVALID", "group is unavailable to browser chat: "+e.ID)
		}
		found := false
		for _, m := range s.displayModels(ctx, g.ID, g.Platform) {
			if m.Name == e.Model {
				found = true
				break
			}
		}
		if !found {
			return infraerrors.BadRequest("WEB_CHAT_CATALOG_MODEL_INVALID", "model is not advertised by group: "+e.ID)
		}
	}
	if recommended > 1 {
		return infraerrors.BadRequest("WEB_CHAT_CATALOG_INVALID", "only one enabled model may be recommended")
	}
	return nil
}

func (s *WebChatService) SaveModelCatalog(ctx context.Context, cfg WebChatCatalogConfig) error {
	if err := s.ValidateModelCatalog(ctx, cfg); err != nil {
		return err
	}
	w, ok := s.settingService.(webChatCatalogWriter)
	if !ok {
		return infraerrors.InternalServer("WEB_CHAT_CATALOG_UNAVAILABLE", "catalog store unavailable")
	}
	if cfg.Entries == nil {
		cfg.Entries = []WebChatCatalogEntry{}
	}
	return w.SaveWebChatCatalog(ctx, cfg)
}

func catalogAllows(cfg *WebChatCatalogConfig, groupID int64, model string) bool {
	if cfg == nil {
		return true
	} // Existing deployments/clients until first publication.
	for _, e := range cfg.Entries {
		if e.Enabled && e.GroupID == groupID && e.Model == model {
			return true
		}
	}
	return false
}

func (s *WebChatService) resolveCatalogSelection(ctx context.Context, id string) (int64, string, error) {
	if cfg := s.runtime(ctx).Catalog; cfg != nil {
		for _, e := range cfg.Entries {
			if e.Enabled && e.selectionID() == id {
				return e.GroupID, e.Model, nil
			}
		}
	}
	return 0, "", infraerrors.BadRequest("WEB_CHAT_MODEL_UNAVAILABLE", "model configuration changed or is unavailable; reload and select a model")
}

func catalogOptions(cfg *WebChatCatalogConfig, groups []WebChatGroupOption) []WebChatCatalogOption {
	out := []WebChatCatalogOption{}
	if cfg == nil {
		return out
	}
	entries := append([]WebChatCatalogEntry(nil), cfg.Entries...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].SortOrder < entries[j].SortOrder })
	for _, e := range entries {
		if !e.Enabled {
			continue
		}
		for _, g := range groups {
			if g.ID != e.GroupID {
				continue
			}
			for _, m := range g.Models {
				if m.Name == e.Model {
					out = append(out, WebChatCatalogOption{Capabilities: e.capabilities(), ID: e.selectionID(), Name: e.Name, Brand: e.Brand, Description: e.Description, Model: e.Model, BillingType: g.SubscriptionType, Recommended: e.Recommended})
					break
				}
			}
		}
	}
	return out
}

func catalogTargetID(cfg *WebChatCatalogConfig, groupID int64, model string) string {
	if cfg != nil {
		for _, e := range cfg.Entries {
			if e.Enabled && e.GroupID == groupID && e.Model == model {
				return e.selectionID()
			}
		}
	}
	return ""
}
