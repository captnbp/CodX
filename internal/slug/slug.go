// Package slug converts OIDC usernames into Kubernetes-safe slugs.
package slug

import (
	"strings"
)

// Make converts a username into a Kubernetes-safe slug:
//   - lowercase
//   - replace [^a-z0-9-] with "-"
//   - trim leading/trailing "-"
//   - truncate to maxLen
func Make(username string, maxLen int) string {
	s := strings.ToLower(username)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s = b.String()
	s = strings.Trim(s, "-")
	if maxLen > 0 && len(s) > maxLen {
		s = s[:maxLen]
		s = strings.TrimRight(s, "-")
	}
	return s
}

// ObjectName returns the Kubernetes object name for a workspace resource:
// <instance>-<slug>, truncated to maxLen.
func ObjectName(instance, slug string, maxLen int) string {
	name := instance + "-" + slug
	if maxLen > 0 && len(name) > maxLen {
		name = name[:maxLen]
		name = strings.TrimRight(name, "-")
	}
	return name
}
