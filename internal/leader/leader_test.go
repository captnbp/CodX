package leader

import (
	"testing"
)

func TestPodIdentity(t *testing.T) {
	t.Setenv("POD_NAME", "codx-7d9c6b5f4-x2kqz")
	t.Setenv("POD_UID", "f47ac10b-58cc-4372-a567-0e02b2c3d479")
	if got, want := PodIdentity(), "codx-7d9c6b5f4-x2kqz_f47ac10b-58cc-4372-a567-0e02b2c3d479"; got != want {
		t.Errorf("PodIdentity() = %q, want %q", got, want)
	}
}

func TestPodIdentityWithoutUID(t *testing.T) {
	t.Setenv("POD_NAME", "")
	t.Setenv("POD_UID", "")
	t.Setenv("HOSTNAME", "codx-host")
	if got, want := PodIdentity(), "codx-host"; got != want {
		t.Errorf("PodIdentity() = %q, want %q", got, want)
	}
}

func TestEnsureLeaseNamespaceDefault(t *testing.T) {
	if got, want := EnsureLeaseNamespaceDefault("custom"), "custom"; got != want {
		t.Errorf("EnsureLeaseNamespaceDefault(\"custom\") = %q, want %q", got, want)
	}
	t.Setenv("POD_NAMESPACE", "codx-system")
	if got, want := EnsureLeaseNamespaceDefault(""), "codx-system"; got != want {
		t.Errorf("EnsureLeaseNamespaceDefault(\"\") = %q, want %q", got, want)
	}
}
