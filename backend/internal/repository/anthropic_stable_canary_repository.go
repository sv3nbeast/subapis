package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbgroup "github.com/Wei-Shaw/sub2api/ent/group"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type anthropicStableCanaryConnProvider interface {
	Conn(context.Context) (*sql.Conn, error)
}

// discardAnthropicStableCanaryLeaseConn prevents a PostgreSQL session whose
// advisory-lock state is ambiguous from returning to database/sql's idle pool.
// sql.Conn.Close alone only releases the logical handle and may reuse the same
// physical session, including any session-level advisory lock it still owns.
func discardAnthropicStableCanaryLeaseConn(conn *sql.Conn) {
	if conn == nil {
		return
	}
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

// anthropicStableCanaryAdvisoryLockID is intentionally stable across releases
// and processes. Hashing the full int64 account id avoids truncation while the
// fixed prefix keeps this lock domain separate from unrelated advisory locks.
func anthropicStableCanaryAdvisoryLockID(accountID int64) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte("sub2api:anthropic-stable-canary:v1:"))
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(accountID))
	_, _ = hash.Write(encoded[:])
	return int64(hash.Sum64())
}

// AcquireAnthropicStableCanaryLease holds one PostgreSQL session advisory lock
// for the complete upstream request. Closing the dedicated connection releases
// the lock even after a process-side error; explicit unlock keeps normal
// completion observable and immediately reusable.
func (r *accountRepository) AcquireAnthropicStableCanaryLease(ctx context.Context, accountID int64) (func() error, error) {
	if r == nil || r.sql == nil || accountID <= 0 {
		return nil, errors.New("stable canary lease repository is not configured")
	}
	provider, ok := r.sql.(anthropicStableCanaryConnProvider)
	if !ok {
		return nil, errors.New("stable canary lease requires a PostgreSQL connection provider")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	conn, err := provider.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("open stable canary lease connection: %w", err)
	}
	lockID := anthropicStableCanaryAdvisoryLockID(accountID)
	rows, err := conn.QueryContext(ctx, "SELECT pg_advisory_lock($1)", lockID)
	if err != nil {
		discardAnthropicStableCanaryLeaseConn(conn)
		return nil, fmt.Errorf("acquire stable canary advisory lock: %w", err)
	}
	if !rows.Next() {
		rowErr := rows.Err()
		_ = rows.Close()
		discardAnthropicStableCanaryLeaseConn(conn)
		if rowErr == nil {
			rowErr = errors.New("stable canary advisory lock returned no row")
		}
		return nil, rowErr
	}
	if err := rows.Close(); err != nil {
		discardAnthropicStableCanaryLeaseConn(conn)
		return nil, fmt.Errorf("finish stable canary advisory lock query: %w", err)
	}
	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var unlocked bool
			if err := conn.QueryRowContext(releaseCtx, "SELECT pg_advisory_unlock($1)", lockID).Scan(&unlocked); err != nil {
				releaseErr = fmt.Errorf("release stable canary advisory lock: %w", err)
				discardAnthropicStableCanaryLeaseConn(conn)
				return
			} else if !unlocked {
				releaseErr = errors.New("stable canary advisory lock was not owned by the connection")
			}
			if err := conn.Close(); err != nil && releaseErr == nil {
				releaseErr = fmt.Errorf("close stable canary lease connection: %w", err)
			}
		})
		return releaseErr
	}
	return release, nil
}

var anthropicStableCanaryManagedExtraKeys = [...]string{
	service.AnthropicStableCanaryEnabledExtraKey,
	service.AnthropicStableCanaryDeviceIDExtraKey,
	service.AnthropicStableCanaryReservedExtraKey,
	service.AnthropicStableCanaryPreviousSchedulableExtraKey,
	service.AnthropicStableCanaryProfileExtraKey,
	service.AnthropicStableCanaryBlockedExtraKey,
	service.AnthropicStableCanaryBlockedReasonExtraKey,
	service.AnthropicStableCanaryBlockedAtExtraKey,
}

const anthropicStableCanaryManagedSQL = `COALESCE(extra, '{}'::jsonb) ?| ARRAY[` +
	`'` + service.AnthropicStableCanaryEnabledExtraKey + `',` +
	`'` + service.AnthropicStableCanaryDeviceIDExtraKey + `',` +
	`'` + service.AnthropicStableCanaryReservedExtraKey + `',` +
	`'` + service.AnthropicStableCanaryPreviousSchedulableExtraKey + `',` +
	`'` + service.AnthropicStableCanaryProfileExtraKey + `',` +
	`'` + service.AnthropicStableCanaryBlockedExtraKey + `',` +
	`'` + service.AnthropicStableCanaryBlockedReasonExtraKey + `',` +
	`'` + service.AnthropicStableCanaryBlockedAtExtraKey + `']`

