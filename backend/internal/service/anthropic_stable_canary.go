package service

// This file is the deliberately small, local-only Anthropic OAuth identity
// canary. It is kept separate from both the legacy OAuth mimicry path and the
// unfinished multi-account stable-identity executor. The canary has one exact
// group/account pair, one fixed account device id, no application failover,
// and only the single reactive 401 refresh/replay permitted by the reference
// claude-gateway implementation.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

const (
	anthropicStableCanaryModeName       = "canary"
	anthropicStableCanarySharedModeName = "canary_shared"
)

const anthropicStableCanaryDurableBlockTimeout = 2 * time.Second

const anthropicStableCanaryRefreshTimeout = 30 * time.Second

// Session ownership is a safety gate, not an unbounded request queue. A
// database outage must fail closed within a short, explicit budget instead of
// consuming the entire client TTFT window before any upstream byte is sent.
const anthropicStableCanarySessionClaimTimeout = 2 * time.Second

// The reference claude-gateway selects its OAuth Bearer branch from the raw
// access-token prefix (strings.HasPrefix(token, "sk-ant-oat")).  Stable mode
// must reject a wrong-shaped value before constructing an upstream request;
// trimming it first would turn malformed credential storage into a different
// wire identity and could accidentally authorize an API-key-shaped token.
const anthropicStableOAuthAccessTokenPrefix = "sk-ant-oat"

func validateAnthropicStableOAuthAccessToken(token string) error {
	if token == "" {
		return errors.New("stable canary access token is empty")
	}
	if token != strings.TrimSpace(token) {
		return errors.New("stable canary access token contains surrounding whitespace")
	}
	if !strings.HasPrefix(token, anthropicStableOAuthAccessTokenPrefix) {
		return errors.New("stable canary access token has an unsupported OAuth prefix")
	}
	return nil
}

// Refresh tokens are opaque OAuth credentials. Keep their stored and wire
// representation byte-for-byte intact; trimming would silently turn a
// malformed credential into a different credential and make reference-wire
// comparisons misleading.
func validateAnthropicStableOAuthRefreshToken(token string) error {
	if token == "" {
		return errors.New("stable canary refresh token is empty")
	}
	if token != strings.TrimSpace(token) {
		return errors.New("stable canary refresh token contains surrounding whitespace")
	}
	return nil
}

var (
	errAnthropicStableCanaryDisabled        = errors.New("Anthropic stable canary is disabled")
	errAnthropicStableCanaryGroupMismatch   = errors.New("Anthropic stable canary group mismatch")
	errAnthropicStableCanaryAccountInvalid  = errors.New("Anthropic stable canary account is not eligible")
	errAnthropicStableCanaryModelRestricted = errors.New("Anthropic stable canary model is restricted")
	ErrAnthropicStableCanaryReserved        = errors.New("Anthropic stable canary account is reserved")
)

type anthropicStableCanaryRefreshFailureClass uint8

const (
	anthropicStableCanaryRefreshFailureTransient anthropicStableCanaryRefreshFailureClass = iota + 1
	anthropicStableCanaryRefreshFailureCredentialRejected
	anthropicStableCanaryRefreshFailureCredentialAmbiguous
)

// anthropicStableCanaryRefreshError retains only a classification and the
// wrapped local cause. Upstream response text is never returned to callers or
// persisted in account.extra.
type anthropicStableCanaryRefreshError struct {
	class  anthropicStableCanaryRefreshFailureClass
	status int
	err    error
}

func (e *anthropicStableCanaryRefreshError) Error() string {
	if e == nil {
		return "stable canary token refresh failed"
	}
	if e.status > 0 {
		return fmt.Sprintf("stable canary token refresh failed: status %d", e.status)
	}
	if e.err != nil {
		return fmt.Sprintf("stable canary token refresh failed: %v", e.err)
	}
	return "stable canary token refresh failed"
}

func (e *anthropicStableCanaryRefreshError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func anthropicStableCanaryRefreshFailure(class anthropicStableCanaryRefreshFailureClass, status int, err error) error {
	return &anthropicStableCanaryRefreshError{class: class, status: status, err: err}
}

func anthropicStableCanaryRefreshFailureClassOf(err error) anthropicStableCanaryRefreshFailureClass {
	var classified *anthropicStableCanaryRefreshError
	if errors.As(err, &classified) && classified != nil {
		return classified.class
	}
	return anthropicStableCanaryRefreshFailureCredentialAmbiguous
}

// AnthropicStableCanaryStateRepository is intentionally a small optional
// extension rather than part of AccountRepository. The D1 canary must continue
// to work with existing repository/test doubles, while the production account
// repository can durably pause a rejected credential. The interface can later
// be replaced by the generation-aware stable-identity lifecycle without
// changing all account repository consumers.
type AnthropicStableCanaryStateRepository interface {
	BlockAnthropicStableCanary(ctx context.Context, accountID int64, reason string) error
}

// AnthropicStableCanaryLeaseRepository serializes the reserved credential
// across gateway processes. A regular account concurrency slot has an expiry
// and can lapse during a long stream; this lease remains tied to one database
// connection until the request finishes or the process disconnects.
type AnthropicStableCanaryLeaseRepository interface {
	AcquireAnthropicStableCanaryLease(ctx context.Context, accountID int64) (release func() error, err error)
}

const (
	anthropicStableCanaryBlockReasonCredentialRejected = "credential_rejected"
	anthropicStableCanaryBlockReasonRefreshFailed      = "refresh_failed"
)

// NormalizeAnthropicStableCanaryBlockReason keeps the persistence boundary
// finite and non-sensitive even if a future caller reaches the optional
// repository interface directly.
func NormalizeAnthropicStableCanaryBlockReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case anthropicStableCanaryBlockReasonRefreshFailed:
		return anthropicStableCanaryBlockReasonRefreshFailed
	case anthropicStableCanaryBlockReasonCredentialRejected:
		fallthrough
	default:
		// Never persist upstream response text, token material, or arbitrary
		// caller input in accounts.extra.
		return anthropicStableCanaryBlockReasonCredentialRejected
	}
}

type anthropicStableCanaryRuntime struct {
	mu      sync.Mutex
	clients map[string]*http.Client
	slots   map[int64]chan struct{}
	blocked map[int64]string
}

func newAnthropicStableCanaryRuntime() *anthropicStableCanaryRuntime {
	return &anthropicStableCanaryRuntime{
		clients: make(map[string]*http.Client),
		slots:   make(map[int64]chan struct{}),
		blocked: make(map[int64]string),
	}
}

// IsAnthropicStableCanaryGroup reports whether the exact configured group is
// currently enabled. It intentionally returns false for every group when the
// process switch is off, preserving all existing routing behavior.
func (s *GatewayService) IsAnthropicStableCanaryGroup(groupID *int64) bool {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.AnthropicStableCanary.Enabled || groupID == nil {
		return false
	}
	canary := s.cfg.Gateway.AnthropicStableCanary
	return canary.GroupID > 0 && *groupID == canary.GroupID
}

