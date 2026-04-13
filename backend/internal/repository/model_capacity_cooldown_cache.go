package repository

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const modelCapacityCooldownPrefix = "model_capacity_cooldown:account:"

var modelCapacityCooldownSetScript = redis.NewScript(`
	local key = KEYS[1]
	local new_until = tonumber(ARGV[1])
	local new_ttl = tonumber(ARGV[2])

	local existing = redis.call('GET', key)
	if existing then
		local existing_until = tonumber(existing)
		if existing_until and new_until <= existing_until then
			return 0
		end
	end

	redis.call('SET', key, tostring(new_until), 'EX', new_ttl)
	return 1
`)

type modelCapacityCooldownCache struct {
	rdb *redis.Client
}

func NewModelCapacityCooldownCache(rdb *redis.Client) service.ModelCapacityCooldownCache {
	return &modelCapacityCooldownCache{rdb: rdb}
}

func (c *modelCapacityCooldownCache) SetModelCapacityCooldown(ctx context.Context, accountID int64, modelKey string, until time.Time) error {
	key := modelCapacityCooldownKey(accountID, modelKey)
	if key == "" {
		return nil
	}
	ttl := time.Until(until)
	if ttl <= 0 {
		return nil
	}
	ttlSeconds := int(ttl.Seconds())
	if ttlSeconds < 1 {
		ttlSeconds = 1
	}
	_, err := modelCapacityCooldownSetScript.Run(ctx, c.rdb, []string{key}, until.Unix(), ttlSeconds).Result()
	return err
}

func (c *modelCapacityCooldownCache) DeleteModelCapacityCooldown(ctx context.Context, accountID int64, modelKey string) error {
	key := modelCapacityCooldownKey(accountID, modelKey)
	if key == "" {
		return nil
	}
	return c.rdb.Del(ctx, key).Err()
}

func (c *modelCapacityCooldownCache) BatchGetModelCapacityCooldownRemaining(ctx context.Context, lookups []service.ModelCapacityCooldownLookup) (map[service.ModelCapacityCooldownLookup]time.Duration, error) {
	if len(lookups) == 0 {
		return map[service.ModelCapacityCooldownLookup]time.Duration{}, nil
	}

	keys := make([]string, 0, len(lookups))
	keyToLookup := make(map[string]service.ModelCapacityCooldownLookup, len(lookups))
	for _, lookup := range lookups {
		key := modelCapacityCooldownKey(lookup.AccountID, lookup.ModelKey)
		if key == "" {
			continue
		}
		keys = append(keys, key)
		keyToLookup[key] = lookup
	}
	if len(keys) == 0 {
		return map[service.ModelCapacityCooldownLookup]time.Duration{}, nil
	}

	values, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	result := make(map[service.ModelCapacityCooldownLookup]time.Duration, len(values))
	for idx, val := range values {
		if val == nil {
			continue
		}
		raw, ok := val.(string)
		if !ok || raw == "" {
			continue
		}
		untilUnix, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		remaining := time.Unix(untilUnix, 0).Sub(now)
		if remaining <= 0 {
			continue
		}
		key := keys[idx]
		lookup := keyToLookup[key]
		result[lookup] = remaining
	}

	return result, nil
}

func modelCapacityCooldownKey(accountID int64, modelKey string) string {
	modelKey = strings.TrimSpace(modelKey)
	if accountID <= 0 || modelKey == "" {
		return ""
	}
	encodedModelKey := base64.RawURLEncoding.EncodeToString([]byte(modelKey))
	return fmt.Sprintf("%s%d:model:%s", modelCapacityCooldownPrefix, accountID, encodedModelKey)
}
