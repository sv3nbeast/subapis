package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type AnthropicStableCanaryLifecycleAction string

const (
	AnthropicStableCanaryLifecycleInspect AnthropicStableCanaryLifecycleAction = "inspect"
	AnthropicStableCanaryLifecycleEnable  AnthropicStableCanaryLifecycleAction = "enable"
	AnthropicStableCanaryLifecycleDisable AnthropicStableCanaryLifecycleAction = "disable"
)

type AnthropicStableCanaryLifecycleInput struct {
	Action   AnthropicStableCanaryLifecycleAction
	Config   config.GatewayAnthropicStableCanaryConfig
	DeviceID string
	Profile  string
	Execute  bool
}

// AnthropicStableCanaryLifecycleResult deliberately contains no token, device
// identifier, account name, API key value, or request data.
type AnthropicStableCanaryLifecycleResult struct {
	Action                 AnthropicStableCanaryLifecycleAction `json:"action"`
	Executed               bool                                 `json:"executed"`
	Validated              bool                                 `json:"validated"`
	GroupID                int64                                `json:"group_id"`
	AccountID              int64                                `json:"account_id"`
	OwnerUserID            int64                                `json:"owner_user_id"`
	APIKeyID               int64                                `json:"api_key_id"`
	Profile                string                               `json:"profile,omitempty"`
	EnrolledBefore         bool                                 `json:"enrolled_before"`
	EnrolledAfter          bool                                 `json:"enrolled_after"`
	BlockedBefore          bool                                 `json:"blocked_before"`
	PreviousSchedulable    bool                                 `json:"previous_schedulable"`
	RestoredSchedulable    bool                                 `json:"restored_schedulable"`
	RequiresManualRecovery bool                                 `json:"requires_manual_recovery"`
}

type anthropicStableLifecycleGroup struct {
	Platform            string
	Status              string
	Exclusive           bool
	ClaudeCodeOnly      bool
	RequireOAuthOnly    bool
	FallbackGroupID     sql.NullInt64
	InvalidFallbackID   sql.NullInt64
	ModelRoutingEnabled bool
}

type anthropicStableLifecycleAPIKey struct {
	ID         int64
	UserID     int64
	GroupID    sql.NullInt64
	Status     string
	ExpiresAt  sql.NullTime
	LastUsedAt sql.NullTime
	UserStatus string
}

