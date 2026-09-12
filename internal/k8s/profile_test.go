package k8s

import (
	"context"
	"testing"

	profilev1 "github.com/captnbp/CodX/api/profile/v1"
)

func TestProfileStoreAllowedForGroups(t *testing.T) {
	profiles := []*profilev1.Profile{
		testProfile("python-dev", "Python Dev", []string{"developers"}),
		testProfile("admin-tools", "Admin Tools", []string{"codx-admins"}),
		testProfile("default", "Default", nil), // empty = all users
	}

	fakeClient := &fakeProfileClient{profiles: profiles}
	store := NewProfileStore()

	if err := store.Load(context.Background(), fakeClient, "codx-system"); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// User in "developers" group.
	allowed := store.AllowedForGroups([]string{"developers"})
	if len(allowed) != 2 {
		t.Errorf("developers: got %d profiles, want 2", len(allowed))
	}
	names := profileNames(allowed)
	if !contains(names, "python-dev") {
		t.Errorf("developers: should include python-dev, got %v", names)
	}
	if !contains(names, "default") {
		t.Errorf("developers: should include default, got %v", names)
	}
	if contains(names, "admin-tools") {
		t.Errorf("developers: should not include admin-tools, got %v", names)
	}

	// User in "codx-admins" group.
	allowed = store.AllowedForGroups([]string{"codx-admins"})
	if len(allowed) != 2 {
		t.Errorf("admin: got %d profiles, want 2", len(allowed))
	}
	names = profileNames(allowed)
	if !contains(names, "admin-tools") {
		t.Errorf("admin: should include admin-tools, got %v", names)
	}
	if !contains(names, "default") {
		t.Errorf("admin: should include default, got %v", names)
	}

	// User with no groups at all — only sees the open profile.
	allowed = store.AllowedForGroups([]string{})
	if len(allowed) != 1 {
		t.Errorf("no-groups: got %d profiles, want 1", len(allowed))
	}
	if allowed[0].Name != "default" {
		t.Errorf("no-groups: got %q, want default", allowed[0].Name)
	}

	// User in both groups.
	allowed = store.AllowedForGroups([]string{"developers", "codx-admins"})
	if len(allowed) != 3 {
		t.Errorf("both-groups: got %d profiles, want 3", len(allowed))
	}
}

func TestProfileStoreUpsertDelete(t *testing.T) {
	store := NewProfileStore()

	p := testProfile("new-profile", "New Profile", nil)
	store.Upsert(p)

	if store.Get("new-profile") == nil {
		t.Error("Upsert should add the profile")
	}

	store.Delete("new-profile")
	if store.Get("new-profile") != nil {
		t.Error("Delete should remove the profile")
	}
}

func TestProfileStoreLoad(t *testing.T) {
	profiles := []*profilev1.Profile{
		testProfile("a", "A", nil),
		testProfile("b", "B", nil),
	}

	store := NewProfileStore()
	fakeClient := &fakeProfileClient{profiles: profiles}

	if err := store.Load(context.Background(), fakeClient, "codx-system"); err != nil {
		t.Fatalf("Load: %v", err)
	}

	all := store.All()
	if len(all) != 2 {
		t.Errorf("Load: got %d profiles, want 2", len(all))
	}
}

func profileNames(profiles []*profilev1.Profile) []string {
	names := make([]string, 0, len(profiles))
	for _, p := range profiles {
		names = append(names, p.Name)
	}
	return names
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
