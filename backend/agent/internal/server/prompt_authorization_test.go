package server

import "testing"

func TestValidatePromptCredentialAccess(t *testing.T) {
	cases := []struct {
		name      string
		operation string
		has       bool
		wantError bool
	}{
		{name: "no credentials", operation: "ask", has: false},
		{name: "dosql ask", operation: "ask", has: true},
		{name: "generic ask", operation: "ask", has: true},
		{name: "implicit ask", operation: "", has: true},
		{name: "apply generic", operation: "apply", has: true},
		{name: "unsupported operation denied", operation: "inspect", has: true, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePromptCredentialAccess(tc.operation, tc.has)
			if tc.wantError && err == nil {
				t.Fatal("expected credential access to be denied")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("unexpected denial: %v", err)
			}
		})
	}
}