// AnthropicStableCanaryOwnerAllowed applies the D1 single-owner gate before
// reading or routing a request. Zero owner configuration fails closed.
func (s *GatewayService) AnthropicStableCanaryOwnerAllowed(userID int64) bool {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.AnthropicStableCanary.Enabled {
		return false
	}
	owner := s.cfg.Gateway.AnthropicStableCanary.OwnerUserID
	return owner > 0 && userID == owner
}

// AnthropicStableCanaryAPIKeyAllowed narrows D1 to one explicitly enrolled
// credential. Other keys owned by the same user must not enter this wire path.
func (s *GatewayService) AnthropicStableCanaryAPIKeyAllowed(apiKeyID int64) bool {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.AnthropicStableCanary.Enabled {
		return false
	}
	configured := s.cfg.Gateway.AnthropicStableCanary.APIKeyID
	return configured > 0 && apiKeyID == configured
}

// GetAnthropicStableCanaryAccount loads the configured account directly. It
// intentionally bypasses scheduler snapshots because the account is marked
// unschedulable while reserved for this path.
func (s *GatewayService) GetAnthropicStableCanaryAccount(ctx context.Context, groupID int64) (*Account, error) {
	canary, err := s.anthropicStableCanaryConfig()
	if err != nil || groupID != canary.GroupID || s.accountRepo == nil {
		return nil, errAnthropicStableCanaryGroupMismatch
	}
	account, err := s.accountRepo.GetByID(ctx, canary.AccountID)
	if err != nil {
		return nil, err
	}
	if err := s.validateAnthropicStableCanaryAccount(ctx, groupID, account); err != nil {
		return nil, err
	}
	return account, nil
}

// AnthropicStableCanaryAccountID returns the exact account configured for the
// local canary. A zero value means the feature is disabled or incomplete.
func (s *GatewayService) AnthropicStableCanaryAccountID() int64 {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.AnthropicStableCanary.Enabled {
		return 0
	}
	return s.cfg.Gateway.AnthropicStableCanary.AccountID
}

func (s *GatewayService) AnthropicStableCanaryMaxBodyBytes() int64 {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.AnthropicStableCanary.Enabled {
		return 0
	}
	return s.cfg.Gateway.AnthropicStableCanary.MaxBodyBytes
}

// validateAnthropicStableCanaryAdminUpdate keeps the D1 reservation under the
// dedicated lifecycle path. Generic account updates persist a full account
// snapshot, so even a display-only edit could overwrite protected identity
// fields from a stale snapshot and must fail closed while the account is reserved.
func validateAnthropicStableCanaryAdminUpdate(account *Account, input *UpdateAccountInput) error {
	if input == nil {
		return nil
	}
	if err := validateAnthropicStableIdentityAdminUpdate(account, input); err != nil {
		return err
	}
	if AnthropicStableCanaryExtraUpdateTouchesManagedFields(input.Extra) {
		return fmt.Errorf("%w: enrollment fields require the dedicated canary lifecycle", ErrAnthropicStableCanaryReserved)
	}
	if account == nil || !account.HasAnthropicStableCanaryManagedFields() {
		return nil
	}
	return fmt.Errorf("%w: generic account form cannot update a reserved canary", ErrAnthropicStableCanaryReserved)
}

func validateAnthropicStableCanaryAccountServiceUpdate(account *Account, input UpdateAccountRequest) error {
	if err := validateAnthropicStableIdentityAccountServiceUpdate(account, input); err != nil {
		return err
	}
	if input.Extra != nil && AnthropicStableCanaryExtraUpdateTouchesManagedFields(*input.Extra) {
		return fmt.Errorf("%w: enrollment fields require the dedicated canary lifecycle", ErrAnthropicStableCanaryReserved)
	}
	if account == nil || !account.HasAnthropicStableCanaryManagedFields() {
		return nil
	}
	return fmt.Errorf("%w: generic account service cannot update a reserved canary", ErrAnthropicStableCanaryReserved)
}

func anthropicStableCanaryBulkUpdateTouchesManagedFields(input *BulkUpdateAccountsInput) bool {
	if input == nil {
		return false
	}
	return input.ProxyID != nil || input.Concurrency != nil || input.Priority != nil || input.RateMultiplier != nil ||
		input.LoadFactor != nil || input.Status != "" || input.Schedulable != nil || input.GroupIDs != nil ||
		len(input.Credentials) > 0 || len(input.Extra) > 0 || input.ProbeEnabled != nil
}

// AnthropicStableCanaryAccountBulkUpdateTouchesManagedFields is the repository-
// facing form of the lifecycle write guard for the lower-level repository DTO.
// Keeping the predicate in service avoids a second, drifting list of restricted
// bulk fields in the SQL adapter.
func AnthropicStableCanaryAccountBulkUpdateTouchesManagedFields(input *AccountBulkUpdate) bool {
	if input == nil {
		return false
	}
	return input.ProxyID != nil || input.Concurrency != nil || input.Priority != nil || input.RateMultiplier != nil ||
		input.LoadFactor != nil || input.Status != nil || input.Schedulable != nil || input.ProbeEnabled != nil ||
		len(input.Credentials) > 0 || len(input.Extra) > 0
}

func anthropicStableCanaryProfileKnown(profileID string) bool {
	_, ok := anthropicStableIngressProfiles[canonicalAnthropicStableIngressProfileID(profileID)]
	return ok
}

// IsKnownAnthropicStableCanaryProfile exposes the finite, capture-backed
// profile allow-list to the operator lifecycle without exposing its wire
// headers or permitting a free-form profile.
func IsKnownAnthropicStableCanaryProfile(profileID string) bool {
	return anthropicStableCanaryProfileKnown(profileID)
}

// ValidateAnthropicStableCanaryEnrollmentAccount checks the account state that
// must be true before lifecycle enrollment. It returns no credential values and
// must run while the account still belongs to the ordinary scheduler.
func ValidateAnthropicStableCanaryEnrollmentAccount(account *Account, deviceID, profileID string) error {
	if account == nil || account.ID <= 0 || account.Platform != PlatformAnthropic ||
		account.Type != AccountTypeOAuth || account.Status != StatusActive || !account.Schedulable ||
		account.HasAnthropicStableCanaryManagedFields() ||
		!account.isRuntimeAvailableIgnoringLegacySchedulable() {
		return errAnthropicStableCanaryAccountInvalid
	}
	if !IsValidAnthropicStableDeviceID(deviceID) || !anthropicStableCanaryProfileKnown(profileID) {
		return errAnthropicStableCanaryAccountInvalid
	}
	return validateAnthropicStableCanaryIdentitySettings(account)
}

// ValidateAnthropicStableCanaryEnrolledAccount verifies the durable identity
// shape shared by lifecycle inspect/disable and the request hot path. It does
// not reject a durable block or transient availability window so an operator
// can still inspect and retire a fenced account safely.
func ValidateAnthropicStableCanaryEnrolledAccount(account *Account, groupID int64) error {
	if account == nil || account.ID <= 0 || account.Schedulable ||
		!account.IsAnthropicStableCanaryEnabled() || !account.IsAnthropicStableCanaryReserved() ||
		!accountHasOnlyGroup(account, groupID) ||
		!IsValidAnthropicStableDeviceID(account.AnthropicStableCanaryDeviceID()) ||
		!anthropicStableCanaryProfileKnown(account.AnthropicStableCanaryProfileID()) {
		return errAnthropicStableCanaryAccountInvalid
	}
	previous, captured := account.AnthropicStableCanaryPreviousSchedulable()
	if !captured || !previous {
		return errAnthropicStableCanaryAccountInvalid
	}
	return validateAnthropicStableCanaryIdentitySettings(account)
}

