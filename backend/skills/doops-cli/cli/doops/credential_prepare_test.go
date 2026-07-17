package main

import (
	"strings"
	"testing"
)

func TestDecodeCredentialPrepareFailureRequiresExactVersionedObject(t *testing.T) {
	valid := []byte(`{"apiVersion":"doops.sh/v2","kind":"CredentialPrepareError","category":"agent_protocol_incompatible"}`)
	category, err := decodeCredentialPrepareFailure(valid)
	if err != nil || category != "agent_protocol_incompatible" {
		t.Fatalf("decode valid credential failure: category=%q err=%v", category, err)
	}
	for name, body := range map[string][]byte{
		"plain text":    []byte("agent_protocol_incompatible"),
		"unknown field": []byte(`{"apiVersion":"doops.sh/v2","kind":"CredentialPrepareError","category":"agent_protocol_incompatible","extra":true}`),
		"trailing JSON": []byte(`{"apiVersion":"doops.sh/v2","kind":"CredentialPrepareError","category":"agent_protocol_incompatible"} {}`),
		"wrong version": []byte(`{"apiVersion":"doops.sh/v3","kind":"CredentialPrepareError","category":"agent_protocol_incompatible"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeCredentialPrepareFailure(body); err == nil {
				t.Fatal("invalid credential failure must be rejected")
			}
		})
	}
}

func TestCredentialPrepareFailureIncludesRequiredProtocol(t *testing.T) {
	err := credentialPrepareFailureError(
		"426 Upgrade Required",
		"agent_protocol_incompatible",
		"doops.sh/credential-plan/v2",
	)
	if err == nil ||
		!strings.Contains(err.Error(), "credential prepare failed: HTTP 426 Upgrade Required: agent_protocol_incompatible") ||
		!strings.Contains(err.Error(), "required_protocol=doops.sh/credential-plan/v2") {
		t.Fatalf("required protocol missing from error: %v", err)
	}
}

func TestCredentialMaterializationMetadataRequiresPromptSafeProjection(t *testing.T) {
	materialization := credentialMaterializationMetadata{
		CredentialID: "cred_test", GrantID: "grant_test", VersionID: "version_test",
		ResourceName: "managed-secret", Namespace: "apps", SecretType: "Opaque",
		Keys: []string{"canary"}, ResourceVersion: "42", Status: "verified",
	}
	if err := validateCredentialMaterializationMetadata(materialization); err == nil {
		t.Fatal("target evidence was accepted in prompt credential metadata")
	}
	materialization.ResourceName = ""
	materialization.SecretType = ""
	materialization.Keys = nil
	materialization.ResourceVersion = ""
	if err := validateCredentialMaterializationMetadata(materialization); err != nil {
		t.Fatalf("prompt-safe credential metadata rejected: %v", err)
	}
}
