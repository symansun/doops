package server

import (
	"reflect"
	"testing"
)

func TestMutationContextDigestBindsInstructionAndDirectTarget(t *testing.T) {
	base, err := mutationContextDigest(
		"release-session",
		"deploy the current workspace with Helm",
	)
	if err != nil {
		t.Fatal(err)
	}
	changedInstruction, err := mutationContextDigest(
		"release-session",
		"deploy a different release with Helm",
	)
	if err != nil {
		t.Fatal(err)
	}
	if base == changedInstruction {
		t.Fatal("instruction was not bound into mutation context")
	}
}

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
		"mcp_doops_web",
	}
	contextDigest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	token, digest, err := signMutationAuthorization(
		secret,
		"session-golden",
		authorityID,
		1_899_999_999,
		allowedTools,
		contextDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if token != "044840f7fc90ad122fa7a86636d7f49eec02fd946afbfc317901bb5b8e1c62e1" {
		t.Fatalf("token = %s", token)
	}
	if digest != "sha256:489043a76d614c14fd712b47324f344bf8cb4bb48d8c89d8981bebfacacf612a" {
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
		contextDigest,
	)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(token, changedToken) {
		t.Fatal("tool order was not bound into the authority token")
	}
	changedContextToken, _, err := signMutationAuthorization(
		secret,
		"session-golden",
		authorityID,
		1_899_999_999,
		allowedTools,
		"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(token, changedContextToken) {
		t.Fatal("task and workspace context was not bound into the authority token")
	}
}