func validateAnthropicStableCanaryIdentitySettings(account *Account) error {
	if account == nil || account.Platform != PlatformAnthropic || account.Type != AccountTypeOAuth ||
		account.Concurrency != 1 || account.ProxyID != nil || account.Proxy != nil ||
		account.ProxyFallbackOriginID != nil || account.ParentAccountID != nil {
		return errAnthropicStableCanaryAccountInvalid
	}
	if account.IsAnthropicOAuthPassthroughEnabled() || account.IsCustomBaseURLEnabled() ||
		account.IsCacheTTLOverrideEnabled() || account.IsSessionIDMaskingEnabled() ||
		account.IsTLSFingerprintEnabled() {
		return errAnthropicStableCanaryAccountInvalid
	}
	if strings.TrimSpace(account.GetCredential("base_url")) != "" ||
		validateAnthropicStableOAuthAccessToken(account.GetCredential("access_token")) != nil ||
		validateAnthropicStableOAuthRefreshToken(account.GetCredential("refresh_token")) != nil ||
		len(account.GetModelMapping()) > 0 || len(account.GetCompactModelMapping()) > 0 {
		return errAnthropicStableCanaryAccountInvalid
	}
	if raw, ok := account.Credentials["header_overrides"]; ok && raw != nil {
		switch value := raw.(type) {
		case map[string]any:
			if len(value) > 0 {
				return errAnthropicStableCanaryAccountInvalid
			}
		case map[string]string:
			if len(value) > 0 {
				return errAnthropicStableCanaryAccountInvalid
			}
		default:
			return errAnthropicStableCanaryAccountInvalid
		}
	}
	if enabled, ok := account.Extra["anthropic_passthrough"].(bool); ok && enabled {
		return errAnthropicStableCanaryAccountInvalid
	}
	return nil
}

func accountHasOnlyGroup(account *Account, groupID int64) bool {
	if account == nil || groupID <= 0 {
		return false
	}
	seen := make(map[int64]struct{}, len(account.GroupIDs)+len(account.AccountGroups))
	for _, id := range account.GroupIDs {
		if id > 0 {
			seen[id] = struct{}{}
		}
	}
	for _, binding := range account.AccountGroups {
		if binding.GroupID > 0 {
			seen[binding.GroupID] = struct{}{}
		}
	}
	if len(seen) != 1 {
		return false
	}
	_, ok := seen[groupID]
	return ok
}

func (s *GatewayService) anthropicStableCanaryConfig() (config.GatewayAnthropicStableCanaryConfig, error) {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.AnthropicStableCanary.Enabled {
		return config.GatewayAnthropicStableCanaryConfig{}, errAnthropicStableCanaryDisabled
	}
	canary := s.cfg.Gateway.AnthropicStableCanary
	if canary.GroupID <= 0 || canary.AccountID <= 0 {
		return config.GatewayAnthropicStableCanaryConfig{}, fmt.Errorf("%w: group/account binding is incomplete", errAnthropicStableCanaryDisabled)
	}
	if canary.SharedUsers {
		if canary.OwnerUserID != 0 || canary.APIKeyID != 0 || len(canary.SharedAPIKeyIDs) == 0 ||
			canary.SessionGeneration <= 0 || len(strings.TrimSpace(canary.SessionHMACKey)) < 32 {
			return config.GatewayAnthropicStableCanaryConfig{}, fmt.Errorf("%w: shared binding is invalid", errAnthropicStableCanaryDisabled)
		}
	} else if canary.OwnerUserID <= 0 || canary.APIKeyID <= 0 {
		return config.GatewayAnthropicStableCanaryConfig{}, fmt.Errorf("%w: owner/key binding is incomplete", errAnthropicStableCanaryDisabled)
	}
	if canary.MaxBodyBytes <= 0 || canary.MaxBodyBytes > AnthropicStableIngressMaxBodyBytes {
		return config.GatewayAnthropicStableCanaryConfig{}, fmt.Errorf("%w: max body size is invalid", errAnthropicStableCanaryDisabled)
	}
	return canary, nil
}

// validateAnthropicStableCanaryGroup verifies the safety boundary that cannot
// be represented by an account flag alone. The group must be Anthropic,
// exclusive, Claude-Code-only, OAuth-only, and have no fallback path.
func (s *GatewayService) validateAnthropicStableCanaryGroup(ctx context.Context, groupID int64) error {
	if s == nil || s.groupRepo == nil || groupID <= 0 {
		return errAnthropicStableCanaryGroupMismatch
	}
	group, err := s.groupRepo.GetByID(ctx, groupID)
	if err != nil || group == nil {
		if err != nil {
			return fmt.Errorf("load canary group: %w", err)
		}
		return errAnthropicStableCanaryGroupMismatch
	}
	if group.ID != groupID || !group.IsActive() || group.Platform != PlatformAnthropic || !group.IsExclusive ||
		!group.ClaudeCodeOnly || !group.RequireOAuthOnly ||
		group.FallbackGroupID != nil || group.FallbackGroupIDOnInvalidRequest != nil || group.ModelRoutingEnabled ||
		group.AccountCount != 1 {
		return errAnthropicStableCanaryGroupMismatch
	}
	if s.accountRepo != nil {
		accounts, err := s.accountRepo.ListByGroup(ctx, groupID)
		if err != nil || len(accounts) != 1 || accounts[0].ID != s.AnthropicStableCanaryAccountID() {
			return errAnthropicStableCanaryGroupMismatch
		}
	}
	return nil
}

// validateAnthropicStableCanaryModelAccess preserves the gateway's channel
// allowlist without changing a single request byte. A configured model rewrite
// is incompatible with D1: silently applying it would alter the captured wire,
// while silently ignoring it would bypass administrator routing policy.
func (s *GatewayService) validateAnthropicStableCanaryModelAccess(ctx context.Context, groupID int64, model string) error {
	if s == nil || s.channelService == nil {
		return nil
	}
	channel, err := s.channelService.GetChannelForGroup(ctx, groupID)
	if err != nil {
		return fmt.Errorf("load stable canary channel policy: %w", err)
	}
	if channel == nil {
		return nil
	}
	mapping := s.channelService.ResolveChannelMapping(ctx, groupID, model)
	if mapping.Mapped && mapping.MappedModel != model {
		return fmt.Errorf("%w: channel model mapping would rewrite the raw request", errAnthropicStableCanaryAccountInvalid)
	}
	if channel.RestrictModels && s.channelService.IsModelRestricted(ctx, groupID, model) {
		return fmt.Errorf("%w: %s", errAnthropicStableCanaryModelRestricted, model)
	}
	return nil
}