// BlockAnthropicStableCanary is the only generic-repository mutation allowed
// for a reserved D1 account. It records a finite pause code without changing
// credentials, scheduler status, group membership, or the stored device.
func (r *accountRepository) BlockAnthropicStableCanary(ctx context.Context, accountID int64, reason string) error {
	if r == nil || r.sql == nil {
		return errors.New("account repository SQL executor is not configured")
	}
	reason = service.NormalizeAnthropicStableCanaryBlockReason(reason)
	result, err := r.sql.ExecContext(ctx, `
		UPDATE accounts
		SET extra = COALESCE(extra, '{}'::jsonb) || jsonb_build_object(
			$1::text, TRUE,
			$2::text, $3::text,
			$4::text, NOW()
		), updated_at = NOW()
		WHERE id = $5
		  AND deleted_at IS NULL
		  AND platform = $6
		  AND type = $7
		  AND COALESCE(extra, '{}'::jsonb) ->> $8::text = 'true'
		  AND COALESCE(extra, '{}'::jsonb) ->> $9::text = 'true'
	`,
		service.AnthropicStableCanaryBlockedExtraKey,
		service.AnthropicStableCanaryBlockedReasonExtraKey,
		reason,
		service.AnthropicStableCanaryBlockedAtExtraKey,
		accountID,
		service.PlatformAnthropic,
		service.AccountTypeOAuth,
		service.AnthropicStableCanaryEnabledExtraKey,
		service.AnthropicStableCanaryReservedExtraKey,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		reserved, checkErr := isAnthropicStableCanaryReserved(ctx, r.sql, accountID)
		if checkErr != nil {
			return checkErr
		}
		if reserved {
			return service.ErrAnthropicStableCanaryReserved
		}
		return service.ErrAccountNotFound
	}
	return nil
}

func isAnthropicStableCanaryReserved(ctx context.Context, exec sqlExecutor, id int64) (bool, error) {
	if exec == nil {
		return false, service.ErrAccountNotFound
	}
	rows, err := exec.QueryContext(ctx, `
		SELECT extra
		FROM accounts
		WHERE id = $1 AND deleted_at IS NULL
	`, id)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	var rawExtra []byte
	if err := rows.Scan(&rawExtra); err != nil {
		return false, err
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(rawExtra) == 0 {
		return false, nil
	}
	var extra map[string]json.RawMessage
	if err := json.Unmarshal(rawExtra, &extra); err != nil {
		return false, fmt.Errorf("decode account extra while checking stable canary reservation: %w", err)
	}
	for _, key := range anthropicStableCanaryManagedExtraKeys {
		if _, exists := extra[key]; exists {
			return true, nil
		}
	}
	return false, nil
}

func lockAnthropicStableCanaryMutationAccount(ctx context.Context, client *dbent.Client, id int64) error {
	if client == nil || id <= 0 {
		return service.ErrAccountNotFound
	}
	query := client.Account.Query().Where(dbaccount.IDEQ(id), dbaccount.DeletedAtIsNil())
	if client.Driver().Dialect() != dialect.SQLite {
		query = query.ForUpdate()
	}
	row, err := query.Only(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrAccountNotFound, nil)
	}
	if service.AnthropicStableCanaryExtraUpdateTouchesManagedFields(row.Extra) {
		return service.ErrAnthropicStableCanaryReserved
	}
	return nil
}

// lockAnthropicStableCanaryGroupMutation serializes every account-group edit
// with enrollment and rejects adding/removing members from a group containing
// a managed canary account. Callers lock groups in ascending order before the
// account row to keep the lifecycle and generic admin paths deadlock-free.
func lockAnthropicStableCanaryGroupMutation(ctx context.Context, client *dbent.Client, groupID int64) error {
	if client == nil || groupID <= 0 {
		return service.ErrGroupNotFound
	}
	groupQuery := client.Group.Query().Where(dbgroup.IDEQ(groupID), dbgroup.DeletedAtIsNil())
	if client.Driver().Dialect() != dialect.SQLite {
		groupQuery = groupQuery.ForUpdate()
	}
	if _, err := groupQuery.Only(ctx); err != nil {
		return translatePersistenceError(err, service.ErrGroupNotFound, nil)
	}
	accounts, err := client.Account.Query().
		Where(
			dbaccount.DeletedAtIsNil(),
			dbaccount.HasGroupsWith(dbgroup.IDEQ(groupID), dbgroup.DeletedAtIsNil()),
		).
		Select(dbaccount.FieldID, dbaccount.FieldExtra).
		All(ctx)
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if service.AnthropicStableCanaryExtraUpdateTouchesManagedFields(account.Extra) {
			return service.ErrAnthropicStableCanaryReserved
		}
	}
	return nil
}

func sortedUniqueAnthropicStableGroupIDs(values ...[]int64) []int64 {
	seen := make(map[int64]struct{})
	for _, ids := range values {
		for _, id := range ids {
			if id > 0 {
				seen[id] = struct{}{}
			}
		}
	}
	result := make([]int64, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func anthropicStableCanaryNotManagedPredicate() dbpredicate.Account {
	return dbpredicate.Account(func(selector *entsql.Selector) {
		column := selector.C(dbaccount.FieldExtra)
		managed := make([]*entsql.Predicate, 0, len(anthropicStableCanaryManagedExtraKeys))
		for _, key := range anthropicStableCanaryManagedExtraKeys {
			key := key
			managed = append(managed, entsql.P(func(builder *entsql.Builder) {
				switch builder.Dialect() {
				case dialect.Postgres:
					builder.WriteString(column).WriteString(" ? ").Arg(key)
				case dialect.SQLite:
					builder.WriteString("JSON_TYPE(").WriteString(column).Comma().Arg("$." + key).WriteString(") IS NOT NULL")
				default:
					// The repository officially uses PostgreSQL and SQLite. An
					// unknown dialect must fail closed rather than make a reserved
					// identity writable.
					builder.WriteString("1 = 1")
				}
			}))
		}
		selector.Where(entsql.Or(entsql.IsNull(column), entsql.Not(entsql.Or(managed...))))
	})
}
