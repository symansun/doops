package server

import (
	"reflect"
	"testing"
)

func TestSignMutationAuthorizationMatchesDoagentProtocol(t *testing.T) {
	secret := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	authorityID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	allowedTools := []string{
		"Read",
		"Glob",
		"Grep",
		"Skill",
		"mcp_doops_k8s_read",
		"mcp_doops_helm",
	}

	token, digest, err := signMutationAuthorization(
		secret,
		"session-golden",
		authorityID,
		1_899_999_999,
		allowedTools,
	)
	if err != nil {
		t.Fatal(err)
	}
	if token != "c219fc4b3fec19a358dadff87d785631da5da40d167dd346acdc80f67db786f0" {
		t.Fatalf("token = %s", token)
	}
	if digest != "sha256:9bb5053b3aa7b937e1a5dc3837f7ca7ec665e9a8d91aea11aacd2a7f3bdf974e" {
		t.Fatalf("digest = %s", digest)
	}

	changedTools := append([]string(nil), allowedTools...)
	changedTools[0], changedTools[1] = changedTools[1], changedTools[0]
	changedToken, _, err := signMutationAuthorization(
		secret,
		"session-golden",
		authorityID,
		1_899_999_999,
		changedTools,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(token, changedToken) {
		t.Fatal("tool order was not bound into the authority token")
	}
}