// validateAnthropicStableCanaryAccount verifies the exact account and all
// settings that could otherwise silently change the upstream-visible identity.
func (s *GatewayService) validateAnthropicStableCanaryAccount(ctx context.Context, groupID int64, account *Account) error {
	canary, err := s.anthropicStableCanaryConfig()
	if err != nil {
		return err
	}
	if groupID != canary.GroupID {
		return errAnthropicStableCanaryGroupMismatch
	}
	if err := s.validateAnthropicStableCanaryGroup(ctx, groupID); err != nil {
		return err
	}
	if account == nil {
		return errAnthropicStableCanaryAccountInvalid
	}
	// A durable block is a credential-generation fence, not a transient
	// scheduler status. It must be honored on every fresh account load, including
	// after a process restart, until an explicit canary lifecycle reset clears it.
	if account.IsAnthropicStableCanaryBlocked() {
		return errAnthropicStableCanaryAccountInvalid
	}
	if account.ID != canary.AccountID || !account.isRuntimeAvailableIgnoringLegacySchedulable() {
		return errAnthropicStableCanaryAccountInvalid
	}
	return ValidateAnthropicStableCanaryEnrolledAccount(account, groupID)
}

// IsAnthropicStableCanaryAccount reports the cheap route marker used by the
// handler before Forward. Full eligibility is checked again immediately before
// any upstream request, so a stale scheduler object cannot bypass the guard.
func (s *GatewayService) IsAnthropicStableCanaryAccount(groupID *int64, account *Account) bool {
	if s == nil || account == nil || groupID == nil || s.cfg == nil || !s.cfg.Gateway.AnthropicStableCanary.Enabled {
		return false
	}
	canary := s.cfg.Gateway.AnthropicStableCanary
	return *groupID == canary.GroupID && account.ID == canary.AccountID && account.IsAnthropicStableCanaryEnabled()
}

func stableCanaryIngressHeaders(c *gin.Context) http.Header {
	if c != nil {
		if value, ok := c.Get(anthropicOAuthIngressHeaderKey); ok {
			if header, ok := value.(http.Header); ok && header != nil {
				return header
			}
		}
		if c.Request != nil {
			return c.Request.Header
		}
	}
	return nil
}

func stableCanaryContentTypeIsJSON(c *gin.Context) bool {
	if c == nil {
		return false
	}
	raw := strings.TrimSpace(c.GetHeader("Content-Type"))
	if raw == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(raw)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func parseAnthropicStableCanaryIngress(c *gin.Context, body []byte) (*AnthropicStableIngressRequest, error) {
	if c == nil || c.Request == nil || c.Request.URL == nil || !stableCanaryContentTypeIsJSON(c) {
		return nil, ErrAnthropicStableIngressMalformed
	}
	header := stableCanaryIngressHeaders(c)
	profileID := DetectAnthropicStableIngressProfile(header.Get("User-Agent"), header.Get("anthropic-beta"))
	if profileID == "" {
		return nil, ErrAnthropicStableIngressNotClaudeCode
	}
	return ParseAnthropicStableIngressProfile(
		c.Request.Method,
		c.Request.URL.Path,
		c.Request.URL.RawQuery,
		c.GetHeader("Content-Encoding"),
		header.Get("User-Agent"),
		header.Get("x-app"),
		header.Get("X-Claude-Code-Session-Id"),
		header.Get("anthropic-beta"),
		header.Get("anthropic-version"),
		profileID,
		body,
	)
}

// parseAnthropicStableIdentityIngress keeps the shared stable scheduler
// compatible with native Claude Code upgrades. The capture-backed D1 canary
// above remains exact; shared identity admission validates the native client
// shape and session/device invariants without pinning feature beta strings.
func parseAnthropicStableIdentityIngress(c *gin.Context, body []byte) (*AnthropicStableIngressRequest, error) {
	if c == nil || c.Request == nil || c.Request.URL == nil || !stableCanaryContentTypeIsJSON(c) {
		return nil, ErrAnthropicStableIngressMalformed
	}
	header := stableCanaryIngressHeaders(c)
	return ParseAnthropicStableIdentityIngress(
		c.Request.Method,
		c.Request.URL.Path,
		c.Request.URL.RawQuery,
		c.GetHeader("Content-Encoding"),
		header.Get("User-Agent"),
		header.Get("x-app"),
		header.Get("X-Claude-Code-Session-Id"),
		body,
	)
}

// InspectAnthropicStableCanaryIngress validates a raw request without invoking
// ParseGatewayRequest. The handler uses it only to learn stream/model shape
// before acquiring ordinary user billing/concurrency gates.
func InspectAnthropicStableCanaryIngress(c *gin.Context, body []byte) (*AnthropicStableIngressRequest, error) {
	return parseAnthropicStableCanaryIngress(c, body)
}

// InspectAnthropicStableIdentityIngress validates a raw shared-scheduler
// request without invoking ParseGatewayRequest or mutating the request body.
func InspectAnthropicStableIdentityIngress(c *gin.Context, body []byte) (*AnthropicStableIngressRequest, error) {
	return parseAnthropicStableIdentityIngress(c, body)
}

func stableCanaryFailMessage(err error) string {
	if err == nil {
		return "Request is not eligible for the configured Claude Code canary"
	}
	// Do not expose account flags, device identifiers, proxy details, or token
	// refresh internals to API callers. The detailed cause remains in logs.
	switch {
	case errors.Is(err, ErrAnthropicStableCanarySessionOwnerConflict):
		return "This Claude Code session belongs to another user"
	case errors.Is(err, ErrAnthropicStableCanarySessionBindingUnavailable):
		return "The Claude Code session is temporarily unavailable"
	case errors.Is(err, ErrAnthropicStableIngressNotClaudeCode),
		errors.Is(err, ErrAnthropicStableIngressMalformed),
		errors.Is(err, ErrAnthropicStableIngressDuplicateKey):
		return "Request is not a supported Claude Code /v1/messages request"
	case errors.Is(err, errAnthropicStableCanaryModelRestricted):
		return "Requested model is not available for this API key/group"
	default:
		return "The configured Claude Code canary is temporarily unavailable"
	}
}

func (s *GatewayService) stableCanaryReject(c *gin.Context, status int, cause error) error {
	if c != nil && !IsResponseCommitted(c) {
		c.JSON(status, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "invalid_request_error",
				"message": stableCanaryFailMessage(cause),
			},
		})
		MarkResponseCommitted(c)
	}
	return fmt.Errorf("anthropic stable canary rejected request: %w", cause)
}

func (r *anthropicStableCanaryRuntime) acquire(ctx context.Context, accountID int64) (func(), error) {
	if r == nil || accountID <= 0 {
		return nil, errors.New("stable canary runtime is not configured")
	}
	r.mu.Lock()
	if r.slots[accountID] == nil {
		r.slots[accountID] = make(chan struct{}, 1)
	}
	slot := r.slots[accountID]
	r.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case slot <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-slot }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *anthropicStableCanaryRuntime) block(accountID int64, reason string) {
	if r == nil || accountID <= 0 {
		return
	}
	reason = NormalizeAnthropicStableCanaryBlockReason(reason)
	r.mu.Lock()
	if r.blocked == nil {
		r.blocked = make(map[int64]string)
	}
	r.blocked[accountID] = reason
	r.mu.Unlock()
}

