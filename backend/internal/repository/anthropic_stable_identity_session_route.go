package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ResolveAnthropicStableIdentitySessionRoute atomically preserves the first
// selected account/device identity for a logical Claude session. Existing
// routes are touched at most once per five minutes; ON CONFLICT updates only
// last_seen_at. Candidate identity fields can never migrate an existing
// conversation when a pool is edited or rebalanced.
func (r *accountRepository) ResolveAnthropicStableIdentitySessionRoute(
	ctx context.Context,
	candidate service.AnthropicStableIdentitySessionRouteBinding,
) (*service.AnthropicStableIdentitySessionRouteBinding, error) {
	if r == nil || r.sql == nil {
		return nil, service.ErrAnthropicStableIdentitySessionRouteUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	candidate.SessionHash = strings.TrimSpace(candidate.SessionHash)
	candidate.IdentityFingerprint = strings.TrimSpace(candidate.IdentityFingerprint)
	candidate.CandidateDeviceID = strings.TrimSpace(candidate.CandidateDeviceID)
	candidate.CandidateProfileID = strings.TrimSpace(candidate.CandidateProfileID)
	if candidate.GroupID <= 0 || candidate.OwnerUserID <= 0 || candidate.AccountID <= 0 ||
		candidate.AccountGeneration <= 0 ||
		!anthropicStableCanarySessionHashPattern.MatchString(candidate.SessionHash) ||
		!anthropicStableCanarySessionHashPattern.MatchString(candidate.IdentityFingerprint) ||
		!service.IsValidAnthropicStableIdentityDeviceID(candidate.CandidateDeviceID) ||
		!service.IsKnownAnthropicStableCanaryProfile(candidate.CandidateProfileID) {
		return nil, fmt.Errorf("%w: invalid route identity", service.ErrAnthropicStableIdentitySessionRouteUnavailable)
	}

	rows, err := r.sql.QueryContext(ctx, `
			WITH existing AS (
				SELECT group_id, owner_user_id, session_hash, account_id, account_generation, identity_fingerprint
				FROM anthropic_stable_identity_session_routes
				WHERE group_id = $1
					AND session_hash = $3
			), touched AS (
				UPDATE anthropic_stable_identity_session_routes AS route
				SET last_seen_at = NOW()
				FROM existing
				WHERE route.group_id = existing.group_id
					AND route.session_hash = existing.session_hash
					AND existing.owner_user_id = $2
					AND route.last_seen_at < NOW() - INTERVAL '5 minutes'
				RETURNING route.id
			), eligible_candidate AS (
				SELECT $1::BIGINT, $2::BIGINT, $3::VARCHAR, $4::BIGINT, $5::BIGINT, $6::VARCHAR
				WHERE NOT EXISTS (SELECT 1 FROM existing)
					AND EXISTS (
						SELECT 1
						FROM accounts AS a
						JOIN account_groups AS ag
							ON ag.account_id = a.id AND ag.group_id = $1
						JOIN groups AS g ON g.id = ag.group_id
						WHERE a.id = $4
							AND a.deleted_at IS NULL
							AND a.platform = $9
							AND a.type IN ($10, $11)
							AND a.status = $12
							AND a.schedulable IS FALSE
							AND a.concurrency = 1
							AND a.extra ->> 'anthropic_stable_identity_enabled' = 'true'
							AND COALESCE(NULLIF(a.extra ->> 'anthropic_stable_identity_state', ''), 'active') = 'active'
							AND COALESCE(NULLIF(a.extra ->> 'anthropic_stable_identity_blocked', ''), 'false') = 'false'
							AND a.extra ->> 'anthropic_stable_identity_generation' = $5::TEXT
							AND a.extra ->> 'anthropic_stable_identity_device_id' = $7
							AND a.extra ->> 'anthropic_stable_identity_profile' = $8
							AND g.deleted_at IS NULL
							AND g.platform = $9
							AND g.status = $12
					)
			), inserted AS (
				INSERT INTO anthropic_stable_identity_session_routes
					(group_id, owner_user_id, session_hash, account_id, account_generation, identity_fingerprint)
				SELECT * FROM eligible_candidate
				WHERE TRUE
				ON CONFLICT (group_id, session_hash) DO UPDATE
				SET last_seen_at = CASE
					WHEN anthropic_stable_identity_session_routes.owner_user_id = EXCLUDED.owner_user_id
					THEN NOW()
					ELSE anthropic_stable_identity_session_routes.last_seen_at
				END
				RETURNING group_id, owner_user_id, session_hash, account_id, account_generation, identity_fingerprint
			)
			SELECT * FROM existing
			UNION ALL
			SELECT * FROM inserted
			LIMIT 1
		`, candidate.GroupID, candidate.OwnerUserID, candidate.SessionHash, candidate.AccountID,
		candidate.AccountGeneration, candidate.IdentityFingerprint, candidate.CandidateDeviceID,
		candidate.CandidateProfileID, service.PlatformAnthropic, service.AccountTypeOAuth,
		service.AccountTypeSetupToken, service.StatusActive)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve route: %v", service.ErrAnthropicStableIdentitySessionRouteUnavailable, err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("%w: read route: %v", service.ErrAnthropicStableIdentitySessionRouteUnavailable, err)
		}
		return nil, service.ErrAnthropicStableIdentitySessionRouteUnavailable
	}

	bound := &service.AnthropicStableIdentitySessionRouteBinding{}
	if err := rows.Scan(
		&bound.GroupID,
		&bound.OwnerUserID,
		&bound.SessionHash,
		&bound.AccountID,
		&bound.AccountGeneration,
		&bound.IdentityFingerprint,
	); err != nil {
		return nil, fmt.Errorf("%w: resolve route: %v", service.ErrAnthropicStableIdentitySessionRouteUnavailable, err)
	}
	if rows.Next() {
		return nil, fmt.Errorf("%w: duplicate route result", service.ErrAnthropicStableIdentitySessionRouteUnavailable)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: finish route: %v", service.ErrAnthropicStableIdentitySessionRouteUnavailable, err)
	}
	if bound.GroupID != candidate.GroupID || bound.SessionHash != candidate.SessionHash || bound.OwnerUserID != candidate.OwnerUserID {
		return nil, fmt.Errorf("%w: route owner mismatch", service.ErrAnthropicStableIdentitySessionRouteUnavailable)
	}
	return bound, nil
}

var _ service.AnthropicStableIdentitySessionRouteRepository = (*accountRepository)(nil)
