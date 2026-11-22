package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic canaries only. Neither may ever appear in an error, an HTTP body
// or a log line the key check emits.
const (
	secretKeyCanary        = "synthetic-master-key-canary"
	secretPayloadCanary    = "synthetic-credential-payload-canary-7f3a"
	wrongSecretKeyCanary   = "different-synthetic-master-key"
	secretKeyTestKeyLength = 32
)

func syntheticMasterKey(seed byte) string {
	return strings.Repeat(string(rune('a'+seed)), secretKeyTestKeyLength)
}

func markSecretKeyVerified(t *testing.T, store *GatewayStore) {
	t.Helper()
	state, err := store.VerifySecretKey()
	if err != nil || state != GatewaySecretKeyReady {
		t.Fatalf("VerifySecretKey = %v, %v", state, err)
	}
}

func secretKeyMarkerCount(t *testing.T, store *GatewayStore) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM gateway_secret_key_check`).Scan(&count); err != nil {
		t.Fatalf("count key marker: %v", err)
	}
	return count
}

// putSyntheticCredential writes one ordinary encrypted credential version and
// returns its resource.
func putSyntheticCredential(t *testing.T, store *GatewayStore) CredentialResource {
	t.Helper()
	owner := createCredentialTestUser(t, store, "key-check-owner")
	resource, err := store.CreateCredential(CredentialCreateRequest{
		Name: "key-check-registry", Scope: CredentialScopePersonal, Type: CredentialTypeRegistry,
		OwnerID: owner.ID, CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	if _, err := store.PutCredentialVersion(CredentialVersionPutRequest{
		CredentialID: resource.ID, CreatedBy: owner.ID, Activate: true,
		Payload: json.RawMessage(`{"server":"registry.example.com","username":"owner","password":"` + secretPayloadCanary + `"}`),
	}); err != nil {
		t.Fatalf("put credential version: %v", err)
	}
	return resource
}

func TestSecretKeyEmptyDatabaseStartup(t *testing.T) {
	t.Run("without key allows startup and writes nothing", func(t *testing.T) {
		t.Setenv("DOOPS_GATEWAY_SECRET_KEY", "")
		store := openCredentialTestStore(t)
		state, err := store.VerifySecretKey()
		if err != nil || state != GatewaySecretKeyAbsent {
			t.Fatalf("VerifySecretKey = %v, %v; want absent without error", state, err)
		}
		if got := secretKeyMarkerCount(t, store); got != 0 {
			t.Fatalf("marker rows = %d, want 0", got)
		}
		// Credential features stay unavailable rather than silently working.
		owner := createCredentialTestUser(t, store, "owner")
		resource, err := store.CreateCredential(CredentialCreateRequest{
			Name: "no-key", Scope: CredentialScopePersonal, Type: CredentialTypeRegistry,
			OwnerID: owner.ID, CreatedBy: owner.ID,
		})
		if err != nil {
			t.Fatalf("create credential without key: %v", err)
		}
		_, err = store.PutCredentialVersion(CredentialVersionPutRequest{
			CredentialID: resource.ID, CreatedBy: owner.ID,
			Payload: json.RawMessage(`{"server":"registry.example.com","username":"owner","password":"` + secretPayloadCanary + `"}`),
		})
		if !errors.Is(err, ErrSecretKeyUnavailable) {
			t.Fatalf("put without key error = %v, want ErrSecretKeyUnavailable", err)
		}
	})

	t.Run("with key writes the marker", func(t *testing.T) {
		key := syntheticMasterKey(1)
		t.Setenv("DOOPS_GATEWAY_SECRET_KEY", key)
		store := openCredentialTestStore(t)
		markSecretKeyVerified(t, store)
		if got := secretKeyMarkerCount(t, store); got != 1 {
			t.Fatalf("marker rows = %d, want 1", got)
		}
		var stored string
		if err := store.db.QueryRow(`SELECT key_mac FROM gateway_secret_key_check WHERE singleton=1`).Scan(&stored); err != nil {
			t.Fatalf("read marker: %v", err)
		}
		if stored == "" || strings.Contains(stored, key) || strings.Contains(stored, secretKeyCanary) {
			t.Fatal("marker stores key material instead of a MAC")
		}
		markSecretKeyVerified(t, store)
		if got := secretKeyMarkerCount(t, store); got != 1 {
			t.Fatalf("second startup marker rows = %d, want 1", got)
		}
	})
}

func TestSecretKeyMarkerMatchMismatchAndMissing(t *testing.T) {
	key := syntheticMasterKey(2)
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", key)
	store := openCredentialTestStore(t)
	putSyntheticCredential(t, store)
	markSecretKeyVerified(t, store)

	if _, err := store.VerifySecretKey(); err != nil {
		t.Fatalf("same key startup: %v", err)
	}

	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", wrongSecretKeyCanary)
	state, err := store.VerifySecretKey()
	if !errors.Is(err, ErrSecretKeyMismatch) || state != GatewaySecretKeyAbsent {
		t.Fatalf("wrong key startup = %v, %v; want ErrSecretKeyMismatch", state, err)
	}
	if strings.Contains(err.Error(), wrongSecretKeyCanary) || strings.Contains(err.Error(), secretPayloadCanary) {
		t.Fatalf("key check error leaked material: %v", err)
	}

	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", "")
	if _, err := store.VerifySecretKey(); !errors.Is(err, ErrSecretKeyUnavailable) {
		t.Fatalf("missing key with marker error = %v, want ErrSecretKeyUnavailable", err)
	}

	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", key)
	if _, err := store.db.Exec(`DELETE FROM gateway_secret_key_check`); err != nil {
		t.Fatalf("drop marker: %v", err)
	}
	// Marker missing: the stored ciphertext must prove the key before it is re-recorded.
	markSecretKeyVerified(t, store)
	if got := secretKeyMarkerCount(t, store); got != 1 {
		t.Fatalf("marker rows after upgrade = %d, want 1", got)
	}
}

func TestSecretKeyUpgradeWithoutMarker(t *testing.T) {
	writeKey := syntheticMasterKey(3)
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", writeKey)
	databasePath := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenGatewayStore(databasePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	putSyntheticCredential(t, store)
	// Simulate a database written before the key check existed.
	if _, err := store.db.Exec(`DROP TABLE gateway_secret_key_check`); err != nil {
		t.Fatalf("drop marker table: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	t.Run("wrong key refuses and writes nothing", func(t *testing.T) {
		t.Setenv("DOOPS_GATEWAY_SECRET_KEY", wrongSecretKeyCanary)
		reopened, err := OpenGatewayStore(databasePath)
		if err != nil {
			t.Fatalf("reopen store: %v", err)
		}
		defer reopened.Close()
		if _, err := reopened.VerifySecretKey(); !errors.Is(err, ErrSecretKeyMismatch) {
			t.Fatalf("wrong key upgrade error = %v, want ErrSecretKeyMismatch", err)
		}
		if got := secretKeyMarkerCount(t, reopened); got != 0 {
			t.Fatalf("failed upgrade marker rows = %d, want 0", got)
		}
	})

	t.Run("missing key refuses", func(t *testing.T) {
		t.Setenv("DOOPS_GATEWAY_SECRET_KEY", "")
		reopened, err := OpenGatewayStore(databasePath)
		if err != nil {
			t.Fatalf("reopen store: %v", err)
		}
		defer reopened.Close()
		if _, err := reopened.VerifySecretKey(); !errors.Is(err, ErrSecretKeyUnavailable) {
			t.Fatalf("missing key upgrade error = %v, want ErrSecretKeyUnavailable", err)
		}
		if got := secretKeyMarkerCount(t, reopened); got != 0 {
			t.Fatalf("failed upgrade marker rows = %d, want 0", got)
		}
	})

	t.Run("original key records the marker", func(t *testing.T) {
		t.Setenv("DOOPS_GATEWAY_SECRET_KEY", writeKey)
		reopened, err := OpenGatewayStore(databasePath)
		if err != nil {
			t.Fatalf("reopen store: %v", err)
		}
		defer reopened.Close()
		markSecretKeyVerified(t, reopened)
		if got := secretKeyMarkerCount(t, reopened); got != 1 {
			t.Fatalf("marker rows after upgrade = %d, want 1", got)
		}
		var count int
		if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM credential_versions WHERE payload_ciphertext <> ''`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("upgrade changed stored versions: %d, %v", count, err)
		}
	})
}

