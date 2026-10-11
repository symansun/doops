package server

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHelmMaterializationRequiresUsableNativeContext(t *testing.T) {
	resource := CredentialResource{ID: "cred_" + strings.Repeat("a", 24), Type: CredentialTypeHelmRepository}
	grantID := "credgrant_" + strings.Repeat("b", 24)
	reference := CredentialPlanReference{Scope: CredentialGrantScope{Namespace: "apps", Use: CredentialUseHelmPull}}
	valid := CredentialMaterialization{
		CredentialID: resource.ID, GrantID: grantID, VersionID: "credver_" + strings.Repeat("c", 24),
		ResourceName: credentialSecretName(resource.ID, grantID), Namespace: "apps", ResourceVersion: "native-7",
		Status: "verified", Digest: "sha256:" + strings.Repeat("d", 64), ContextKind: "helm-native",
		ContextSocket: filepath.Join(t.TempDir(), "doops-native-helm-test", "broker.sock"),
	}
	hub := &GatewayHub{}
	if err := hub.validateCredentialMaterializationResult(valid, resource, grantID, reference, nil, ""); err != nil {
		t.Fatalf("valid native context rejected: %v", err)
	}
	for name, mutate := range map[string]func(*CredentialMaterialization){
		"missing kind":   func(m *CredentialMaterialization) { m.ContextKind = "" },
		"missing socket": func(m *CredentialMaterialization) { m.ContextSocket = "" },
		"missing both":   func(m *CredentialMaterialization) { m.ContextKind, m.ContextSocket = "", "" },
		"invalid socket": func(m *CredentialMaterialization) { m.ContextSocket = "/tmp/unrelated.sock" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := valid
			mutate(&invalid)
			if err := hub.validateCredentialMaterializationResult(invalid, resource, grantID, reference, nil, ""); err == nil {
				t.Fatal("unusable native context would permit verification and promotion")
			}
		})
	}
	resource.Type = CredentialTypeOpaque
	invalid := valid
	invalid.ContextKind, invalid.SecretType, invalid.Digest = "", "Opaque", ""
	if err := hub.validateCredentialMaterializationResult(invalid, resource, grantID, reference, nil, ""); err == nil {
		t.Fatal("opaque Secret unexpectedly accepted a native context socket")
	}
}
