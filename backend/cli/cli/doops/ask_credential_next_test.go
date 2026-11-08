package main

import (
	"fmt"
	"strings"
	"testing"
)

// A target's bound Helm credential is used automatically, so the only failures
// left for the caller to act on are "none is bound" and "several are bound".
// Each one must come back as the command that fixes it.
func TestAskCredentialNextGuidesEachHelmCredentialFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      error
		wantAll []string
	}{
		{
			"repository requires a credential",
			fmt.Errorf("helm_chart_requires_credential: helm_artifact_http_401"),
			[]string{"next:", "doops credential list --target tgt", "--type helmRepository", "--use helmPull", "no --credential needed"},
		},
		{
			"several are bound",
			fmt.Errorf("credential_reference_ambiguous"),
			[]string{"doops credential list --target tgt", "--credential <name>"},
		},
		{
			"bound but never promoted",
			fmt.Errorf("credential_version_not_found"),
			[]string{"--id <id>", "--use helmPull"},
		},
		{
			// A name that does not exist and a name the caller may not use
			// answer the same way, so the reply never reveals a credential
			// outside the caller's authority.
			"named credential not usable on this target",
			fmt.Errorf("remote error: credential_grant_denied"),
			[]string{"doops credential list --target tgt", "rerun without --credential"},
		},
		{"named credential forbidden", fmt.Errorf("credential_forbidden"), []string{"doops credential list --target tgt"}},
		{"unrelated failure", fmt.Errorf("helm_release_pending"), nil},
		{"anonymous download still allowed", fmt.Errorf("helm_artifact_http_404"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := askCredentialNext(tc.in, "tgt")
			if tc.wantAll == nil {
				if got != tc.in {
					t.Fatalf("unrelated error was rewritten: %v", got)
				}
				return
			}
			for _, want := range tc.wantAll {
				if !strings.Contains(got.Error(), want) {
					t.Errorf("missing %q in %q", want, got.Error())
				}
			}
			if !strings.Contains(got.Error(), tc.in.Error()) {
				t.Errorf("original message lost: %q", got.Error())
			}
		})
	}
	if askCredentialNext(nil, "tgt") != nil {
		t.Fatal("nil must stay nil")
	}
}

// The guidance tells the caller how to bind a credential; it must never carry a
// payload, a password prompt, or a place to type one.
func TestAskCredentialNextNeverAsksForAPayload(t *testing.T) {
	for _, in := range []error{
		fmt.Errorf("helm_chart_requires_credential: helm_artifact_http_401"),
		fmt.Errorf("credential_reference_ambiguous"),
		fmt.Errorf("credential_version_not_found"),
	} {
		message := strings.ToLower(askCredentialNext(in, "tgt").Error())
		for _, forbidden := range []string{"--password", "--token", "password=", "token=", "enter your", "paste"} {
			if strings.Contains(message, forbidden) {
				t.Fatalf("guidance asks for a payload (%q): %s", forbidden, message)
			}
		}
	}
}
