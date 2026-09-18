package k8s

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// defaultReloadInterval is how often Run re-lists profiles from the cluster
// when no explicit interval is provided.
const defaultReloadInterval = 30 * time.Second

// ProfileStore caches Profile CRs and provides filtered access based on
// the user's OIDC groups.
type ProfileStore struct {
	mu       sync.RWMutex
	profiles map[string]*profilev1.Profile // keyed by name
	log      logr.Logger
}

// NewProfileStore creates an empty ProfileStore.
func NewProfileStore() *ProfileStore {
	return &ProfileStore{
		profiles: make(map[string]*profilev1.Profile),
	}
}

// WithLogger sets the logger used by Run for profile watch logging.
func (s *ProfileStore) WithLogger(log logr.Logger) *ProfileStore {
	s.log = log
	return s
}

// Run loads profiles from the cluster on start and re-lists them on a fixed
// interval so the cache stays in sync with Profile CRs created, updated, or
// deleted after startup. It blocks until ctx is cancelled.
func (s *ProfileStore) Run(ctx context.Context, client ProfileClient, namespace string, reloadInterval time.Duration) {
	if reloadInterval <= 0 {
		reloadInterval = defaultReloadInterval
	}

	log := s.log
	if log.GetSink() == nil {
		log = logr.Discard()
	}
	log = log.WithName("profile-store")

	// Initial load before the first tick so profiles are available immediately.
	if err := s.Load(ctx, client, namespace); err != nil {
		log.Error(err, "initial profile load failed", "namespace", namespace)
	} else {
		log.Info("loaded profiles", "namespace", namespace, "count", s.Count())
	}

	ticker := time.NewTicker(reloadInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("profile watch stopped")
			return
		case <-ticker.C:
			if err := s.Load(ctx, client, namespace); err != nil {
				log.Error(err, "reload profiles failed", "namespace", namespace)
				continue
			}
			log.V(1).Info("reloaded profiles", "namespace", namespace, "count", s.Count())
		}
	}
}

// Count returns the number of cached profiles.
func (s *ProfileStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.profiles)
}

// Load fetches all Profile CRs from the cluster and populates the cache.
func (s *ProfileStore) Load(ctx context.Context, client ProfileClient, namespace string) error {
	list, err := client.List(ctx, namespace, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list profiles: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles = make(map[string]*profilev1.Profile, len(list.Items))
	for i := range list.Items {
		p := &list.Items[i]
		s.profiles[p.Name] = p
	}
	return nil
}

// Upsert adds or replaces a single profile in the cache.
func (s *ProfileStore) Upsert(p *profilev1.Profile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles[p.Name] = p.DeepCopy()
}

// Delete removes a profile from the cache by name.
func (s *ProfileStore) Delete(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.profiles, name)
}

// All returns all cached profiles.
func (s *ProfileStore) All() []*profilev1.Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*profilev1.Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		result = append(result, p.DeepCopy())
	}
	return result
}

// AllowedForGroups returns the profiles the user is allowed to use based on
// their OIDC group memberships. A profile with empty OIDCGroups is allowed for
// all authenticated users.
func (s *ProfileStore) AllowedForGroups(groups []string) []*profilev1.Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*profilev1.Profile, 0)
	for _, p := range s.profiles {
		if isProfileAllowed(p, groups) {
			result = append(result, p.DeepCopy())
		}
	}
	return result
}

// isProfileAllowed checks whether any of the user's groups match the profile's
// allowed groups. An empty OIDCGroups list means all authenticated users.
func isProfileAllowed(profile *profilev1.Profile, userGroups []string) bool {
	if len(profile.Spec.OIDCGroups) == 0 {
		return true
	}
	for _, allowed := range profile.Spec.OIDCGroups {
		for _, user := range userGroups {
			if allowed == user {
				return true
			}
		}
	}
	return false
}

// Get returns a single profile by name, or nil if not found.
func (s *ProfileStore) Get(name string) *profilev1.Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.profiles[name]; ok {
		return p.DeepCopy()
	}
	return nil
}
