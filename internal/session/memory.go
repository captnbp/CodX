package session

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is an in-process Store implementation for testing and local
// development. It satisfies the same interface as RedisStore.
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	ttls     map[string]time.Time
}

// NewMemoryStore creates a new MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[string]*Session),
		ttls:     make(map[string]time.Time),
	}
}

// Save stores a copy of the session in memory.
func (s *MemoryStore) Save(ctx context.Context, sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.ID] = sess
	return nil
}

// Get retrieves a session by ID.
func (s *MemoryStore) Get(ctx context.Context, id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return sess, nil
}

// Delete removes a session by ID.
func (s *MemoryStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	delete(s.ttls, id)
	return nil
}

// Close is a no-op for MemoryStore.
func (s *MemoryStore) Close() error {
	return nil
}
