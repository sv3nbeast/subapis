package repository

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var anthropicStableCanarySessionHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ClaimAnthropicStableCanarySession atomically creates or verifies a durable
// owner binding. The unique (group_id, generation, session_hash) key makes two
// concurrent users race safely: only the first owner can commit, while a later
// different owner receives a deterministic conflict before upstream egress.
func (r *accountRepository) ClaimAnthropicStableCanarySession(
	ctx context.Context,
	groupID, accountID, generation, ownerUserID int64,
	sessionHash, keyFingerprint, policyFingerprint string,
) error {
	if r == nil || r.sql == nil {
		return service.ErrAnthropicStableCanarySessionBindingUnavailable
	}
	// database/sql requires a non-nil context. The production handler always
	// supplies one, but normalizing here keeps alternate callers and future
	// maintenance jobs fail-closed instead of panicking inside the repository.
	if ctx == nil {
		ctx = context.Background()
	}
	sessionHash = strings.TrimSpace(sessionHash)
	keyFingerprint = strings.TrimSpace(keyFingerprint)
	policyFingerprint = strings.TrimSpace(policyFingerprint)
	if groupID <= 0 || accountID <= 0 || generation <= 0 || ownerUserID <= 0 || !anthropicStableCanarySessionHashPattern.MatchString(sessionHash) ||
		!anthropicStableCanarySessionHashPattern.MatchString(keyFingerprint) ||
		!anthropicStableCanarySessionHashPattern.MatchString(policyFingerprint) {
		return fmt.Errorf("%w: invalid binding identity", service.ErrAnthropicStableCanarySessionBindingUnavailable)
	}
	rows, err := r.sql.QueryContext(ctx, `
		WITH generation_gate AS (
			SELECT COALESCE(MAX(generation), 0) AS highest_generation
			FROM anthropic_stable_canary_session_keys
			WHERE group_id = $1
		), key_gate AS (
			INSERT INTO anthropic_stable_canary_session_keys
				(group_id, generation, account_id, key_fingerprint, policy_fingerprint)
			SELECT $1, $3, $2, $6, $7
			FROM generation_gate
			WHERE $3 >= highest_generation
			ON CONFLICT (group_id, generation) DO UPDATE
			SET last_seen_at = CASE
				WHEN anthropic_stable_canary_session_keys.account_id = EXCLUDED.account_id
				 AND anthropic_stable_canary_session_keys.key_fingerprint = EXCLUDED.key_fingerprint
				 AND anthropic_stable_canary_session_keys.policy_fingerprint = EXCLUDED.policy_fingerprint
				THEN NOW()
				ELSE anthropic_stable_canary_session_keys.last_seen_at
			END
			RETURNING account_id, key_fingerprint, policy_fingerprint
		), claimed AS (
			INSERT INTO anthropic_stable_canary_sessions
				(group_id, generation, account_id, owner_user_id, session_hash, key_fingerprint)
			SELECT $1, $3, $2, $4, $5, $6
			FROM key_gate
			WHERE account_id = $2 AND key_fingerprint = $6 AND policy_fingerprint = $7
			ON CONFLICT (group_id, generation, session_hash) DO UPDATE
			SET last_seen_at = CASE
				WHEN anthropic_stable_canary_sessions.account_id = EXCLUDED.account_id
				 AND anthropic_stable_canary_sessions.owner_user_id = EXCLUDED.owner_user_id
				 AND anthropic_stable_canary_sessions.key_fingerprint = EXCLUDED.key_fingerprint
				THEN NOW()
				ELSE anthropic_stable_canary_sessions.last_seen_at
			END
			RETURNING account_id, owner_user_id, key_fingerprint
		)
		SELECT account_id, owner_user_id, key_fingerprint FROM claimed
	`, groupID, accountID, generation, ownerUserID, sessionHash, keyFingerprint, policyFingerprint)
	if err != nil {
		return fmt.Errorf("%w: claim session: %v", service.ErrAnthropicStableCanarySessionBindingUnavailable, err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return fmt.Errorf("%w: read claim: %v", service.ErrAnthropicStableCanarySessionBindingUnavailable, err)
		}
		return service.ErrAnthropicStableCanarySessionBindingUnavailable
	}
	var boundAccountID, boundOwnerUserID int64
	var boundKeyFingerprint string
	if err := rows.Scan(&boundAccountID, &boundOwnerUserID, &boundKeyFingerprint); err != nil {
		return fmt.Errorf("%w: scan claim: %v", service.ErrAnthropicStableCanarySessionBindingUnavailable, err)
	}
	if rows.Next() {
		return fmt.Errorf("%w: duplicate claim result", service.ErrAnthropicStableCanarySessionBindingUnavailable)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: finish claim: %v", service.ErrAnthropicStableCanarySessionBindingUnavailable, err)
	}
	if boundAccountID != accountID {
		return fmt.Errorf("%w: account mismatch", service.ErrAnthropicStableCanarySessionBindingUnavailable)
	}
	if boundKeyFingerprint != keyFingerprint {
		return fmt.Errorf("%w: routing key mismatch", service.ErrAnthropicStableCanarySessionBindingUnavailable)
	}
	if boundOwnerUserID != ownerUserID {
		return service.ErrAnthropicStableCanarySessionOwnerConflict
	}
	return nil
}

var _ service.AnthropicStableCanarySessionBindingRepository = (*accountRepository)(nil)
