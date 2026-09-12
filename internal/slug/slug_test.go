package slug

import "testing"

func TestMake(t *testing.T) {
	tests := []struct {
		username string
		maxLen   int
		want     string
	}{
		{"john.doe", 63, "john-doe"},
		{"John_Doe", 63, "john-doe"},
		{"john@doe.com", 63, "john-doe-com"},
		{"  hello world  ", 63, "hello-world"},
		{"Already-Clean", 63, "already-clean"},
		{"MixedCase123", 63, "mixedcase123"},
		{"___leading", 63, "leading"},
		{"trailing___", 63, "trailing"},
		{"___both___", 63, "both"},
		{"ABC-123-xyz", 63, "abc-123-xyz"},
	}

	for _, tt := range tests {
		t.Run(tt.username, func(t *testing.T) {
			got := Make(tt.username, tt.maxLen)
			if got != tt.want {
				t.Errorf("Make(%q, %d) = %q, want %q", tt.username, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestMakeTruncation(t *testing.T) {
	long := "aVeryLongUsernameThatExceedsTheMaximumLengthAllowed"
	got := Make(long, 20)
	if len(got) > 20 {
		t.Errorf("slug length = %d, want <= 20", len(got))
	}
	if got != "averylongusernametha" {
		t.Errorf("Make truncated = %q, want %q", got, "averylongusernametha")
	}
}

func TestMakeEmpty(t *testing.T) {
	if got := Make("", 63); got != "" {
		t.Errorf("Make(\"\") = %q, want empty", got)
	}
	if got := Make("---", 63); got != "" {
		t.Errorf("Make(\"---\") = %q, want empty", got)
	}
}

func TestObjectName(t *testing.T) {
	got := ObjectName("codx", "john-doe", 63)
	if got != "codx-john-doe" {
		t.Errorf("ObjectName = %q, want codx-john-doe", got)
	}
}

func TestObjectNameTruncation(t *testing.T) {
	got := ObjectName("codx-prod-release", "a-very-long-username-here", 25)
	if len(got) > 25 {
		t.Errorf("ObjectName length = %d, want <= 25", len(got))
	}
	if got != "codx-prod-release-a-very" {
		t.Errorf("ObjectName truncated = %q, want %q", got, "codx-prod-release-a-very")
	}
}
