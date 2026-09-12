package session

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreSaveGetDelete(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()
	ctx := context.Background()

	sess := &Session{
		ID:        "abc-123",
		Subject:   "sub-xyz",
		Username:  "john.doe",
		Slug:      "john-doe",
		Groups:    []string{"developers"},
		IsAdmin:   false,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	if err := store.Save(ctx, sess); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get(ctx, "abc-123")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Subject != sess.Subject {
		t.Errorf("Subject = %q, want %q", got.Subject, sess.Subject)
	}
	if got.Slug != sess.Slug {
		t.Errorf("Slug = %q, want %q", got.Slug, sess.Slug)
	}
	if len(got.Groups) != 1 || got.Groups[0] != "developers" {
		t.Errorf("Groups = %v, want [developers]", got.Groups)
	}

	if err := store.Delete(ctx, "abc-123"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = store.Get(ctx, "abc-123")
	if err != ErrNotFound {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestMemoryStoreGetNotFound(t *testing.T) {
	store := NewMemoryStore()
	defer store.Close()

	_, err := store.Get(context.Background(), "nonexistent")
	if err != ErrNotFound {
		t.Errorf("Get nonexistent = %v, want ErrNotFound", err)
	}
}

func TestSessionIsExpired(t *testing.T) {
	now := time.Now()

	// Session with future expiry
	s := &Session{ExpiresAt: now.Add(1 * time.Hour)}
	if s.IsExpired(now) {
		t.Error("session with future expiry should not be expired")
	}

	// Session with past expiry
	s = &Session{ExpiresAt: now.Add(-1 * time.Hour)}
	if !s.IsExpired(now) {
		t.Error("session with past expiry should be expired")
	}

	// Session with zero expiry (never expires)
	s = &Session{}
	if s.IsExpired(now) {
		t.Error("session with zero expiry should not be expired")
	}
}
