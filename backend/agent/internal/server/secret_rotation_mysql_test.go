package server

// INT-62 follow-up: the master key rotation was only ever exercised on SQLite.
// This file repeats the two-key window against the real MySQL 8.4 backend,
// opened through the production DOOPS_GATEWAY_STORE=mysql contract, so a
// statement or column type that only SQLite accepts fails here.
//
// The container is disposable. DOOPS_TEST_GATEWAY_MYSQL_COPY_PORT is the port
// of a MySQL 8.4 that accepts root without a password, the same
// entry point gateway_mysql_credential_e2e_test.go uses. It is fail closed:
// unset means skip, set but unreachable means FAIL, because a silently skipped
// gate would report a release nobody actually checked.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"
)

// seedMySQLRotationPayloads writes the three stored shapes the rotation must
// handle on MySQL: two AAD-bound credential versions, one legacy v1 envelope
// without an AAD, and two machine-login vault rows bound to different assets
// and usernames. It mirrors seedRotationPayloads, except the inventory tables
// already exist in the MySQL schema and must not be created from the SQLite
// migration file.
func seedMySQLRotationPayloads(t *testing.T, store *GatewayStore) rotationSeed {
	t.Helper()
	owner := createCredentialTestUser(t, store, "rotation-mysql-owner")
	var seed rotationSeed
	for i, name := range []string{"rotation-mysql-registry", "rotation-mysql-database"} {
		resource, err := store.CreateCredential(CredentialCreateRequest{
			Name: name, Scope: CredentialScopePersonal, Type: CredentialTypeRegistry,
			OwnerID: owner.ID, CreatedBy: owner.ID,
		})
		if err != nil {
			t.Fatalf("create credential: %v", err)
		}
		payload := "{\"server\":\"" + name + ".example.com\",\"username\":\"owner\",\"password\":\"" + rotationCanary + "-mysql-" + strconv.Itoa(i) + "\"}"
		version, err := store.PutCredentialVersion(CredentialVersionPutRequest{
			CredentialID: resource.ID, CreatedBy: owner.ID, Activate: true,
			Payload: json.RawMessage(payload),
		})
		if err != nil {
			t.Fatalf("put credential version: %v", err)
		}
		seed.credentials = append(seed.credentials, rotationRow{
			credentialID: resource.ID, versionID: version.ID, credentialType: resource.Type,
			digest: version.PayloadDigest, payload: payload,
		})
	}

	// A v1 row is the pre-AAD shape: no binding, prefix v1.
	legacyID, legacyVersionID := "cred_"+randomHex(12), "credver_"+randomHex(12)
	legacyPayload := "{\"server\":\"legacy.example.com\",\"password\":\"" + rotationCanary + "-mysql-legacy\"}"
	ciphertext, err := store.encryptSecret(legacyPayload)
	if err != nil {
		t.Fatalf("encrypt legacy payload: %v", err)
	}
	now := formatTime(time.Now().UTC())
	if _, err := store.db.Exec("INSERT INTO credentials(id,name,scope,owner_id,type,state,revoked_at,created_by,created_at,updated_at) VALUES(?,?,?,?,?,'active','',?,?,?)",
		legacyID, "rotation-mysql-legacy", CredentialScopePersonal, owner.ID, CredentialTypeRegistry, owner.ID, now, now); err != nil {
		t.Fatalf("insert legacy credential: %v", err)
	}
	if _, err := store.db.Exec("INSERT INTO credential_versions(id,credential_id,payload_ciphertext,payload_digest,state,revoked_at,created_by,created_at,promoted_at) VALUES(?,?,?,?,'active','',?,?,?)",
		legacyVersionID, legacyID, ciphertext, credentialPayloadDigest([]byte(legacyPayload)), owner.ID, now, now); err != nil {
		t.Fatalf("insert legacy version: %v", err)
	}
	seed.legacy = rotationRow{
		credentialID: legacyID, versionID: legacyVersionID, credentialType: CredentialTypeRegistry,
		payload: legacyPayload, legacy: true,
	}

	assets := []string{"mysql-asset-alpha", "mysql-asset-beta"}
	for i, asset := range assets {
		if _, err := store.db.Exec("INSERT INTO inventory_machines(asset_id,name,kind,address) VALUES(?,?,'physical',?)",
			asset, asset, "10.10.0."+strconv.Itoa(i+1)); err != nil {
			t.Fatalf("insert machine: %v", err)
		}
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin vault seed: %v", err)
	}
	defer tx.Rollback()
	for i, asset := range assets {
		row := rotationRow{
			assetID: asset, username: "deploy-" + strconv.Itoa(i),
			password: rotationCanary + "-mysql-vault-" + strconv.Itoa(i),
		}
		if err := store.storeMachineLogin(tx, row.assetID, row.username, row.password, TokenAuth{UserID: owner.ID}); err != nil {
			t.Fatalf("store machine login: %v", err)
		}
		if err := tx.QueryRow("SELECT credential_id, version_id FROM inventory_machine_login_accounts WHERE asset_id = ? AND username = ?",
			row.assetID, row.username).Scan(&row.credentialID, &row.versionID); err != nil {
			t.Fatalf("read vault binding: %v", err)
		}
		if err := tx.QueryRow("SELECT payload_digest FROM credential_versions WHERE id = ?", row.versionID).Scan(&row.digest); err != nil {
			t.Fatalf("read vault digest: %v", err)
		}
		row.credentialType = CredentialTypeOpaque
		seed.vaults = append(seed.vaults, row)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit vault seed: %v", err)
	}
	return seed
}