func OpenAnthropicStableCanaryLifecycleDB(cfg *config.Config) (*sql.DB, error) {
	if cfg == nil {
		return nil, errors.New("stable canary lifecycle config is nil")
	}
	db, err := sql.Open("postgres", cfg.Database.DSNWithTimezone(cfg.Timezone))
	if err != nil {
		return nil, err
	}
	applyDBPoolSettings(db, cfg)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func RunAnthropicStableCanaryLifecycle(
	ctx context.Context,
	db *sql.DB,
	input AnthropicStableCanaryLifecycleInput,
) (*AnthropicStableCanaryLifecycleResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return nil, errors.New("stable canary lifecycle database is nil")
	}
	input.Action = AnthropicStableCanaryLifecycleAction(strings.ToLower(strings.TrimSpace(string(input.Action))))
	input.Profile = strings.TrimSpace(input.Profile)
	input.DeviceID = strings.TrimSpace(input.DeviceID)
	if input.DeviceID == "" {
		input.DeviceID = strings.TrimSpace(os.Getenv("ANTHROPIC_STABLE_CANARY_DEVICE_ID"))
	}
	switch input.Action {
	case AnthropicStableCanaryLifecycleInspect, AnthropicStableCanaryLifecycleEnable, AnthropicStableCanaryLifecycleDisable:
	default:
		return nil, fmt.Errorf("unsupported stable canary lifecycle action %q", input.Action)
	}
	canary := input.Config
	if canary.GroupID <= 0 || canary.AccountID <= 0 || canary.OwnerUserID <= 0 || canary.APIKeyID <= 0 {
		return nil, errors.New("stable canary group/account/owner/api-key ids must be configured")
	}
	if input.Action == AnthropicStableCanaryLifecycleEnable {
		if canary.Enabled {
			return nil, errors.New("disable gateway.anthropic_stable_canary.enabled before enrollment")
		}
		if !service.IsValidAnthropicStableDeviceID(input.DeviceID) || !service.IsKnownAnthropicStableCanaryProfile(input.Profile) {
			return nil, errors.New("stable canary device/profile is not a reviewed capture")
		}
	}
	if input.Action == AnthropicStableCanaryLifecycleDisable && canary.Enabled {
		return nil, errors.New("disable gateway.anthropic_stable_canary.enabled before retirement")
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin stable canary lifecycle transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	advisoryRows, err := tx.QueryContext(ctx, "SELECT pg_advisory_xact_lock($1)", anthropicStableCanaryAdvisoryLockID(canary.AccountID))
	if err != nil {
		return nil, fmt.Errorf("acquire stable canary lifecycle lock: %w", err)
	}
	if !advisoryRows.Next() {
		rowErr := advisoryRows.Err()
		_ = advisoryRows.Close()
		if rowErr == nil {
			rowErr = errors.New("stable canary lifecycle lock returned no row")
		}
		return nil, rowErr
	}
	if err := advisoryRows.Close(); err != nil {
		return nil, fmt.Errorf("finish stable canary lifecycle lock query: %w", err)
	}

	group, err := loadAnthropicStableLifecycleGroup(ctx, tx, canary.GroupID)
	groupValid := err == nil && validateAnthropicStableLifecycleGroup(group) == nil
	if input.Action != AnthropicStableCanaryLifecycleDisable {
		if err != nil {
			return nil, err
		}
		if !groupValid {
			return nil, errors.New("stable canary group isolation is invalid")
		}
	} else if err != nil && !errors.Is(err, service.ErrGroupNotFound) {
		// Retirement is the emergency escape hatch. A deleted or drifted group
		// must prevent automatic rescheduling, but must not strand the account's
		// managed reservation markers forever. Only unexpected database failures
		// are allowed to stop cleanup.
		return nil, err
	}
	account, err := loadAnthropicStableLifecycleAccount(ctx, tx, canary.AccountID)
	if err != nil {
		return nil, err
	}
	groupAccountIDs, err := loadAnthropicStableLifecycleIDs(ctx, tx,
		`SELECT account_id FROM account_groups WHERE group_id = $1 ORDER BY account_id FOR UPDATE`, canary.GroupID)
	if err != nil {
		return nil, fmt.Errorf("lock stable canary group memberships: %w", err)
	}
	accountGroupIDs, err := loadAnthropicStableLifecycleIDs(ctx, tx,
		`SELECT group_id FROM account_groups WHERE account_id = $1 ORDER BY group_id FOR UPDATE`, canary.AccountID)
	if err != nil {
		return nil, fmt.Errorf("lock stable canary account memberships: %w", err)
	}
	exactBinding := len(groupAccountIDs) == 1 && groupAccountIDs[0] == canary.AccountID &&
		len(accountGroupIDs) == 1 && accountGroupIDs[0] == canary.GroupID
	if input.Action != AnthropicStableCanaryLifecycleDisable && !exactBinding {
		return nil, errors.New("stable canary requires one exact account/group binding")
	}
	account.GroupIDs = append([]int64(nil), accountGroupIDs...)
	previousSchedulable, previousCaptured := account.AnthropicStableCanaryPreviousSchedulable()
	enrolled := account.IsAnthropicStableCanaryEnabled() && account.IsAnthropicStableCanaryReserved() && previousCaptured
	apiKeys, err := loadAnthropicStableLifecycleAPIKeys(ctx, tx, canary.GroupID)
	if err != nil {
		return nil, err
	}
	apiKeyValidationErr := validateAnthropicStableLifecycleAPIKey(
		apiKeys,
		canary,
		input.Action == AnthropicStableCanaryLifecycleEnable,
		input.Action == AnthropicStableCanaryLifecycleEnable && !enrolled,
	)
	if input.Action != AnthropicStableCanaryLifecycleDisable && apiKeyValidationErr != nil {
		return nil, apiKeyValidationErr
	}

	result := &AnthropicStableCanaryLifecycleResult{
		Action: input.Action, GroupID: canary.GroupID, AccountID: canary.AccountID,
		OwnerUserID: canary.OwnerUserID, APIKeyID: canary.APIKeyID,
		Profile: account.AnthropicStableCanaryProfileID(), EnrolledBefore: enrolled,
		EnrolledAfter: enrolled, BlockedBefore: account.IsAnthropicStableCanaryBlocked(),
		PreviousSchedulable: previousSchedulable,
	}

	switch input.Action {
	case AnthropicStableCanaryLifecycleInspect:
		if account.HasAnthropicStableCanaryManagedFields() && !enrolled {
			return nil, errors.New("stable canary account contains a partial or invalid lifecycle marker set")
		}
		if enrolled {
			if err := validateAnthropicStableLifecycleEnrollment(account, canary, "", "", true); err != nil {
				return nil, err
			}
		}
	case AnthropicStableCanaryLifecycleEnable:
		result.Profile = input.Profile
		if enrolled {
			if err := validateAnthropicStableLifecycleEnrollment(account, canary, input.DeviceID, input.Profile, true); err != nil {
				return nil, err
			}
		} else {
			if account.HasAnthropicStableCanaryManagedFields() {
				return nil, errors.New("stable canary account contains unmanaged lifecycle fields")
			}
			account.GroupIDs = []int64{canary.GroupID}
			if err := service.ValidateAnthropicStableCanaryEnrollmentAccount(account, input.DeviceID, input.Profile); err != nil {
				return nil, fmt.Errorf("stable canary account is not eligible: %w", err)
			}
			result.PreviousSchedulable = account.Schedulable
			if input.Execute {
				if err := enableAnthropicStableLifecycle(ctx, tx, account, input.DeviceID, input.Profile, canary.GroupID); err != nil {
					return nil, err
				}
			}
			result.EnrolledAfter = true
		}
	case AnthropicStableCanaryLifecycleDisable:
		if !account.HasAnthropicStableCanaryManagedFields() {
			return nil, errors.New("stable canary account is not enrolled")
		}
		markerValid := validateAnthropicStableLifecycleEnrollment(account, canary, "", "", false) == nil
		identityValid := markerValid && service.ValidateAnthropicStableCanaryEnrolledAccount(account, canary.GroupID) == nil
		restore := previousSchedulable && markerValid && identityValid && groupValid && exactBinding &&
			apiKeyValidationErr == nil && account.Status == service.StatusActive && !result.BlockedBefore &&
			anthropicStableLifecycleRuntimeAvailable(account)
		result.RestoredSchedulable = restore
		result.RequiresManualRecovery = (!markerValid || previousSchedulable) && !restore
		if input.Execute {
			if err := disableAnthropicStableLifecycle(ctx, tx, account.ID, restore, canary.GroupID); err != nil {
				return nil, err
			}
		}
		result.EnrolledAfter = false
	}

	result.Validated = true
	if input.Execute && input.Action != AnthropicStableCanaryLifecycleInspect {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit stable canary lifecycle: %w", err)
		}
		result.Executed = true
	}
	return result, nil
}

