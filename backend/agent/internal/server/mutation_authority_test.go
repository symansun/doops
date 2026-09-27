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
	if token != "28d6500373b1a0262e72fa3a7f5c8f75fe459b0e1703a64f0439ad87f8f629bd" {
		t.Fatalf("token = %s", token)
	}
	if digest != "sha256:2df62012da35b1cb3d4961dc7c989a674cdc6514652be49de097013c24574e96" {
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