// mysqlCiphertextLengths snapshots every stored ciphertext length. MySQL
// payload_ciphertext is TEXT; a truncating column would shorten a re-encrypted
// row here even though SQLite accepted it.
func mysqlCiphertextLengths(t *testing.T, store *GatewayStore) map[string]int {
	t.Helper()
	rows, err := store.db.Query("SELECT id, OCTET_LENGTH(payload_ciphertext) FROM credential_versions")
	if err != nil {
		t.Fatalf("snapshot ciphertext lengths: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var length sql.NullInt64
		if err := rows.Scan(&id, &length); err != nil {
			t.Fatalf("scan ciphertext length: %v", err)
		}
		if !length.Valid {
			t.Fatalf("ciphertext row %s has no length", id)
		}
		out[id] = int(length.Int64)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ciphertext lengths: %v", err)
	}
	return out
}

// requireMySQLCiphertextNotTruncated compares every row's byte length before
// and after the rotation and reports the column definition. AES-GCM output only
// depends on the plaintext length, so a stable length proves the TEXT column
// stored the envelope whole.
func requireMySQLCiphertextNotTruncated(t *testing.T, store *GatewayStore, before, after map[string]int) {
	t.Helper()
	var dataType string
	var maxChars sql.NullInt64
	if err := store.db.QueryRow("SELECT DATA_TYPE, CHARACTER_MAXIMUM_LENGTH FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'credential_versions' AND COLUMN_NAME = 'payload_ciphertext'").Scan(&dataType, &maxChars); err != nil {
		t.Fatalf("read payload_ciphertext column definition: %v", err)
	}
	for id, oldLength := range before {
		newLength, ok := after[id]
		if !ok {
			t.Fatalf("ciphertext row %s disappeared during rotation", id)
		}
		if newLength != oldLength {
			t.Fatalf("ciphertext %s changed length across rotation: %d -> %d (truncation or envelope change)", id, oldLength, newLength)
		}
		if maxChars.Valid && int64(newLength) > maxChars.Int64 {
			t.Fatalf("ciphertext %s length %d exceeds payload_ciphertext CHARACTER_MAXIMUM_LENGTH %d", id, newLength, maxChars.Int64)
		}
		t.Logf("EVIDENCE ciphertext row=%s bytes_before=%d bytes_after=%d", id, oldLength, newLength)
	}
	t.Logf("EVIDENCE mysql column credential_versions.payload_ciphertext data_type=%s character_maximum_length=%v rows=%d", dataType, maxChars.Int64, len(after))
}

func TestRotateSecretKeyMySQLWindowThenRestart(t *testing.T) {
	oldKey, newKey := syntheticMasterKey('o'), syntheticMasterKey('n')
	t.Setenv(secretKeyEnv, oldKey)
	t.Setenv(previousSecretKeyEnv, "")
	store := openGatewayMySQLCredentialE2EStore(t)
	seed := seedMySQLRotationPayloads(t, store)
	markSecretKeyVerified(t, store)
	before := mysqlCiphertextLengths(t, store)

	t.Setenv(secretKeyEnv, newKey)
	t.Setenv(previousSecretKeyEnv, oldKey)
	if state, err := store.VerifySecretKey(); err != nil || state != GatewaySecretKeyRotationPending {
		t.Fatalf("VerifySecretKey in window = %v, %v, want rotation pending", state, err)
	}

	report, err := store.RotateSecretKey(context.Background(), SecretRotationOptions{BatchSize: 2})
	if err != nil {
		t.Fatalf("RotateSecretKey on MySQL: %v", err)
	}
	requireRotationReport(t, report, SecretRotationReport{CredentialVersions: 3, MachineLoginVault: 2})
	if got, want := storedSecretKeyMAC(t, store), rotationKeyMAC(t, newKey); got != want {
		t.Fatalf("key marker = %s, want the new primary MAC", got)
	}
	requireVaultMarker(t, store)

	var credentialRows, vaultRows int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM credential_versions v JOIN credentials c ON c.id = v.credential_id WHERE c.scope <> 'machine_login'").Scan(&credentialRows); err != nil {
		t.Fatalf("count credential rows: %v", err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM inventory_machine_login_accounts").Scan(&vaultRows); err != nil {
		t.Fatalf("count vault rows: %v", err)
	}
	if credentialRows != report.CredentialVersions || vaultRows != report.MachineLoginVault {
		t.Fatalf("rotation report %+v does not match real rows: credential_versions=%d machine_login_accounts=%d", report, credentialRows, vaultRows)
	}
	t.Logf("EVIDENCE rotation report=%+v credential_rows=%d vault_rows=%d marker=%s", report, credentialRows, vaultRows, storedSecretKeyMAC(t, store))

	requireMySQLCiphertextNotTruncated(t, store, before, mysqlCiphertextLengths(t, store))

	// A second run must rewrite nothing.
	snapshot := snapshotCiphertexts(t, store)
	again, err := store.RotateSecretKey(context.Background(), SecretRotationOptions{BatchSize: 2})
	if err != nil {
		t.Fatalf("second RotateSecretKey on MySQL: %v", err)
	}
	requireRotationReport(t, again, SecretRotationReport{AlreadyPrimary: 5})
	for id, ciphertext := range snapshotCiphertexts(t, store) {
		if snapshot[id] != ciphertext {
			t.Fatalf("second rotation rewrote ciphertext %s", id)
		}
	}

	// Only the new key: the marker matches it and all five payloads read back.
	if err := store.Close(); err != nil {
		t.Fatalf("close window store: %v", err)
	}
	t.Setenv(previousSecretKeyEnv, "")
	reopened, err := OpenGatewayStore("")
	if err != nil {
		t.Fatalf("reopen MySQL store with the new key: %v", err)
	}
	if state, err := reopened.VerifySecretKey(); err != nil || state != GatewaySecretKeyReady {
		t.Fatalf("VerifySecretKey with only the new key = %v, %v, want ready", state, err)
	}
	requireSeedReadable(t, reopened, seed)
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened store: %v", err)
	}

	// Only the old key: fail closed, no silent fallback.
	t.Setenv(secretKeyEnv, oldKey)
	stale, err := OpenGatewayStore("")
	if err != nil {
		t.Fatalf("reopen MySQL store with the old key: %v", err)
	}
	defer stale.Close()
	if _, err := stale.VerifySecretKey(); !errors.Is(err, ErrSecretKeyMismatch) {
		t.Fatalf("VerifySecretKey with only the old key = %v, want ErrSecretKeyMismatch", err)
	}
}