func loadAnthropicStableLifecycleGroup(ctx context.Context, tx *sql.Tx, id int64) (*anthropicStableLifecycleGroup, error) {
	row := &anthropicStableLifecycleGroup{}
	err := tx.QueryRowContext(ctx, `
		SELECT platform, status, is_exclusive, claude_code_only, require_oauth_only,
		       fallback_group_id, fallback_group_id_on_invalid_request, model_routing_enabled
		FROM groups WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
	`, id).Scan(&row.Platform, &row.Status, &row.Exclusive, &row.ClaudeCodeOnly, &row.RequireOAuthOnly,
		&row.FallbackGroupID, &row.InvalidFallbackID, &row.ModelRoutingEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrGroupNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock stable canary group: %w", err)
	}
	return row, nil
}

func validateAnthropicStableLifecycleGroup(group *anthropicStableLifecycleGroup) error {
	if group == nil || group.Platform != service.PlatformAnthropic || group.Status != service.StatusActive ||
		!group.Exclusive || !group.ClaudeCodeOnly || !group.RequireOAuthOnly ||
		group.FallbackGroupID.Valid || group.InvalidFallbackID.Valid || group.ModelRoutingEnabled {
		return errors.New("stable canary group isolation is invalid")
	}
	return nil
}

func loadAnthropicStableLifecycleAccount(ctx context.Context, tx *sql.Tx, id int64) (*service.Account, error) {
	account := &service.Account{}
	var credentialsRaw, extraRaw []byte
	err := tx.QueryRowContext(ctx, `
		SELECT id, platform, type, status, schedulable, concurrency,
		       proxy_id, proxy_fallback_origin_id, parent_account_id,
		       auto_pause_on_expired, expires_at, rate_limit_reset_at,
		       overload_until, temp_unschedulable_until, credentials, extra
		FROM accounts WHERE id = $1 AND deleted_at IS NULL FOR UPDATE
	`, id).Scan(&account.ID, &account.Platform, &account.Type, &account.Status, &account.Schedulable, &account.Concurrency,
		&account.ProxyID, &account.ProxyFallbackOriginID, &account.ParentAccountID,
		&account.AutoPauseOnExpired, &account.ExpiresAt, &account.RateLimitResetAt,
		&account.OverloadUntil, &account.TempUnschedulableUntil, &credentialsRaw, &extraRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock stable canary account: %w", err)
	}
	if err := json.Unmarshal(credentialsRaw, &account.Credentials); err != nil {
		return nil, fmt.Errorf("decode stable canary credentials: %w", err)
	}
	if err := json.Unmarshal(extraRaw, &account.Extra); err != nil {
		return nil, fmt.Errorf("decode stable canary extra: %w", err)
	}
	return account, nil
}