func TestSecretKeyMachineLoginOnlyUpgrade(t *testing.T) {
	for _, scenario := range []struct {
		name string
		key  string
		want error
	}{
		{name: "wrong key", key: wrongSecretKeyCanary, want: ErrSecretKeyMismatch},
		{name: "missing key", key: "", want: ErrSecretKeyUnavailable},
		{name: "original key", key: syntheticMasterKey(4)},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Setenv("DOOPS_GATEWAY_SECRET_KEY", syntheticMasterKey(4))
			databasePath := filepath.Join(t.TempDir(), "gateway.db")
			store, err := OpenGatewayStore(databasePath)
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			// Only the machine-login vault holds ciphertext; it carries its own
			// key marker, and the gateway marker is simulated as absent.
			if _, err := store.db.Exec(`CREATE TABLE inventory_machine_login_key (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    key_mac TEXT NOT NULL
) STRICT`); err != nil {
				t.Fatalf("create vault key table: %v", err)
			}
			vaultKey, err := store.machineLoginMAC("vault-key", "v1")
			if err != nil {
				t.Fatalf("vault key MAC: %v", err)
			}
			if _, err := store.db.Exec(`INSERT INTO inventory_machine_login_key(singleton,key_mac) VALUES(1,?)`, vaultKey); err != nil {
				t.Fatalf("initialize vault key: %v", err)
			}
			owner := createCredentialTestUser(t, store, "owner")
			if _, err := store.db.Exec(`INSERT INTO credentials(id,name,scope,owner_id,type,state,created_by,created_at,updated_at)
 VALUES('cred_vault','cred_vault','machine_login',?, 'opaque','active',?,?,'2026-01-01T00:00:00Z')`, owner.ID, owner.ID, "2026-01-01T00:00:00Z"); err != nil {
				t.Fatalf("insert vault credential: %v", err)
			}
			if _, err := store.db.Exec(`INSERT INTO credential_versions(id,credential_id,payload_ciphertext,payload_digest,state,created_by,created_at,promoted_at)
 VALUES('credver_vault','cred_vault','v2:opaque-account-bound','digest','active',?,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, owner.ID); err != nil {
				t.Fatalf("insert vault version: %v", err)
			}
			if _, err := store.db.Exec(`DROP TABLE gateway_secret_key_check`); err != nil {
				t.Fatalf("drop marker table: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatalf("close store: %v", err)
			}

			t.Setenv("DOOPS_GATEWAY_SECRET_KEY", scenario.key)
			reopened, err := OpenGatewayStore(databasePath)
			if err != nil {
				t.Fatalf("reopen store: %v", err)
			}
			defer reopened.Close()
			if _, err := reopened.VerifySecretKey(); !errors.Is(err, scenario.want) {
				t.Fatalf("machine-login-only upgrade error = %v, want %v", err, scenario.want)
			}
			wantMarkers := 0
			if scenario.want == nil {
				wantMarkers = 1
			}
			if got := secretKeyMarkerCount(t, reopened); got != wantMarkers {
				t.Fatalf("machine-login-only marker rows = %d, want %d", got, wantMarkers)
			}
		})
	}
}

func TestSecretKeyTamperedCiphertext(t *testing.T) {
	key := syntheticMasterKey(5)
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", key)
	store := openCredentialTestStore(t)
	resource := putSyntheticCredential(t, store)
	markSecretKeyVerified(t, store)

	var ciphertext string
	if err := store.db.QueryRow(`SELECT payload_ciphertext FROM credential_versions WHERE credential_id=?`, resource.ID).Scan(&ciphertext); err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(ciphertext, "v2:"))
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	raw[len(raw)/2] ^= 0x01
	tampered := "v2:" + base64.RawStdEncoding.EncodeToString(raw)
	if _, err := store.db.Exec(`UPDATE credential_versions SET payload_ciphertext=? WHERE credential_id=?`, tampered, resource.ID); err != nil {
		t.Fatalf("tamper ciphertext: %v", err)
	}
	// The marker still matches, so startup stays up; the affected row fails closed.
	if _, err := store.VerifySecretKey(); err != nil {
		t.Fatalf("startup with tampered row: %v", err)
	}
	version, err := store.ActiveCredentialVersion(resource.ID)
	if err != nil {
		t.Fatalf("active version: %v", err)
	}
	_, err = store.ResolveCredentialPayload(resource.ID, version.ID)
	if !errors.Is(err, ErrCredentialCiphertextInvalid) {
		t.Fatalf("tampered ciphertext error = %v, want ErrCredentialCiphertextInvalid", err)
	}
	if strings.Contains(err.Error(), secretPayloadCanary) || strings.Contains(err.Error(), tampered) {
		t.Fatalf("ciphertext error leaked material: %v", err)
	}
	if got := credentialOperationErrorCategory(err); got != "credential_ciphertext_invalid" {
		t.Fatalf("tampered ciphertext category = %q", got)
	}
	if got := credentialHTTPStatus(err); got != http.StatusBadRequest {
		t.Fatalf("tampered ciphertext status = %d", got)
	}
}

// flipBase64Tail changes the last raw-standard-base64 character, which alters
// the authentication tag and guarantees a GCM open failure.
func flipBase64Tail(tail string) string {
	last := tail[len(tail)-1]
	if last == 'A' {
		return tail[:len(tail)-1] + "B"
	}
	return tail[:len(tail)-1] + "A"
}

func TestSecretKeyMismatchCategoryAndStatus(t *testing.T) {
	if got := credentialErrorCategory(ErrSecretKeyMismatch); got != "credential_key_mismatch" {
		t.Fatalf("category = %q", got)
	}
	if got := credentialHTTPStatus(ErrSecretKeyMismatch); got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", got)
	}
	if got := credentialOperationErrorCategory(ErrSecretKeyMismatch); got != "credential_key_mismatch" {
		t.Fatalf("operation category = %q", got)
	}
	if got := credentialOperationErrorCategory(ErrCredentialCiphertextInvalid); got != "credential_ciphertext_invalid" {
		t.Fatalf("ciphertext operation category = %q", got)
	}
	if got := credentialHTTPStatus(ErrSecretKeyUnavailable); got != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d, want 503", got)
	}

	// A changed key no longer matches the marker written at startup. Runtime
	// env changes are not the normal path; this proves the guard that the
	// Gateway runs before accepting traffic.
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", syntheticMasterKey(6))
	store := openCredentialTestStore(t)
	putSyntheticCredential(t, store)
	markSecretKeyVerified(t, store)
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", wrongSecretKeyCanary)
	_, err := store.VerifySecretKey()
	if !errors.Is(err, ErrSecretKeyMismatch) {
		t.Fatalf("VerifySecretKey after key change = %v, want ErrSecretKeyMismatch", err)
	}
	if strings.Contains(err.Error(), wrongSecretKeyCanary) || strings.Contains(err.Error(), secretPayloadCanary) {
		t.Fatalf("key mismatch error leaked material: %v", err)
	}
}

func TestSecretKeyCheckNeverLogsKeyOrPayload(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", syntheticMasterKey(7))
	store := openCredentialTestStore(t)
	resource := putSyntheticCredential(t, store)
	markSecretKeyVerified(t, store)

	var errorsText strings.Builder
	for _, key := range []string{wrongSecretKeyCanary, "", syntheticMasterKey(7)} {
		t.Setenv("DOOPS_GATEWAY_SECRET_KEY", key)
		state, err := store.VerifySecretKey()
		if err != nil {
			errorsText.WriteString(err.Error())
		}
		log.Printf("startup state=%d err=%v", state, err)
	}
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", wrongSecretKeyCanary)
	// The gateway entrypoint renders the same sentinel for its operator message.
	if !strings.Contains(ErrSecretKeyMismatch.Error(), "does not match") {
		t.Fatal("mismatch sentinel lost its stable description")
	}
	log.Printf("startup mismatch: %s", ErrSecretKeyMismatch)
	_, err := store.ResolveCredentialPayload(resource.ID, "")
	errorsText.WriteString(err.Error())
	log.Printf("resolve err=%v", err)

	combined := logs.String() + errorsText.String()
	for _, canary := range []string{syntheticMasterKey(7), wrongSecretKeyCanary, secretPayloadCanary} {
		if strings.Contains(combined, canary) {
			t.Fatal("key check output contained key material or a payload value")
		}
	}
}
