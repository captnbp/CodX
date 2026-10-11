// ActivityStore persists the inactivity tracking context of the leader
// (workspace last-activity timestamps) in Redis/Valkey, so that a restarted
// leader resumes the inactivity tracking of the running workspaces instead of
// starting blind. When Redis/Valkey lost its data (restart without
// persistence), Load returns an empty map and the tracking restarts from
// zero.
package session

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ActivityStore is a Redis-backed inactivity.ActivityStore.
type ActivityStore struct {
	client    *redis.Client
	keyPrefix string
}

// NewRedisActivityStore creates an ActivityStore sharing the connection
// settings of the session store (same RedisOptions).
func NewRedisActivityStore(opts RedisOptions) *ActivityStore {
	ro := &redis.Options{
		Addr:     opts.Addr,
		Password: opts.Password,
		DB:       opts.DB,
	}
	if opts.TLS {
		ro.TLSConfig = buildRedisTLSConfig(opts.CAFilePath)
	}
	return &ActivityStore{
		client:    redis.NewClient(ro),
		keyPrefix: opts.KeyPrefix,
	}
}

// Save records the last activity time of one workspace.
func (s *ActivityStore) Save(ctx context.Context, slug string, ts time.Time) error {
	if slug == "" {
		return fmt.Errorf("workspace slug is required")
	}
	return s.client.Set(ctx, s.key(slug), ts.UTC().Format(time.RFC3339Nano), 0).Err()
}

// Delete forgets one workspace.
func (s *ActivityStore) Delete(ctx context.Context, slug string) error {
	return s.client.Del(ctx, s.key(slug)).Err()
}

// Load returns all known last-activity timestamps, keyed by workspace slug.
// An empty map means no known context (fresh store or lost data): the caller
// restarts the tracking from zero. Malformed entries are skipped; they are
// rewritten on the workspace's next activity.
func (s *ActivityStore) Load(ctx context.Context) (map[string]time.Time, error) {
	records := make(map[string]time.Time)

	var cursor uint64
	for {
		keys, next, err := s.client.Scan(ctx, cursor, s.key("*"), 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan activity records: %w", err)
		}
		if len(keys) > 0 {
			values, err := s.client.MGet(ctx, keys...).Result()
			if err != nil {
				return nil, fmt.Errorf("get activity records: %w", err)
			}
			for i, key := range keys {
				raw, ok := values[i].(string)
				if !ok {
					continue
				}
				ts, err := time.Parse(time.RFC3339Nano, raw)
				if err != nil {
					continue
				}
				records[s.slugOf(key)] = ts
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return records, nil
}

// Close releases the Redis client.
func (s *ActivityStore) Close() error {
	return s.client.Close()
}

// key returns the Redis key of one workspace record:
// <prefix>:inactivity:<slug>.
func (s *ActivityStore) key(slug string) string {
	if s.keyPrefix == "" {
		return "codx:inactivity:" + slug
	}
	return s.keyPrefix + ":inactivity:" + slug
}

// slugOf is the inverse of key: the workspace slug part of a Redis key.
func (s *ActivityStore) slugOf(key string) string {
	prefix := "inactivity:"
	i := indexOf(key, prefix)
	if i < 0 {
		return key
	}
	return key[i+len(prefix):]
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
