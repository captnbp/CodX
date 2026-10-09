// Package session provides a Redis-backed session store for CodX.
//
// The Store interface is defined here so that the OIDC and web packages can
// depend on the abstraction rather than the Redis implementation. This keeps
// the domain logic testable without a live Redis instance.
package session

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

// Session holds the authenticated user's identity and tokens.
type Session struct {
	// ID is the session identifier stored in the cookie.
	ID string `json:"id"`

	// Subject is the OIDC subject claim (sub).
	Subject string `json:"subject"`

	// Username is the OIDC preferred_username or the configured claim.
	Username string `json:"username"`

	// Slug is the Kubernetes-safe slug derived from Username.
	Slug string `json:"slug"`

	// Groups is the list of OIDC group memberships.
	Groups []string `json:"groups"`

	// IsAdmin is true when the user is a member of the configured admin group.
	IsAdmin bool `json:"isAdmin"`

	// AccessToken is the raw OAuth2 access token.
	AccessToken string `json:"accessToken"`

	// RefreshToken is the raw OAuth2 refresh token, if issued.
	RefreshToken string `json:"refreshToken,omitempty"`

	// IDToken is the raw OIDC ID token.
	IDToken string `json:"idToken,omitempty"`

	// ExpiresAt is when the session expires.
	ExpiresAt time.Time `json:"expiresAt"`
}

// IsExpired returns true if the session has expired.
func (s *Session) IsExpired(now time.Time) bool {
	return !s.ExpiresAt.IsZero() && now.After(s.ExpiresAt)
}

// Store is the interface for persisting and retrieving sessions.
type Store interface {
	// Save persists a session with the given TTL.
	Save(ctx context.Context, s *Session) error

	// Get retrieves a session by ID. Returns ErrNotFound if not present.
	Get(ctx context.Context, id string) (*Session, error)

	// Delete removes a session by ID.
	Delete(ctx context.Context, id string) error

	// Close releases any underlying resources.
	Close() error
}

// ErrNotFound is returned when a session ID does not exist in the store.
var ErrNotFound = errors.New("session not found")

// RedisStore implements Store using Redis/Valkey.
type RedisStore struct {
	client    *redis.Client
	keyPrefix string
	ttl       time.Duration
}

// RedisOptions configures the RedisStore.
type RedisOptions struct {
	// Addr is the Redis host:port.
	Addr string
	// Password is the optional Redis password.
	Password string
	// DB is the Redis logical database.
	DB int
	// TLS enables TLS.
	TLS bool
	// CAFilePath is the path to a PEM-encoded CA certificate file used to
	// verify the Redis TLS certificate. If set, TLS is automatically enabled
	// and the CA is loaded into the TLS config. If TLS is enabled but
	// CAFilePath is empty, the system root CAs are used.
	CAFilePath string
	// KeyPrefix is prepended to all session keys.
	KeyPrefix string
	// TTL is the default session lifetime.
	TTL time.Duration
}

// NewRedisStore creates a Redis-backed session store.
func NewRedisStore(opts RedisOptions) *RedisStore {
	ro := &redis.Options{
		Addr:     opts.Addr,
		Password: opts.Password,
		DB:       opts.DB,
	}
	if opts.TLS {
		ro.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		if opts.CAFilePath != "" {
			ro.TLSConfig = buildRedisTLSConfig(opts.CAFilePath)
		}
	}
	return &RedisStore{
		client:    redis.NewClient(ro),
		keyPrefix: opts.KeyPrefix,
		ttl:       opts.TTL,
	}
}

// buildRedisTLSConfig loads the CA certificate from the given file path and
// returns a *tls.Config that uses it for server certificate verification.
func buildRedisTLSConfig(caFilePath string) *tls.Config {
	// #nosec G304 -- caFilePath comes from the operator-controlled ConfigMap.
	caCert, err := os.ReadFile(caFilePath)
	if err != nil {
		panic(fmt.Sprintf("failed to read Redis CA certificate from %s: %v", caFilePath, err))
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCert) {
		panic(fmt.Sprintf("failed to parse Redis CA certificate from %s", caFilePath))
	}

	return &tls.Config{
		RootCAs:    caPool,
		MinVersion: tls.VersionTLS12,
	}
}

// Save stores the session in Redis as JSON.
func (s *RedisStore) Save(ctx context.Context, sess *Session) error {
	if sess.ID == "" {
		return fmt.Errorf("session ID is required")
	}
	// #nosec G117 -- the tokens are part of the session on purpose: the
	// session is the user's credential store, written to the operator's
	// Redis/Valkey instance.
	data, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	ttl := s.ttl
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	return s.client.Set(ctx, s.key(sess.ID), data, ttl).Err()
}

// Get retrieves and deserializes a session from Redis.
func (s *RedisStore) Get(ctx context.Context, id string) (*Session, error) {
	data, err := s.client.Get(ctx, s.key(id)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get session: %w", err)
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal session: %w", err)
	}
	return &sess, nil
}

// Delete removes a session from Redis.
func (s *RedisStore) Delete(ctx context.Context, id string) error {
	return s.client.Del(ctx, s.key(id)).Err()
}

// Close releases the Redis client.
func (s *RedisStore) Close() error {
	return s.client.Close()
}

func (s *RedisStore) key(id string) string {
	if s.keyPrefix == "" {
		return "codx:session:" + id
	}
	return s.keyPrefix + ":session:" + id
}
