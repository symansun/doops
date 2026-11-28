package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeOpaqueCredentialMaterializeAndShredCleanup(t *testing.T) {
	ctx := context.Background()
	const canaryID = "LTAI-SYNTHETIC-CANARY-ID"
	const canarySecret = "SYNTHETIC-ALIDNS-SECRET-CANARY-999"

	payload, _ := json.Marshal(map[string]any{
		"data": map[string]string{
			"ALIYUN_ACCESS_KEY_ID":     canaryID,
			"ALIYUN_ACCESS_KEY_SECRET": canarySecret,
		},
	})
	req := CredentialMaterializeRequest{
		SessionID:      "sess-dns-opaque-1",
		Cluster:        "prod",
		Instance:       "dns-1",
		CredentialID:   "cred_aaaaaaaaaaaaaaaaaaaaaaaa",
		GrantID:        "credgrant_bbbbbbbbbbbbbbbbbbbbbbbb",
		VersionID:      "credver_cccccccccccccccccccccccc",
		CredentialType: CredentialTypeOpaque,
		Scope: CredentialGrantScope{
			Cluster:   "prod",
			Instance:  "dns-1",
			Namespace: "ops",
			Use:       CredentialUseOpaqueSecret,
		},
		RequiredKeys:   []string{"ALIYUN_ACCESS_KEY_ID", "ALIYUN_ACCESS_KEY_SECRET"},
		Payload:        payload,
		RuntimeEnvOnly: true,
	}

	mat, err := materializeCredential(ctx, req)
	if err != nil {
		t.Fatalf("materializeCredential(RuntimeEnvOnly) failed: %v", err)
	}
	if mat.ContextKind != runtimeOpaqueContextKind {
		t.Fatalf("ContextKind = %q, want %q", mat.ContextKind, runtimeOpaqueContextKind)
	}
	if mat.SecretType != "" {
		t.Fatalf("runtime opaque must not set SecretType, got %q", mat.SecretType)
	}
	if !validRuntimeOpaqueEnvPath(mat.ContextSocket) {
		t.Fatalf("invalid ContextSocket path %q", mat.ContextSocket)
	}
	if err := validateCredentialMaterializationMetadata(mat); err != nil {
		t.Fatalf("validateCredentialMaterializationMetadata rejected runtime opaque: %v", err)
	}

	// Verify 0600 file inside 0700 dir and valid JSON payload without ever invoking kubectl.
	info, err := os.Stat(mat.ContextSocket)
	if err != nil {
		t.Fatalf("stat runtime opaque file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("file perm = %o, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(mat.ContextSocket))
	if err != nil || dirInfo.Mode().Perm() != 0700 {
		t.Fatalf("dir perm = %v err=%v, want 0700", dirInfo.Mode().Perm(), err)
	}
	raw, err := os.ReadFile(mat.ContextSocket)
	if err != nil {
		t.Fatalf("read runtime opaque file: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode runtime opaque JSON: %v", err)
	}
	if decoded["ALIYUN_ACCESS_KEY_ID"] != canaryID || decoded["ALIYUN_ACCESS_KEY_SECRET"] != canarySecret {
		t.Fatalf("unexpected decoded payload keys: %v", mapKeys(decoded))
	}

	// Metadata JSON returned to Gateway must never contain any secret canary.
	metaJSON, _ := json.Marshal(mat)
	if strings.Contains(string(metaJSON), canaryID) || strings.Contains(string(metaJSON), canarySecret) {
		t.Fatalf("materialization metadata leaked secret canary!")
	}

	// Cleanup must shred & delete file and parent directory.
	if err := cleanupCredential(ctx, CredentialCleanupRequest{
		CredentialType: CredentialTypeOpaque,
		SessionID:      req.SessionID,
		CredentialID:   req.CredentialID,
		GrantID:        req.GrantID,
		VersionID:      req.VersionID,
		Namespace:      req.Scope.Namespace,
		RuntimeEnvOnly: true,
	}); err != nil {
		t.Fatalf("cleanupCredential(RuntimeEnvOnly) failed: %v", err)
	}
	if _, err := os.Stat(mat.ContextSocket); !os.IsNotExist(err) {
		t.Fatalf("expected runtime opaque file deleted after cleanup, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Dir(mat.ContextSocket)); !os.IsNotExist(err) {
		t.Fatalf("expected runtime opaque dir deleted after cleanup, stat err=%v", err)
	}
}

func TestRuntimeOpaqueRejectsNonOpaqueOrWrongUse(t *testing.T) {
	ctx := context.Background()
	_, err := materializeRuntimeOpaqueCredential(ctx, CredentialMaterializeRequest{
		CredentialType: CredentialTypeOpaque,
		Scope:          CredentialGrantScope{Use: CredentialUseDatabase},
	}, map[string]string{"k": "v"}, runtimeOpaquePayloadDigest([]byte(`{"data":{"k":"v"}}`)))
	if err == nil {
		t.Fatal("expected runtime opaque to reject non-opaqueSecret use")
	}
	if validRuntimeOpaqueEnvPath("/root/ws/sess/env.json") {
		t.Fatal("workspace path must never pass validRuntimeOpaqueEnvPath")
	}
}