func (r *anthropicStableCanaryRuntime) blockReason(accountID int64) string {
	if r == nil || accountID <= 0 {
		return "runtime_unavailable"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.blocked[accountID]
}

func (r *anthropicStableCanaryRuntime) clearBlock(accountID int64) {
	if r == nil || accountID <= 0 {
		return
	}
	r.mu.Lock()
	delete(r.blocked, accountID)
	r.mu.Unlock()
}

// blockAnthropicStableCanary records the local fail-closed state first, then
// persists the same finite reason code when the concrete repository supports
// the optional D1 state extension. Memory is authoritative for the current
// request even if a database write is unavailable; the warning makes a failed
// durability boundary visible to operators instead of silently reopening the
// account after a restart.
func (s *GatewayService) blockAnthropicStableCanary(ctx context.Context, accountID int64, reason string) {
	if s == nil || accountID <= 0 {
		return
	}
	reason = NormalizeAnthropicStableCanaryBlockReason(reason)
	if s.anthropicStableCanary != nil {
		s.anthropicStableCanary.block(accountID, reason)
	}
	if repo, ok := s.accountRepo.(AnthropicStableCanaryStateRepository); ok {
		// A definitive credential rejection has already happened upstream. Its
		// fail-closed marker must survive a client disconnect that races the error
		// response, otherwise a process restart could silently reopen the same
		// credential. Detach cancellation but keep a short hard persistence bound.
		persistBase := context.Background()
		if ctx != nil {
			persistBase = context.WithoutCancel(ctx)
		}
		persistCtx, cancel := context.WithTimeout(persistBase, anthropicStableCanaryDurableBlockTimeout)
		defer cancel()
		if err := repo.BlockAnthropicStableCanary(persistCtx, accountID, reason); err != nil {
			logger.LegacyPrintf("service.gateway", "[Anthropic Stable Canary] durable block failed account=%d reason=%s err=%v", accountID, reason, err)
		}
	}
}

// roundTripAnthropicStableCanary invokes the dedicated transport directly.
// This intentionally skips http.Client's redirect state machine: a 307/308 can
// otherwise construct a replay request (and call GetBody) before CheckRedirect
// runs. Transport-internal connection recovery remains unchanged, while an
// upstream redirect is returned as the final raw response and never receives a
// second Bearer-authenticated request.
func roundTripAnthropicStableCanary(client *http.Client, request *http.Request) (*http.Response, error) {
	if client == nil || request == nil {
		return nil, errors.New("stable canary HTTP request is incomplete")
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if transport == nil {
		return nil, errors.New("stable canary HTTP transport is unavailable")
	}
	return transport.RoundTrip(request)
}

func (s *GatewayService) anthropicStableCanaryHTTPClient(account *Account) (*http.Client, error) {
	if s == nil || s.anthropicStableCanary == nil || account == nil {
		return nil, errors.New("stable canary runtime is not configured")
	}
	key := fmt.Sprintf("%d", account.ID)
	identityPrefix := ""
	if account.HasAnthropicStableIdentityManagedFields() {
		deviceID := account.AnthropicStableIdentityDeviceID()
		generation := account.AnthropicStableIdentityGeneration()
		if generation <= 0 || !IsValidAnthropicStableIdentityDeviceID(deviceID) {
			return nil, errors.New("stable identity transport generation is invalid")
		}
		identityPrefix = fmt.Sprintf("identity:%d:", account.ID)
		key = fmt.Sprintf("%s%d:%s", identityPrefix, generation, deviceID[:12])
	}
	s.anthropicStableCanary.mu.Lock()
	if client := s.anthropicStableCanary.clients[key]; client != nil {
		s.anthropicStableCanary.mu.Unlock()
		return client, nil
	}
	// A generation/device transition must never reuse the previous identity's
	// idle TCP/TLS pool. Cross-process lifecycle serialization guarantees no
	// old-generation upstream request is active when the new generation commits;
	// keying and pruning here applies that boundary independently on every
	// gateway instance when it next observes the route.
	staleClients := make([]*http.Client, 0)
	if identityPrefix != "" {
		for existingKey, existingClient := range s.anthropicStableCanary.clients {
			if strings.HasPrefix(existingKey, identityPrefix) && existingKey != key {
				delete(s.anthropicStableCanary.clients, existingKey)
				if existingClient != nil {
					staleClients = append(staleClients, existingClient)
				}
			}
		}
	}
	s.anthropicStableCanary.mu.Unlock()
	for _, staleClient := range staleClients {
		staleClient.CloseIdleConnections()
	}

	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok || transport == nil {
		return nil, errors.New("default HTTP transport is unavailable")
	}
	clone := transport.Clone()
	clone.Proxy = nil
	client := &http.Client{
		Transport: clone,
	}
	s.anthropicStableCanary.mu.Lock()
	if existing := s.anthropicStableCanary.clients[key]; existing != nil {
		clone.CloseIdleConnections()
		client = existing
	} else {
		s.anthropicStableCanary.clients[key] = client
	}
	s.anthropicStableCanary.mu.Unlock()
	return client, nil
}

func stableCanaryReadBody(resp *http.Response, maxBytes int64) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("stable canary response body is empty")
	}
	if maxBytes <= 0 {
		maxBytes = 64 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("stable canary response exceeds %d bytes", maxBytes)
	}
	return body, nil
}

// stableCanaryResponseWriter delays committing the upstream status until the
// first downstream write attempt. This keeps a pre-output
// upstream disconnect representable as a local gateway error instead of an
// empty, already-committed 2xx response.
type stableCanaryResponseWriter struct {
	ctx       *gin.Context
	status    int
	committed bool
}

func (w *stableCanaryResponseWriter) Write(p []byte) (int, error) {
	if w == nil || w.ctx == nil || w.ctx.Writer == nil {
		return 0, errors.New("stable canary response writer is incomplete")
	}
	if !w.committed {
		w.ctx.Writer.WriteHeader(w.status)
		MarkResponseCommitted(w.ctx)
		w.committed = true
	}
	return w.ctx.Writer.Write(p)
}

func (w *stableCanaryResponseWriter) CommitEmpty() {
	if w == nil || w.ctx == nil || w.ctx.Writer == nil || w.committed {
		return
	}
	w.ctx.Writer.WriteHeader(w.status)
	// Gin's ResponseWriter keeps WriteHeader deferred until WriteHeaderNow (or
	// Write). Empty 204/terminal responses have no body to trigger that flush;
	// explicitly commit the selected upstream status instead of silently leaving
	// the recorder/server at its default 200.
	w.ctx.Writer.WriteHeaderNow()
	MarkResponseCommitted(w.ctx)
	w.committed = true
}

