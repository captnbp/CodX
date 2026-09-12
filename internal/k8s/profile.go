package k8s

import (
	"context"
	"fmt"
	"sync"

	profilev1 "github.com/captnbp/CodX/api/profile/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProfileStore caches Profile CRs and provides filtered access based on
// the user's OIDC groups.
type ProfileStore struct {
	mu       sync.RWMutex
	profiles map[string]*profilev1.Profile // keyed by name
}

// NewProfileStore creates an empty ProfileStore.
func NewProfileStore() *ProfileStore {
	return &ProfileStore{
		profiles: make(map[string]*profilev1.Profile),
	}
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
