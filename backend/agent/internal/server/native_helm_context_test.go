package server

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestCleanupNativeHelmCredentialRemovesAllMatchingContexts(t *testing.T) {
	auth := nativeHelmAuth{
		URL:      "https://charts.example.test",
		Username: "user",
		Password: "synthetic-canary",
	}
	const (
		namespace    = "apps"
		credentialID = "cred_aaaaaaaaaaaaaaaaaaaaaaaa"
		grantID      = "credgrant_bbbbbbbbbbbbbbbbbbbbbbbb"
	)
	makeRequest := func(version string) CredentialMaterializeRequest {
		return CredentialMaterializeRequest{
			SessionID:      "helm-cleanup-" + version,
			CredentialID:   credentialID,
			GrantID:        grantID,
			VersionID:      version,
			CredentialType: CredentialTypeHelmRepository,
			Scope: CredentialGrantScope{
				Namespace:   namespace,
				Use:         CredentialUseHelmPull,
				HelmRelease: "app",
				HelmChart:   "https://charts.example.test/app.tgz",
			},
		}
	}
	first, err := materializeNativeHelmCredential(context.Background(), makeRequest("credver_cccccccccccccccccccccccc"), auth, "sha256:"+strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	second, err := materializeNativeHelmCredential(context.Background(), makeRequest("credver_dddddddddddddddddddddddd"), auth, "sha256:"+strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = cleanupNativeHelmCredential(CredentialCleanupRequest{
			Namespace: namespace, CredentialID: credentialID, GrantID: grantID, AnyVersion: true,
		})
	})

	cleaned, err := cleanupNativeHelmCredential(CredentialCleanupRequest{
		Namespace: namespace, CredentialID: credentialID, GrantID: grantID, AnyVersion: true,
	})
	if err != nil || !cleaned {
		t.Fatalf("cleanup = %v, %v", cleaned, err)
	}
	for _, socket := range []string{first.ContextSocket, second.ContextSocket} {
		if _, err := os.Stat(socket); !os.IsNotExist(err) {
			t.Fatalf("native Helm socket survived cleanup: %s (%v)", socket, err)
		}
	}
	nativeHelmContexts.Lock()
	defer nativeHelmContexts.Unlock()
	for _, record := range nativeHelmContexts.bySocket {
		if record.Namespace == namespace && record.CredentialID == credentialID && record.GrantID == grantID {
			t.Fatalf("matching native Helm context survived cleanup: %#v", record)
		}
	}
}