// anthropicStableCanaryRefreshResponse is deliberately local to the strict
// canary. The normal Claude OAuth client uses a compatibility HTTP stack
// (proxy/impersonation/axios headers); reusing it here would make a 401 replay
// observably different from the reference claude-gateway refresh request.
type anthropicStableCanaryRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// refreshAnthropicStableCanaryToken performs the only credential mutation that
// stable mode permits. The per-account runtime slot and cross-process lease are
// held by the caller, so D1 and shared D2 cannot refresh the reserved account
// concurrently.
func (s *GatewayService) refreshAnthropicStableCanaryToken(
	ctx context.Context,
	account *Account,
	client *http.Client,
) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if account == nil || client == nil {
		return "", errors.New("stable canary refresh is not configured")
	}
	if s == nil || s.accountRepo == nil {
		return "", errors.New("stable canary refresh persistence is not configured")
	}
	// Refresh is an account operation, not a lease owned by the triggering
	// client. Detach cancellation while retaining context values, then impose a
	// bounded operation deadline. The caller decides separately whether its own
	// replay should still be attempted.
	refreshBase := context.WithoutCancel(ctx)
	refreshCtx, cancel := context.WithTimeout(refreshBase, anthropicStableCanaryRefreshTimeout)
	defer cancel()
	ctx = refreshCtx
	if err := enforceAnthropicStableCanaryOutbound(ctx, account, anthropicStableCanaryRefreshOperation); err != nil {
		return "", err
	}
	refreshToken := account.GetCredential("refresh_token")
	if err := validateAnthropicStableOAuthRefreshToken(refreshToken); err != nil {
		return "", anthropicStableCanaryRefreshFailure(anthropicStableCanaryRefreshFailureCredentialRejected, 0, err)
	}
	request, err := BuildAnthropicStableRefreshRequest(ctx, refreshToken)
	if err != nil {
		return "", anthropicStableCanaryRefreshFailure(anthropicStableCanaryRefreshFailureCredentialRejected, 0, err)
	}
	var writeObserved atomic.Bool
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		WroteHeaders: func() { writeObserved.Store(true) },
		WroteRequest: func(httptrace.WroteRequestInfo) { writeObserved.Store(true) },
	}))
	response, err := roundTripAnthropicStableCanary(client, request)
	if err != nil {
		class := anthropicStableCanaryRefreshFailureTransient
		if writeObserved.Load() {
			// Once token request bytes may have crossed the socket, the rotating
			// refresh family is ambiguous and must stay fenced until operator
			// recovery; replaying the old refresh token could fork the family.
			class = anthropicStableCanaryRefreshFailureCredentialAmbiguous
		}
		return "", anthropicStableCanaryRefreshFailure(class, 0,
			fmt.Errorf("stable canary token refresh request failed: %w", err))
	}
	if response == nil {
		return "", anthropicStableCanaryRefreshFailure(anthropicStableCanaryRefreshFailureTransient, 0,
			errors.New("stable canary token refresh returned an empty response"))
	}
	if response.Body == nil {
		return "", anthropicStableCanaryRefreshFailure(anthropicStableCanaryRefreshFailureCredentialAmbiguous, response.StatusCode,
			errors.New("stable canary token refresh returned an empty body"))
	}
	defer response.Body.Close()
	body, err := stableCanaryReadBody(response, 1<<20)
	if err != nil {
		return "", anthropicStableCanaryRefreshFailure(anthropicStableCanaryRefreshFailureCredentialAmbiguous, response.StatusCode,
			fmt.Errorf("read stable canary token refresh response: %w", err))
	}
	if response.StatusCode != http.StatusOK {
		class := anthropicStableCanaryRefreshFailureTransient
		if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			class = anthropicStableCanaryRefreshFailureCredentialRejected
		} else if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			// The reference endpoint accepts exactly 200. Another 2xx may still
			// have rotated the refresh family, so treating it as retryable would be
			// unsafe.
			class = anthropicStableCanaryRefreshFailureCredentialAmbiguous
		}
		return "", anthropicStableCanaryRefreshFailure(class, response.StatusCode, nil)
	}
	var tokenResponse anthropicStableCanaryRefreshResponse
	if err := json.Unmarshal(body, &tokenResponse); err != nil {
		return "", anthropicStableCanaryRefreshFailure(anthropicStableCanaryRefreshFailureCredentialAmbiguous, response.StatusCode,
			fmt.Errorf("decode stable canary token refresh response: %w", err))
	}
	if err := validateAnthropicStableOAuthAccessToken(tokenResponse.AccessToken); err != nil {
		return "", anthropicStableCanaryRefreshFailure(anthropicStableCanaryRefreshFailureCredentialAmbiguous, response.StatusCode,
			fmt.Errorf("stable canary token refresh returned an invalid access token: %w", err))
	}
	if strings.TrimSpace(tokenResponse.TokenType) == "" {
		tokenResponse.TokenType = "Bearer"
	}
	tokenInfo := &TokenInfo{
		AccessToken:  tokenResponse.AccessToken,
		TokenType:    tokenResponse.TokenType,
		RefreshToken: tokenResponse.RefreshToken,
		Scope:        tokenResponse.Scope,
	}
	newCredentials := BuildClaudeAccountCredentials(tokenInfo)
	// Some OAuth responses omit expiry fields. Do not replace a known-good
	// expiry with zero merely because the refresh endpoint was terse.
	if tokenResponse.ExpiresIn > 0 {
		tokenInfo.ExpiresIn = tokenResponse.ExpiresIn
		tokenInfo.ExpiresAt = time.Now().Add(time.Duration(tokenResponse.ExpiresIn) * time.Second).Unix()
		newCredentials["expires_in"] = strconv.FormatInt(tokenInfo.ExpiresIn, 10)
		newCredentials["expires_at"] = strconv.FormatInt(tokenInfo.ExpiresAt, 10)
	} else {
		delete(newCredentials, "expires_in")
		delete(newCredentials, "expires_at")
	}
	credentials := MergeCredentials(account.Credentials, newCredentials)
	persistCtx := withAnthropicStableCanaryRefreshAuthorization(ctx, account.ID)
	if err := persistAccountCredentials(persistCtx, s.accountRepo, account, credentials); err != nil {
		return "", anthropicStableCanaryRefreshFailure(anthropicStableCanaryRefreshFailureCredentialAmbiguous, 0,
			fmt.Errorf("persist stable canary token refresh: %w", err))
	}
	return tokenResponse.AccessToken, nil
}

func stableCanaryUsage(metrics AnthropicStableResponseMetrics) ClaudeUsage {
	return ClaudeUsage{
		InputTokens:              int(metrics.InputTokens),
		OutputTokens:             int(metrics.OutputTokens),
		CacheReadInputTokens:     int(metrics.CacheReadTokens),
		CacheCreationInputTokens: int(metrics.CacheCreationTokens),
	}
}

func stableCanaryFirstToken(start time.Time, at time.Time) *int {
	if at.IsZero() {
		return nil
	}
	ms := at.Sub(start).Milliseconds()
	if ms < 0 {
		ms = 0
	}
	value := int(ms)
	return &value
}

func stableCanaryFirstSemanticOutput(start time.Time, metrics AnthropicStableResponseMetrics) *int {
	return stableCanaryFirstToken(start, metrics.FirstSemanticOutputAt)
}