func loadAnthropicStableLifecycleIDs(ctx context.Context, tx *sql.Tx, query string, id int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var values []int64
	for rows.Next() {
		var value int64
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func loadAnthropicStableLifecycleAPIKeys(ctx context.Context, tx *sql.Tx, groupID int64) ([]anthropicStableLifecycleAPIKey, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT ak.id, ak.user_id, ak.group_id, ak.status, ak.expires_at, ak.last_used_at, u.status
		FROM api_keys ak JOIN users u ON u.id = ak.user_id AND u.deleted_at IS NULL
		WHERE ak.group_id = $1 AND ak.deleted_at IS NULL
		ORDER BY ak.id FOR UPDATE OF ak
	`, groupID)
	if err != nil {
		return nil, fmt.Errorf("lock stable canary API keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var keys []anthropicStableLifecycleAPIKey
	for rows.Next() {
		var key anthropicStableLifecycleAPIKey
		if err := rows.Scan(&key.ID, &key.UserID, &key.GroupID, &key.Status, &key.ExpiresAt, &key.LastUsedAt, &key.UserStatus); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func validateAnthropicStableLifecycleAPIKey(
	keys []anthropicStableLifecycleAPIKey,
	canary config.GatewayAnthropicStableCanaryConfig,
	requireActive bool,
	requireUnused bool,
) error {
	now := time.Now()
	active := make([]anthropicStableLifecycleAPIKey, 0, len(keys))
	var configured *anthropicStableLifecycleAPIKey
	for _, key := range keys {
		if key.ID == canary.APIKeyID {
			copy := key
			configured = &copy
		}
		if key.Status == service.StatusAPIKeyActive && (!key.ExpiresAt.Valid || key.ExpiresAt.Time.After(now)) {
			active = append(active, key)
		}
	}
	if configured == nil || configured.UserID != canary.OwnerUserID ||
		!configured.GroupID.Valid || configured.GroupID.Int64 != canary.GroupID {
		return errors.New("stable canary configured API key ownership is invalid")
	}
	if requireActive && (len(active) != 1 || active[0].ID != canary.APIKeyID || active[0].UserStatus != service.StatusActive) {
		return errors.New("stable canary requires one exact active owner API key")
	}
	if requireUnused && configured.LastUsedAt.Valid {
		return errors.New("stable canary enrollment requires a dedicated API key that has never been used")
	}
	return nil
}

func validateAnthropicStableLifecycleEnrollment(
	account *service.Account,
	canary config.GatewayAnthropicStableCanaryConfig,
	deviceID,
	profile string,
	requireIdentityValid bool,
) error {
	if account == nil {
		return errors.New("stable canary enrollment markers are invalid")
	}
	previous, captured := account.AnthropicStableCanaryPreviousSchedulable()
	if account.ID != canary.AccountID || account.Schedulable ||
		!account.IsAnthropicStableCanaryEnabled() || !account.IsAnthropicStableCanaryReserved() ||
		!captured || !previous ||
		!service.IsValidAnthropicStableDeviceID(account.AnthropicStableCanaryDeviceID()) ||
		!service.IsKnownAnthropicStableCanaryProfile(account.AnthropicStableCanaryProfileID()) {
		return errors.New("stable canary enrollment markers are invalid")
	}
	if requireIdentityValid && service.ValidateAnthropicStableCanaryEnrolledAccount(account, canary.GroupID) != nil {
		return errors.New("stable canary enrolled identity is invalid")
	}
	if deviceID != "" && account.AnthropicStableCanaryDeviceID() != deviceID {
		return errors.New("stable canary device does not match the existing enrollment")
	}
	if profile != "" && !service.AnthropicStableIngressProfilesEquivalent(account.AnthropicStableCanaryProfileID(), profile) {
		return errors.New("stable canary profile does not match the existing enrollment")
	}
	return nil
}

func anthropicStableLifecycleRuntimeAvailable(account *service.Account) bool {
	if account == nil {
		return false
	}
	now := time.Now()
	return (!account.AutoPauseOnExpired || account.ExpiresAt == nil || now.Before(*account.ExpiresAt)) &&
		(account.RateLimitResetAt == nil || !now.Before(*account.RateLimitResetAt)) &&
		(account.OverloadUntil == nil || !now.Before(*account.OverloadUntil)) &&
		(account.TempUnschedulableUntil == nil || !now.Before(*account.TempUnschedulableUntil))
}

func enableAnthropicStableLifecycle(ctx context.Context, tx *sql.Tx, account *service.Account, deviceID, profile string, groupID int64) error {
	payload, err := json.Marshal(map[string]any{
		service.AnthropicStableCanaryEnabledExtraKey:             true,
		service.AnthropicStableCanaryReservedExtraKey:            true,
		service.AnthropicStableCanaryPreviousSchedulableExtraKey: account.Schedulable,
		service.AnthropicStableCanaryDeviceIDExtraKey:            deviceID,
		service.AnthropicStableCanaryProfileExtraKey:             profile,
	})
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE accounts SET extra = COALESCE(extra, '{}'::jsonb) || $1::jsonb,
		       schedulable = FALSE, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND NOT (`+anthropicStableCanaryManagedSQL+`)
	`, string(payload), account.ID)
	if err != nil {
		return fmt.Errorf("enroll stable canary account: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return fmt.Errorf("enroll stable canary account affected %d rows: %w", affected, err)
	}
	return enqueueAnthropicStableLifecycleOutbox(ctx, tx, account.ID, groupID)
}

func disableAnthropicStableLifecycle(ctx context.Context, tx *sql.Tx, accountID int64, restore bool, groupID int64) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE accounts SET extra = COALESCE(extra, '{}'::jsonb)
		       - '`+service.AnthropicStableCanaryEnabledExtraKey+`'
		       - '`+service.AnthropicStableCanaryReservedExtraKey+`'
		       - '`+service.AnthropicStableCanaryPreviousSchedulableExtraKey+`'
		       - '`+service.AnthropicStableCanaryDeviceIDExtraKey+`'
		       - '`+service.AnthropicStableCanaryProfileExtraKey+`'
		       - '`+service.AnthropicStableCanaryBlockedExtraKey+`'
		       - '`+service.AnthropicStableCanaryBlockedReasonExtraKey+`'
		       - '`+service.AnthropicStableCanaryBlockedAtExtraKey+`',
		       schedulable = $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		  AND (`+anthropicStableCanaryManagedSQL+`)
	`, restore, accountID)
	if err != nil {
		return fmt.Errorf("retire stable canary account: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return fmt.Errorf("retire stable canary account affected %d rows: %w", affected, err)
	}
	return enqueueAnthropicStableLifecycleOutbox(ctx, tx, accountID, groupID)
}

func enqueueAnthropicStableLifecycleOutbox(ctx context.Context, tx *sql.Tx, accountID, groupID int64) error {
	payload, err := json.Marshal(map[string]any{"group_ids": []int64{groupID}})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		VALUES ($1, $2, NULL, $3::jsonb)
	`, service.SchedulerOutboxEventAccountChanged, accountID, string(payload))
	if err != nil {
		return fmt.Errorf("enqueue stable canary scheduler change: %w", err)
	}
	return nil
}
