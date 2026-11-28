package server

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func readVaultMarkerMAC(t *testing.T, store *GatewayStore) string {
	t.Helper()
	var mac string
	if err := store.db.QueryRow(`SELECT key_mac FROM inventory_machine_login_key WHERE singleton = 1`).Scan(&mac); err != nil {
		t.Fatalf("read vault marker: %v", err)
	}
	return mac
}

func requirePreview(t *testing.T, got SecretRotationPreview, want SecretRotationPreview) {
	t.Helper()
	if got != want {
		t.Fatalf("preview = %+v, want %+v", got, want)
	}
}

func TestPreviewSecretRotationCountsWithoutWriting(t *testing.T) {
	store, path, _ := startRotationWindow(t, syntheticMasterKey(0x61), syntheticMasterKey(0x62))
	defer store.Close()
	ctx := context.Background()

	beforeCiphertexts := snapshotCiphertexts(t, store)
	beforeGatewayMAC := storedSecretKeyMAC(t, store)
	beforeVaultMAC := readVaultMarkerMAC(t, store)

	requirePreview(t, mustPreviewSecretRotation(t, store), SecretRotationPreview{
		CredentialVersions: 3, MachineLoginVault: 2, AlreadyPrimary: 0,
	})
	if after := snapshotCiphertexts(t, store); !sameCiphertextSnapshot(beforeCiphertexts, after) {
		t.Fatalf("preview rewrote stored ciphertext")
	}
	if got := storedSecretKeyMAC(t, store); got != beforeGatewayMAC {
		t.Fatalf("preview changed the gateway marker")
	}
	if got := readVaultMarkerMAC(t, store); got != beforeVaultMAC {
		t.Fatalf("preview changed the vault marker")
	}

	// The read-only connection reads the same pending rows while the live store
	// is still open on its WAL.
	requirePreview(t, mustPreviewSecretRotationAtPath(t, path), SecretRotationPreview{
		CredentialVersions: 3, MachineLoginVault: 2, AlreadyPrimary: 0,
	})

	if report, err := store.RotateSecretKey(ctx, SecretRotationOptions{}); err != nil {
		t.Fatalf("rotate: %v", err)
	} else {
		requireRotationReport(t, report, SecretRotationReport{CredentialVersions: 3, MachineLoginVault: 2})
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read database: %v", err)
	}

	requirePreview(t, mustPreviewSecretRotationAtPath(t, path), SecretRotationPreview{AlreadyPrimary: 5})
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read database after preview: %v", err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("read-only preview changed the database file: %d bytes before, %d bytes after", len(beforeBytes), len(afterBytes))
	}
}

func TestPreviewSecretRotationAtPathDoesNotCreateTheDatabase(t *testing.T) {
	t.Setenv(secretKeyEnv, syntheticMasterKey(0x63))
	t.Setenv(previousSecretKeyEnv, "")
	path := filepath.Join(t.TempDir(), "absent.db")
	if _, err := PreviewSecretRotationAtPath(context.Background(), path); err == nil {
		t.Fatalf("preview of an absent database returned no error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("preview created %s", path)
	}
}

func mustPreviewSecretRotation(t *testing.T, store *GatewayStore) SecretRotationPreview {
	t.Helper()
	preview, err := store.PreviewSecretRotation(context.Background())
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	return preview
}

func mustPreviewSecretRotationAtPath(t *testing.T, path string) SecretRotationPreview {
	t.Helper()
	preview, err := PreviewSecretRotationAtPath(context.Background(), path)
	if err != nil {
		t.Fatalf("preview at path: %v", err)
	}
	return preview
}

func sameCiphertextSnapshot(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for id, value := range a {
		if b[id] != value {
			return false
		}
	}
	return true
}