func (s *GatewayService) writeAnthropicStableCanaryUpstreamError(c *gin.Context, resp *http.Response, account *Account, model string) error {
	return s.writeAnthropicStableUpstreamError(c, resp, account, model, "anthropic_stable_canary_http_error", "stable canary")
}

func (s *GatewayService) writeAnthropicStableUpstreamError(
	c *gin.Context,
	resp *http.Response,
	account *Account,
	model string,
	kind string,
	label string,
) error {
	if resp == nil {
		return fmt.Errorf("%s upstream response is empty", label)
	}
	body, readErr := stableCanaryReadBody(resp, 64<<20)
	if readErr != nil {
		return fmt.Errorf("read %s upstream error: %w", label, readErr)
	}
	if c != nil {
		writeAnthropicPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
		if contentType == "" {
			contentType = "application/json"
		}
		c.Data(resp.StatusCode, contentType, body)
		MarkResponseCommitted(c)
	}
	message := strings.TrimSpace(sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(body)))
	setOpsUpstreamError(c, resp.StatusCode, message, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Passthrough:        true,
		Kind:               kind,
		Message:            message,
	})
	return fmt.Errorf("%s upstream status %d", label, resp.StatusCode)
}

// ForwardAnthropicStableCanaryRaw is the handler-facing strict entry. It never
// accepts a ParsedRequest because constructing one would already permit the
// generic gateway to normalize the body before this profile is checked.
func (s *GatewayService) ForwardAnthropicStableCanaryRaw(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	rawBody []byte,
	ownerUserID int64,
	startTime time.Time,
) (*ForwardResult, error) {
	if c == nil || c.Request == nil || c.Writer == nil {
		return nil, errors.New("stable canary requires an HTTP handler context")
	}
	if account == nil {
		return nil, errors.New("stable canary account is nil")
	}
	if startTime.IsZero() {
		startTime = time.Now()
	}
	canary, err := s.anthropicStableCanaryConfig()
	if err != nil {
		return nil, err
	}
	if account.ID != canary.AccountID {
		return nil, s.stableCanaryReject(c, http.StatusServiceUnavailable, errAnthropicStableCanaryAccountInvalid)
	}
	if int64(len(rawBody)) > canary.MaxBodyBytes {
		return nil, s.stableCanaryReject(c, http.StatusRequestEntityTooLarge, ErrAnthropicStableIngressMalformed)
	}
	header := stableCanaryIngressHeaders(c)
	ingress, err := parseAnthropicStableCanaryIngress(c, rawBody)
	if err != nil {
		return nil, s.stableCanaryReject(c, http.StatusBadRequest, err)
	}
	if err := s.validateAnthropicStableCanaryModelAccess(ctx, canary.GroupID, ingress.Model); err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, errAnthropicStableCanaryModelRestricted) {
			status = http.StatusBadRequest
		}
		return nil, s.stableCanaryReject(c, status, err)
	}
	if reason := s.anthropicStableCanary.blockReason(account.ID); reason != "" {
		return nil, s.stableCanaryReject(c, http.StatusServiceUnavailable, fmt.Errorf("stable canary is paused: %s", reason))
	}
	releaseSlot, err := s.anthropicStableCanary.acquire(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	defer releaseSlot()
	leaseRepo, ok := s.accountRepo.(AnthropicStableCanaryLeaseRepository)
	if !ok {
		return nil, s.stableCanaryReject(c, http.StatusServiceUnavailable,
			errors.New("stable canary cross-process lease is unavailable"))
	}
	releaseLease, err := leaseRepo.AcquireAnthropicStableCanaryLease(ctx, account.ID)
	if err != nil || releaseLease == nil {
		if err == nil {
			err = errors.New("stable canary cross-process lease is incomplete")
		}
		return nil, s.stableCanaryReject(c, http.StatusServiceUnavailable, err)
	}
	defer func() {
		if releaseErr := releaseLease(); releaseErr != nil {
			logger.LegacyPrintf("service.gateway", "[Anthropic Stable Canary] release lease failed account=%d err=%v", account.ID, releaseErr)
		}
	}()
	// A queued request may have observed the account before the preceding
	// request proved its credentials unusable. Recheck under the account slot so
	// it cannot start a second refresh/replay cycle with the rejected epoch.
	if reason := s.anthropicStableCanary.blockReason(account.ID); reason != "" {
		return nil, s.stableCanaryReject(c, http.StatusServiceUnavailable, fmt.Errorf("stable canary is paused: %s", reason))
	}
	if s.accountRepo == nil {
		return nil, s.stableCanaryReject(c, http.StatusServiceUnavailable, errAnthropicStableCanaryAccountInvalid)
	}
	// The request may have waited behind a preceding 401 refresh. Reload under
	// the per-account slot so it cannot send a token snapshot that became stale
	// while queued, then revalidate every isolation field before any egress.
	freshAccount, loadErr := s.accountRepo.GetByID(ctx, canary.AccountID)
	if loadErr != nil || freshAccount == nil {
		if loadErr == nil {
			loadErr = errAnthropicStableCanaryAccountInvalid
		}
		return nil, s.stableCanaryReject(c, http.StatusServiceUnavailable, loadErr)
	}
	if err := s.validateAnthropicStableCanaryAccount(ctx, canary.GroupID, freshAccount); err != nil {
		return nil, s.stableCanaryReject(c, http.StatusServiceUnavailable, err)
	}
	account = freshAccount
	if !AnthropicStableIngressProfilesEquivalent(ingress.ProfileID, account.AnthropicStableCanaryProfileID()) {
		return nil, s.stableCanaryReject(c, http.StatusServiceUnavailable, errAnthropicStableCanaryAccountInvalid)
	}
	upstreamBody := rawBody
	if canary.SharedUsers {
		upstreamBody, err = ingress.PatchDevice(account.AnthropicStableCanaryDeviceID())
		if err != nil {
			return nil, s.stableCanaryReject(c, http.StatusBadRequest, err)
		}
	} else if ingress.InboundDevice != account.AnthropicStableCanaryDeviceID() {
		// D1 remains a same-installation canary and does not patch body bytes.
		return nil, s.stableCanaryReject(c, http.StatusBadRequest, ErrAnthropicStableIngressDevicePatch)
	}
	claimCtx, claimCancel := context.WithTimeout(ctx, anthropicStableCanarySessionClaimTimeout)
	defer claimCancel()
	if err := s.ClaimAnthropicStableCanarySession(claimCtx, canary.GroupID, ownerUserID, ingress.SessionID); err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, ErrAnthropicStableCanarySessionOwnerConflict) {
			status = http.StatusConflict
		}
		return nil, s.stableCanaryReject(c, status, err)
	}
	setOpsAnthropicRequestShape(c, ingress.Stream, ingress.Stream, "stable_canary", "")
	if c != nil {
		mode := anthropicStableCanaryModeName
		if canary.SharedUsers {
			mode = anthropicStableCanarySharedModeName
			c.Set("anthropic_stable_session_generation", canary.SessionGeneration)
		}
		c.Set("anthropic_passthrough_mode", mode)
		c.Set("anthropic_passthrough_fallback", "")
	}
	if c != nil {
		loggerFromStableCanary(c, account, ingress)
	}
	authorizedCtx := withAnthropicStableCanaryMessageAuthorization(ctx, account.ID)
	if err := enforceAnthropicStableCanaryOutbound(authorizedCtx, account, anthropicStableCanaryMessagesOperation); err != nil {
		return nil, err
	}
	token := account.GetCredential("access_token")
	if err := validateAnthropicStableOAuthAccessToken(token); err != nil {
		return nil, err
	}
	client, err := s.anthropicStableCanaryHTTPClient(account)
	if err != nil {
		return nil, err
	}
	var resp *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		request, buildErr := BuildAnthropicStableMessageRequest(ctx, AnthropicStableMessagesOriginV1, header, upstreamBody, token)
		if buildErr != nil {
			return nil, buildErr
		}
		resp, err = roundTripAnthropicStableCanary(client, request)
		if err != nil {
			return nil, fmt.Errorf("stable canary upstream request failed: %w", err)
		}
		if resp == nil {
			return nil, errors.New("stable canary upstream returned an empty response")
		}
		if resp.StatusCode != http.StatusUnauthorized || attempt != 0 {
			break
		}
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		refreshCtx := withAnthropicStableCanaryRefreshAuthorization(ctx, account.ID)
		refreshed, refreshErr := s.refreshAnthropicStableCanaryToken(refreshCtx, account, client)
		if refreshErr != nil {
			// Only cancellation of the outer client request is benign credential
			// evidence. The detached refresh has its own deadline; if that expires
			// after request bytes crossed the socket, its rotating refresh family is
			// ambiguous and must follow the failure classification below.
			failureClass := anthropicStableCanaryRefreshFailureClassOf(refreshErr)
			if failureClass != anthropicStableCanaryRefreshFailureTransient {
				s.blockAnthropicStableCanary(ctx, account.ID, anthropicStableCanaryBlockReasonRefreshFailed)
			}
			if cause := context.Cause(ctx); cause != nil {
				return nil, cause
			}
			if failureClass == anthropicStableCanaryRefreshFailureTransient {
				return nil, s.stableCanaryReject(c, http.StatusServiceUnavailable, errors.New("stable canary token refresh is temporarily unavailable"))
			}
			return nil, s.stableCanaryReject(c, http.StatusServiceUnavailable, errors.New("stable canary credential requires recovery"))
		}
		if cause := context.Cause(ctx); cause != nil {
			// The account-owned refresh completed safely, but a caller that
			// disconnected must not start its own second message attempt.
			return nil, cause
		}
		token = refreshed
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("stable canary upstream returned an empty response")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		s.blockAnthropicStableCanary(ctx, account.ID, anthropicStableCanaryBlockReasonCredentialRejected)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, s.writeAnthropicStableCanaryUpstreamError(c, resp, account, ingress.Model)
	}
	var downstream *stableCanaryResponseWriter
	if c != nil {
		writeAnthropicPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
		// These are gateway delivery controls, not upstream identity changes.
		// Without them a reverse proxy may buffer the otherwise byte-for-byte SSE
		// body and turn a valid upstream first token into a delayed client event.
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		downstream = &stableCanaryResponseWriter{ctx: c, status: resp.StatusCode}
	}
	result := &ForwardResult{RequestID: resp.Header.Get("x-request-id"), Model: ingress.Model, Stream: ingress.Stream, UpstreamModel: ingress.Model, Duration: time.Since(startTime)}
	// ParseAnthropicStableIngressProfile has already required stream=true. Keep
	// the D1 response path singular: a non-streaming request must fail before any
	// upstream connection rather than introducing a second response/accounting
	// identity into the canary cohort.
	observer := NewAnthropicStableSSEObserver(time.Now)
	flushed := false
	flush := func() error {
		if c == nil || downstream == nil || !downstream.committed {
			return nil
		}
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
			flushed = true
		}
		return nil
	}
	metrics, copyErr := CopyAnthropicStableResponse(ctx, downstream, resp.Body, true, flush, observer)
	if !flushed {
		metrics.FirstDownstreamFlushAt = time.Time{}
	}
	result.Usage = stableCanaryUsage(metrics)
	result.RequestID = firstNonEmptyStableCanary(result.RequestID, metrics.UpstreamRequestID)
	result.ResponseID = metrics.UpstreamRequestID
	result.FirstTokenMs = stableCanaryFirstSemanticOutput(startTime, metrics)
	result.Duration = time.Since(startTime)
	if copyErr != nil {
		result.ClientDisconnect = context.Cause(ctx) != nil || metrics.DownstreamError
		if c != nil && metrics.ErrorEventSeen {
			MarkGatewaySSEErrorWritten(c)
		}
		if c != nil && !result.ClientDisconnect {
			reason := anthropicStableCanaryStreamErrorReason(copyErr)
			setOpsUpstreamError(c, resp.StatusCode, reason, "")
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  result.RequestID,
				Passthrough:        true,
				Kind:               "anthropic_stable_canary_stream_error",
				Stage:              "response_stream",
				Reason:             reason,
				RequestedModel:     ingress.Model,
				MappedModel:        ingress.Model,
				Message:            reason,
			})
		}
		return result, copyErr
	}
	downstream.CommitEmpty()
	return result, nil
}

