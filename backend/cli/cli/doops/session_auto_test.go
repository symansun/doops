package main

import (
	"regexp"
	"testing"
)

func TestResolveSession(t *testing.T) {
	generated := regexp.MustCompile(`^ses_[0-9a-f]{16}$`)
	cases := []struct {
		name, in, want string
	}{
		{"explicit session is kept", "release-abc", "release-abc"},
		{"surrounding space is trimmed", "  ops-1 ", "ops-1"},
		{"empty session is generated", "", ""},
		{"blank session is generated", "   ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSession(tc.in)
			if tc.want != "" {
				if got != tc.want {
					t.Fatalf("resolveSession(%q) = %q, want %q", tc.in, got, tc.want)
				}
				return
			}
			if !generated.MatchString(got) {
				t.Fatalf("resolveSession(%q) = %q, want a generated ses_<16 hex>", tc.in, got)
			}
			if again := resolveSession(tc.in); again == got {
				t.Fatalf("two generated sessions collided: %q", got)
			}
		})
	}
}