// TestRotateSecretKeyMySQLDoesNotOverwriteConcurrentWrite drives the CAS edge on
// MySQL: a writer replaces one row with the new key just before the first batch
// rewrites it, so the rotation must skip that row and report it instead of
// overwriting the fresher value.
func TestRotateSecretKeyMySQLDoesNotOverwriteConcurrentWrite(t *testing.T) {
	oldKey, newKey := syntheticMasterKey('p'), syntheticMasterKey('q')
	t.Setenv(secretKeyEnv, oldKey)
	t.Setenv(previousSecretKeyEnv, "")
	store := openGatewayMySQLCredentialE2EStore(t)
	seed := seedMySQLRotationPayloads(t, store)
	markSecretKeyVerified(t, store)
	t.Setenv(secretKeyEnv, newKey)
	t.Setenv(previousSecretKeyEnv, oldKey)

	target := seed.credentials[0]
	concurrentPayload := "{\"server\":\"concurrent.example.com\",\"password\":\"" + rotationCanary + "-mysql-concurrent\"}"
	// The whole credential pass fits in one batch, so the row the hook rewrites
	// is always part of the batch snapshot that the CAS compares against.
	opts := SecretRotationOptions{BatchSize: 3, BeforeBatchRewrite: func(batch int) error {
		if batch != 1 {
			return nil
		}
		ciphertext, err := store.encryptSecretWithAAD(concurrentPayload,
			credentialPayloadAAD(target.credentialID, target.versionID, target.credentialType, target.digest))
		if err != nil {
			return err
		}
		_, err = store.db.Exec("UPDATE credential_versions SET payload_ciphertext = ? WHERE id = ?", ciphertext, target.versionID)
		return err
	}}
	report, err := store.RotateSecretKey(context.Background(), opts)
	if err != nil {
		t.Fatalf("RotateSecretKey with a concurrent write: %v", err)
	}
	requireRotationReport(t, report, SecretRotationReport{CredentialVersions: 2, MachineLoginVault: 2, Skipped: 1})
	got, err := store.ResolveCredentialPayload(target.credentialID, target.versionID)
	if err != nil {
		t.Fatalf("resolve concurrent credential: %v", err)
	}
	if !bytes.Equal(got, []byte(concurrentPayload)) {
		t.Fatalf("rotation overwrote the concurrent write: got %q want %q", string(got), concurrentPayload)
	}
	t.Logf("EVIDENCE concurrent report=%+v payload_kept=%t", report, bytes.Equal(got, []byte(concurrentPayload)))
}