func anthropicStableCanaryStreamErrorReason(err error) string {
	switch {
	case errors.Is(err, ErrAnthropicStableResponseTruncated):
		return "truncated_before_terminal"
	case errors.Is(err, ErrAnthropicStableResponseInvalidTerminal):
		return "invalid_terminal"
	case errors.Is(err, ErrAnthropicStableResponseObservationIncomplete):
		return "observer_incomplete"
	case errors.Is(err, ErrAnthropicStableResponseErrorEvent):
		return "upstream_error_event"
	default:
		return "stream_io_error"
	}
}

func firstNonEmptyStableCanary(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// HashAnthropicStableCanarySessionID returns an observability-only pseudonym.
// The raw client session UUID must not be written to stable-canary logs or
// usage rows; future D3 ownership uses a separately keyed HMAC contract.
func HashAnthropicStableCanarySessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("anthropic-stable-canary/session/v1\x00" + sessionID))
	return hex.EncodeToString(sum[:])
}

// loggerFromStableCanary records only bounded identity metadata. In
// particular, it never logs the fixed device id, access token or request body.
func loggerFromStableCanary(c *gin.Context, account *Account, ingress *AnthropicStableIngressRequest) {
	if account == nil || ingress == nil {
		return
	}
	sessionHash := HashAnthropicStableCanarySessionID(ingress.SessionID)
	mode := anthropicStableCanaryModeName
	generation := int64(0)
	if c != nil {
		if value, ok := c.Get("anthropic_passthrough_mode"); ok {
			if text, ok := value.(string); ok && text != "" {
				mode = text
			}
		}
		if value, ok := c.Get("anthropic_stable_session_generation"); ok {
			generation, _ = value.(int64)
		}
	}
	logger.LegacyPrintf("service.gateway", "[Anthropic Stable Canary] account=%d model=%s stream=%v session_hash=%s generation=%d anthropic_passthrough_mode=%s",
		account.ID, ingress.Model, ingress.Stream, shortSessionHash(sessionHash), generation, mode)
}
