package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rotationCanary marks payloads written by these tests. It must never appear in
// a rotation report.
const rotationCanary = "{{SECRET_jf6i4m2x}}"

var errRotationInjected = errors.New("injected rotation crash")

type rotationRow struct {
	credentialID   string
	versionID      string
	credentialType CredentialType
	digest         string
	payload        string
	assetID        string
	username       string
	password       string
	legacy         bool
}

type rotationSeed struct {
	credentials []rotationRow
	legacy      rotationRow
	vaults      []rotationRow
}

func openRotationStore(t *testing.T, path string) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(path)
	if err != nil {
		t.Fatalf("open gateway store: %v", err)
	}
	return store
}

// seedRotationPayloads writes the three stored shapes the rotation must handle:
// ordinary credential versions bound by AAD, one legacy v1 envelope without an
// AAD, and two machine-login vault rows bound to different assets and usernames.
func seedRotationPayloads(t *testing.T, store *GatewayStore) rotationSeed {
	t.Helper()
	owner := createCredentialTestUser(t, store, "rotation-owner")
	var seed rotationSeed
	for i, name := range []string{"rotation-registry", "rotation-database"} {
		resource, err := store.CreateCredential(CredentialCreateRequest{
			Name: name, Scope: CredentialScopePersonal, Type: CredentialTypeRegistry,
			OwnerID: owner.ID, CreatedBy: owner.ID,
		})
		if err != nil {
			t.Fatalf("create credential: %v", err)
		}
		payload := "{\"server\":\"" + name + ".example.com\",\"username\":\"owner\",\"password\":\"" + rotationCanary + "-" + strconv.Itoa(i) + "\"}"
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
	legacyPayload := "{\"server\":\"legacy.example.com\",\"password\":\"" + rotationCanary + "-legacy\"}"
	ciphertext, err := store.encryptSecret(legacyPayload)
	if err != nil {
		t.Fatalf("encrypt legacy payload: %v", err)
	}
	now := formatTime(time.Now().UTC())
	if _, err := store.db.Exec(`INSERT INTO credentials(id,name,scope,owner_id,type,state,revoked_at,created_by,created_at,updated_at)
		VALUES(?,?,?,?,?, 'active','',?,?,?)`, legacyID, "rotation-legacy", CredentialScopePersonal, owner.ID, CredentialTypeRegistry, owner.ID, now, now); err != nil {
		t.Fatalf("insert legacy credential: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO credential_versions(id,credential_id,payload_ciphertext,payload_digest,state,revoked_at,created_by,created_at,promoted_at)
		VALUES(?,?,?,?, 'active','',?,?,?)`, legacyVersionID, legacyID, ciphertext, credentialPayloadDigest([]byte(legacyPayload)), owner.ID, now, now); err != nil {
		t.Fatalf("insert legacy version: %v", err)
	}
	seed.legacy = rotationRow{
		credentialID: legacyID, versionID: legacyVersionID, credentialType: CredentialTypeRegistry,
		payload: legacyPayload, legacy: true,
	}

	// The vault needs the inventory schema, which the Gateway itself never
	// creates at startup.
	migration, err := os.ReadFile("../../../docs/migrations/machine_inventory.sql")
	if err != nil {
		t.Fatalf("read inventory migration: %v", err)
	}
	if _, err := store.db.Exec(string(migration)); err != nil {
		t.Fatalf("apply inventory migration: %v", err)
	}
	assets := []string{"asset-alpha", "asset-beta"}
	for i, asset := range assets {
		if _, err := store.db.Exec(`INSERT INTO inventory_machines(asset_id,name,kind,address) VALUES(?,?, 'physical', ?)`,
			asset, asset, "10.0.0."+strconv.Itoa(i+1)); err != nil {
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
			password: rotationCanary + "-vault-" + strconv.Itoa(i),
		}
		if err := store.storeMachineLogin(tx, row.assetID, row.username, row.password, TokenAuth{UserID: owner.ID}); err != nil {
			t.Fatalf("store machine login: %v", err)
		}
		if err := tx.QueryRow(`SELECT credential_id, version_id FROM inventory_machine_login_accounts
			WHERE asset_id = ? AND username = ?`, row.assetID, row.username).Scan(&row.credentialID, &row.versionID); err != nil {
			t.Fatalf("read vault binding: %v", err)
		}
		if err := tx.QueryRow(`SELECT payload_digest FROM credential_versions WHERE id = ?`, row.versionID).Scan(&row.digest); err != nil {
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

// startRotationWindow writes the seed with the old key and then opens the
// two-key window, exactly as an operator would before running the rotation.
func startRotationWindow(t *testing.T, oldKey, newKey string) (*GatewayStore, string, rotationSeed) {
	t.Helper()
	t.Setenv(secretKeyEnv, oldKey)
	t.Setenv(previousSecretKeyEnv, "")
	path := filepath.Join(t.TempDir(), "gateway.db")
	store := openRotationStore(t, path)
	seed := seedRotationPayloads(t, store)
	markSecretKeyVerified(t, store)
	t.Setenv(secretKeyEnv, newKey)
	t.Setenv(previousSecretKeyEnv, oldKey)
	return store, path, seed
}

func rotationKeyMAC(t *testing.T, key string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return secretKeyMACValue(sum[:])
}

func storedSecretKeyMAC(t *testing.T, store *GatewayStore) string {
	t.Helper()
	var mac string
	if err := store.db.QueryRow(`SELECT key_mac FROM gateway_secret_key_check WHERE singleton = 1`).Scan(&mac); err != nil {
		t.Fatalf("read key marker: %v", err)
	}
	return mac
}

func requireVaultMarker(t *testing.T, store *GatewayStore) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin vault marker check: %v", err)
	}
	defer tx.Rollback()
	if err := store.checkMachineLoginKey(tx, false); err != nil {
		t.Fatalf("machine login vault marker: %v", err)
	}
}

func requireSeedReadable(t *testing.T, store *GatewayStore, seed rotationSeed) {
	t.Helper()
	for _, row := range seed.credentials {
		got, err := store.ResolveCredentialPayload(row.credentialID, row.versionID)
		if err != nil {
			t.Fatalf("resolve credential %s: %v", row.credentialID, err)
		}
		if !bytes.Equal(got, []byte(row.payload)) {
			t.Fatalf("credential payload changed: got %q want %q", string(got), row.payload)
		}
	}
	var ciphertext string
	if err := store.db.QueryRow(`SELECT payload_ciphertext FROM credential_versions WHERE id = ?`, seed.legacy.versionID).Scan(&ciphertext); err != nil {
		t.Fatalf("read legacy ciphertext: %v", err)
	}
	got, err := store.decryptSecret(ciphertext)
	if err != nil {
		t.Fatalf("decrypt legacy payload: %v", err)
	}
	if !bytes.Equal([]byte(got), []byte(seed.legacy.payload)) {
		t.Fatalf("legacy payload changed: got %q want %q", got, seed.legacy.payload)
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatalf("begin vault read: %v", err)
	}
	defer tx.Rollback()
	for _, row := range seed.vaults {
		ok, err := store.matchMachineLogin(tx, row.assetID, row.username, row.password)
		if err != nil {
			t.Fatalf("match machine login %s/%s: %v", row.assetID, row.username, err)
		}
		if !ok {
			t.Fatalf("machine login %s/%s not readable", row.assetID, row.username)
		}
	}
}

func snapshotCiphertexts(t *testing.T, store *GatewayStore) map[string]string {
	t.Helper()
	rows, err := store.db.Query(`SELECT id, payload_ciphertext FROM credential_versions`)
	if err != nil {
		t.Fatalf("snapshot ciphertexts: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, ciphertext string
		if err := rows.Scan(&id, &ciphertext); err != nil {
			t.Fatalf("scan ciphertext: %v", err)
		}
		out[id] = ciphertext
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate ciphertexts: %v", err)
	}
	return out
}

func requireRotationReport(t *testing.T, got SecretRotationReport, want SecretRotationReport) {
	t.Helper()
	if got != want {
		t.Fatalf("rotation report = %+v, want %+v", got, want)
	}
}

func TestRotateSecretKeyWindowThenRestart(t *testing.T) {
	oldKey, newKey := syntheticMasterKey('o'), syntheticMasterKey('n')
	store, path, seed := startRotationWindow(t, oldKey, newKey)

	state, err := store.VerifySecretKey()
	if err != nil || state != GatewaySecretKeyRotationPending {
		t.Fatalf("VerifySecretKey in window = %v, %v, want rotation pending", state, err)
	}
	report, err := store.RotateSecretKey(context.Background(), SecretRotationOptions{BatchSize: 2})
	if err != nil {
		t.Fatalf("RotateSecretKey: %v", err)
	}
	requireRotationReport(t, report, SecretRotationReport{CredentialVersions: 3, MachineLoginVault: 2})
	if mac := storedSecretKeyMAC(t, store); mac != rotationKeyMAC(t, newKey) {
		t.Fatalf("key marker does not name the primary key")
	}
	requireVaultMarker(t, store)
	requireSeedReadable(t, store, seed)
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Only the new key: the rotation is complete and every payload reads.
	t.Setenv(previousSecretKeyEnv, "")
	store = openRotationStore(t, path)
	markSecretKeyVerified(t, store)
	requireSeedReadable(t, store, seed)
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Only the old key: the marker no longer matches, so startup fails closed
	// instead of quietly falling back to the retired key.
	t.Setenv(secretKeyEnv, oldKey)
	store = openRotationStore(t, path)
	defer store.Close()
	if state, err := store.VerifySecretKey(); !errors.Is(err, ErrSecretKeyMismatch) {
		t.Fatalf("VerifySecretKey with retired key = %v, %v, want ErrSecretKeyMismatch", state, err)
	}
}

func TestRotateSecretKeyIsIdempotent(t *testing.T) {
	store, _, seed := startRotationWindow(t, syntheticMasterKey('o'), syntheticMasterKey('n'))
	defer store.Close()
	if _, err := store.RotateSecretKey(context.Background(), SecretRotationOptions{}); err != nil {
		t.Fatalf("first RotateSecretKey: %v", err)
	}
	before := snapshotCiphertexts(t, store)
	report, err := store.RotateSecretKey(context.Background(), SecretRotationOptions{})
	if err != nil {
		t.Fatalf("second RotateSecretKey: %v", err)
	}
	requireRotationReport(t, report, SecretRotationReport{AlreadyPrimary: 5})
	after := snapshotCiphertexts(t, store)
	if len(before) != 5 || len(after) != 5 {
		t.Fatalf("expected 5 stored payloads, got %d then %d", len(before), len(after))
	}
	for id, ciphertext := range before {
		if after[id] != ciphertext {
			t.Fatalf("rerun rewrote an already rotated payload")
		}
	}
	requireSeedReadable(t, store, seed)
}

func TestRotateSecretKeyResumesAfterBatchCrash(t *testing.T) {
	oldKey, newKey := syntheticMasterKey('o'), syntheticMasterKey('n')
	store, _, seed := startRotationWindow(t, oldKey, newKey)
	defer store.Close()

	report, err := store.RotateSecretKey(context.Background(), SecretRotationOptions{
		BatchSize: 1,
		AfterBatchCommit: func(batch int) error {
			if batch == 1 {
				return errRotationInjected
			}
			return nil
		},
	})
	if !errors.Is(err, errRotationInjected) {
		t.Fatalf("interrupted rotation error = %v, want injected error", err)
	}
	if report.CredentialVersions != 1 || report.MachineLoginVault != 0 {
		t.Fatalf("interrupted rotation report = %+v, want one committed credential batch", report)
	}
	if mac := storedSecretKeyMAC(t, store); mac != rotationKeyMAC(t, oldKey) {
		t.Fatalf("marker moved before every payload was rotated")
	}
	if state, err := store.VerifySecretKey(); err != nil || state != GatewaySecretKeyRotationPending {
		t.Fatalf("VerifySecretKey after crash = %v, %v, want rotation pending", state, err)
	}
	pending, err := store.countSecretPayloadsRequiringPrevious(context.Background(), false)
	if err != nil {
		t.Fatalf("count pending payloads: %v", err)
	}
	if pending != 4 {
		t.Fatalf("payloads still requiring the previous key = %d, want 4", pending)
	}
	requireSeedReadable(t, store, seed)

	report, err = store.RotateSecretKey(context.Background(), SecretRotationOptions{BatchSize: 2})
	if err != nil {
		t.Fatalf("resumed RotateSecretKey: %v", err)
	}
	if report.Skipped != 0 {
		t.Fatalf("resumed rotation skipped %d payloads", report.Skipped)
	}
	if mac := storedSecretKeyMAC(t, store); mac != rotationKeyMAC(t, newKey) {
		t.Fatalf("resumed rotation did not move the marker to the primary key")
	}
	if state, err := store.VerifySecretKey(); err != nil || state != GatewaySecretKeyReady {
		t.Fatalf("VerifySecretKey after resume = %v, %v, want ready", state, err)
	}
}

func TestRotateSecretKeyRecoversWhenOnlyMarkerUpdateFailed(t *testing.T) {
	oldKey, newKey := syntheticMasterKey('o'), syntheticMasterKey('n')
	store, _, seed := startRotationWindow(t, oldKey, newKey)
	defer store.Close()

	report, err := store.RotateSecretKey(context.Background(), SecretRotationOptions{
		BeforeMarkerCommit: func() error { return errRotationInjected },
	})
	if !errors.Is(err, errRotationInjected) {
		t.Fatalf("rotation error = %v, want injected error", err)
	}
	requireRotationReport(t, report, SecretRotationReport{CredentialVersions: 3, MachineLoginVault: 2})
	if mac := storedSecretKeyMAC(t, store); mac != rotationKeyMAC(t, oldKey) {
		t.Fatalf("marker moved despite the injected failure")
	}
	pending, err := store.countSecretPayloadsRequiringPrevious(context.Background(), false)
	if err != nil || pending != 0 {
		t.Fatalf("payloads requiring the previous key = %d, %v, want 0", pending, err)
	}

	report, err = store.RotateSecretKey(context.Background(), SecretRotationOptions{})
	if err != nil {
		t.Fatalf("recovery RotateSecretKey: %v", err)
	}
	requireRotationReport(t, report, SecretRotationReport{AlreadyPrimary: 5})
	if mac := storedSecretKeyMAC(t, store); mac != rotationKeyMAC(t, newKey) {
		t.Fatalf("recovery run did not move the marker to the primary key")
	}
	requireSeedReadable(t, store, seed)
}

func TestRotateSecretKeyPreservesPayloadBindings(t *testing.T) {
	store, _, seed := startRotationWindow(t, syntheticMasterKey('o'), syntheticMasterKey('n'))
	defer store.Close()
	if _, err := store.RotateSecretKey(context.Background(), SecretRotationOptions{}); err != nil {
		t.Fatalf("RotateSecretKey: %v", err)
	}
	row := seed.credentials[0]
	var ciphertext string
	if err := store.db.QueryRow(`SELECT payload_ciphertext FROM credential_versions WHERE id = ?`, row.versionID).Scan(&ciphertext); err != nil {
		t.Fatalf("read rotated ciphertext: %v", err)
	}
	if _, err := store.decryptSecretWithAAD(ciphertext, credentialPayloadAAD(row.credentialID, row.versionID, row.credentialType, "wrong-digest")); err == nil {
		t.Fatalf("credential payload opened with a wrong AAD")
	}
	vault := seed.vaults[0]
	if err := store.db.QueryRow(`SELECT payload_ciphertext FROM credential_versions WHERE id = ?`, vault.versionID).Scan(&ciphertext); err != nil {
		t.Fatalf("read rotated vault ciphertext: %v", err)
	}
	if _, err := store.decryptSecretWithAAD(ciphertext, nil); err == nil {
		t.Fatalf("vault payload opened without its AAD binding")
	}
	wrong := machineLoginAAD(vault.credentialID, vault.versionID, vault.digest, vault.assetID, "someone-else")
	if _, err := store.decryptSecretWithAAD(ciphertext, wrong); err == nil {
		t.Fatalf("vault payload opened with a wrong username binding")
	}
	if _, err := store.decryptSecretWithAAD(ciphertext, machineLoginAAD(vault.credentialID, vault.versionID, vault.digest, vault.assetID, vault.username)); err != nil {
		t.Fatalf("vault payload does not open with its real binding: %v", err)
	}
}

func TestRotateSecretKeyDoesNotOverwriteConcurrentWrite(t *testing.T) {
	store, _, seed := startRotationWindow(t, syntheticMasterKey('o'), syntheticMasterKey('n'))
	defer store.Close()
	row := seed.credentials[0]
	concurrent := "{\"server\":\"concurrent.example.com\",\"username\":\"other\",\"password\":\"" + rotationCanary + "-concurrent\"}"
	var injected bool
	report, err := store.RotateSecretKey(context.Background(), SecretRotationOptions{
		BeforeBatchRewrite: func(batch int) error {
			if batch != 1 || injected {
				return nil
			}
			injected = true
			ciphertext, err := store.encryptSecretWithAAD(concurrent,
				credentialPayloadAAD(row.credentialID, row.versionID, row.credentialType, row.digest))
			if err != nil {
				return err
			}
			_, err = store.db.Exec(`UPDATE credential_versions SET payload_ciphertext = ? WHERE id = ?`, ciphertext, row.versionID)
			return err
		},
	})
	if err != nil {
		t.Fatalf("RotateSecretKey: %v", err)
	}
	requireRotationReport(t, report, SecretRotationReport{CredentialVersions: 2, MachineLoginVault: 2, Skipped: 1})
	got, err := store.ResolveCredentialPayload(row.credentialID, row.versionID)
	if err != nil {
		t.Fatalf("resolve concurrently rewritten credential: %v", err)
	}
	if !bytes.Equal(got, []byte(concurrent)) {
		t.Fatalf("rotation overwrote a concurrent write: got %q", string(got))
	}
}

func TestSecretKeyRingRefusesEqualKeys(t *testing.T) {
	key := syntheticMasterKey('o')
	t.Setenv(secretKeyEnv, key)
	t.Setenv(previousSecretKeyEnv, key)
	store := openCredentialTestStore(t)
	if _, err := store.VerifySecretKey(); !errors.Is(err, ErrSecretKeyConflict) {
		t.Fatalf("VerifySecretKey with equal keys error = %v, want ErrSecretKeyConflict", err)
	}
	if _, err := store.RotateSecretKey(context.Background(), SecretRotationOptions{}); !errors.Is(err, ErrSecretKeyConflict) {
		t.Fatalf("RotateSecretKey with equal keys error = %v, want ErrSecretKeyConflict", err)
	}
	if !strings.Contains(ErrSecretKeyConflict.Error(), "gateway_secret_key_conflict") {
		t.Fatalf("conflict category missing from error text")
	}
}
