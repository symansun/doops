package server

import "testing"

func TestValidatePromptCredentialAccess(t *testing.T) {
	cases := []struct {
		name      string
		operation string
		skill     string
		has       bool
		wantError bool
	}{
		{name: "no credentials", operation: "ask", skill: "", has: false},
		{name: "dosql ask", operation: "ask", skill: "dosql", has: true},
		{name: "dosql implicit ask", operation: "", skill: "dosql", has: true},
		{name: "apply generic", operation: "apply", skill: "", has: true},
		{name: "generic ask denied", operation: "ask", skill: "", has: true, wantError: true},
		{name: "wrong skill denied", operation: "ask", skill: "doweb", has: true, wantError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePromptCredentialAccess(tc.operation, tc.skill, tc.has)
			if tc.wantError && err == nil {
				t.Fatal("expected credential access to be denied")
			}
			if !tc.wantError && err != nil {
				t.Fatalf("unexpected denial: %v", err)
			}
		})
	}
}
