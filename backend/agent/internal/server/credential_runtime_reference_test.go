package server

import "testing"

func TestCredentialResourceIDReferenceOnlyAcceptsStableResourceIDs(t *testing.T) {
	if !isCredentialResourceIDReference("cred_0123456789abcdef01234567") {
		t.Fatal("valid credential resource ID was not recognized")
	}
	for _, value := range []string{
		"agentcloud-edu-coder-postgres",
		"credgrant_0123456789abcdef01234567",
		"credver_0123456789abcdef01234567",
		"cred_0123456789abcdef0123456",
		"cred_0123456789abcdef012345678",
	} {
		if isCredentialResourceIDReference(value) {
			t.Fatalf("non-resource reference was accepted: %s", value)
		}
	}
}
