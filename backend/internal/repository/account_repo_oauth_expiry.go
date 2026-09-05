package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.OAuth401ExpiryRepository = (*accountRepository)(nil)

func (r *accountRepository) ExpireOAuthCredentialsIfUnchanged(ctx context.Context, expected *service.Account, expiredAt time.Time) (bool, error) {
	if r == nil || r.sql == nil {
		return false, errors.New("OAuth expiry repository is not configured")
	}
	if expected == nil || expected.ID <= 0 || expected.Type != service.AccountTypeOAuth || expected.IsCredentialShadow() {
		return false, nil
	}
	payload, err := json.Marshal(normalizeJSONMap(expected.Credentials))
	if err != nil {
		return false, err
	}
	managedPredicate := ""
	if !service.AnthropicStableCanaryRefreshAuthorized(ctx, expected.ID) {
		managedPredicate = " AND NOT (" + anthropicStableCanaryManagedSQL + ")" +
			" AND NOT (" + anthropicStableIdentityManagedSQL + ")"
	}
	result, err := r.sql.ExecContext(ctx, `
		WITH updated AS (
			UPDATE accounts AS a
			SET credentials = jsonb_set(a.credentials, '{expires_at}', to_jsonb($1::text), true),
				updated_at = NOW()
			WHERE a.id = $2 AND a.deleted_at IS NULL
				AND a.platform = $3 AND a.type = $4 AND a.parent_account_id IS NULL
				AND a.credentials = $5::jsonb
				AND a.proxy_id IS NOT DISTINCT FROM $6`+managedPredicate+`
			RETURNING a.id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $7, updated.id, NULL, NULL FROM updated
	`, expiredAt.UTC().Format(time.RFC3339Nano), expected.ID, expected.Platform,
		service.AccountTypeOAuth, string(payload), expected.ProxyID, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return false, err
	}
	r.syncSchedulerAccountSnapshotDetached(ctx, expected.ID)
	return true, nil
}
