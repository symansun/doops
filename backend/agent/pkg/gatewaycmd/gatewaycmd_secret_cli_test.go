package gatewaycmd

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/doops/agent/internal/server"
)

const (
	secretKeyTestEnvPrimary  = "DOOPS_GATEWAY_SECRET_KEY"
	secretKeyTestEnvPrevious = "DOOPS_GATEWAY_SECRET_KEY_PREVIOUS"
	secretKeyTestOldKey      = "int-63-old-master-key-3f9c"
	secretKeyTestNewKey      = "int-63-new-master-key-8a1d"
)

var secretKeyTestInventoryDDL = []string{
	`CREATE TABLE IF NOT EXISTS inventory_machines (
		asset_id TEXT PRIMARY KEY NOT NULL,
		name TEXT NOT NULL,
		kind TEXT NOT NULL,
		address TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS inventory_machine_login_key (
		singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
		key_mac TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS inventory_machine_login_accounts (
		asset_id TEXT NOT NULL,
		username TEXT NOT NULL,
		credential_id TEXT NOT NULL,
		version_id TEXT NOT NULL,
		PRIMARY KEY (asset_id, username)
	)`,
}

// secretKeyTestSeed describes a database written by the old key so the CLI can
// be exercised against the same rows a production rotation would find.
type secretKeyTestSeed struct {
	credentialID    string
	payloads        []string
	v2VersionIDs    []string
	v1VersionID     string
	v1Ciphertext    string
	vaultVersionIDs []string
	vaultCiphertext []string
}

func TestSecretKeyCLI(t *testing.T) {
	binary := buildGatewayCLI(t)

	t.Run("rotate rewrites every payload and keeps the keys out of output", func(t *testing.T) {
		t.Setenv(secretKeyTestEnvPrimary, secretKeyTestOldKey)
		t.Setenv(secretKeyTestEnvPrevious, "")
		dbPath := filepath.Join(t.TempDir(), "gateway.db")
		seed := seedSecretKeyTestStore(t, dbPath)
		before := secretKeyTestCiphertexts(t, dbPath)

		run := runSecretKeyTestCLI(t, binary, map[string]string{
			secretKeyTestEnvPrimary:  secretKeyTestNewKey,
			secretKeyTestEnvPrevious: secretKeyTestOldKey,
		}, "secret-key", "rotate", "-db", dbPath)
		if run.exitCode != 0 {
			t.Fatalf("rotate exit = %d, want 0\nstdout: %s\nstderr: %s", run.exitCode, run.stdout, run.stderr)
		}
		want := "secret-key rotate: credential_versions=3 machine_login_vault=2 skipped=0 already_primary=0\n"
		if !strings.Contains(run.stdout, want) {
			t.Fatalf("rotate stdout = %q, want line %q", run.stdout, want)
		}
		if !strings.Contains(run.stdout, "secret-key marker: rotated\n") {
			t.Fatalf("rotate stdout = %q, want the rotated marker", run.stdout)
		}
		t.Logf("rotate exit=%d stdout=%q", run.exitCode, run.stdout)
		assertSecretKeyTestNoLeak(t, run, seed, before)

		after := secretKeyTestCiphertexts(t, dbPath)
		if len(after) != 5 {
			t.Fatalf("stored ciphertext rows = %d, want 5", len(after))
		}
		for id, value := range before {
			if after[id] == value {
				t.Fatalf("version %s ciphertext did not change during rotation", id)
			}
		}
		if gateway, vault := secretKeyTestMarkerValues(t, dbPath); gateway != secretKeyTestGatewayMAC(t, secretKeyTestNewKey) || vault != secretKeyTestVaultMAC(t, secretKeyTestNewKey) {
			t.Fatalf("markers after rotate: gateway=%q vault=%q, want both derived from the new key", gateway, vault)
		}

		// Only the new key: the marker matches and every payload opens.
		t.Setenv(secretKeyTestEnvPrimary, secretKeyTestNewKey)
		t.Setenv(secretKeyTestEnvPrevious, "")
		fresh := openSecretKeyTestStore(t, dbPath)
		if state, err := fresh.VerifySecretKey(); err != nil || state != server.GatewaySecretKeyReady {
			t.Fatalf("VerifySecretKey with the new key = (%v, %v), want Ready", state, err)
		}
		for i, versionID := range seed.v2VersionIDs {
			payload, err := fresh.ResolveCredentialPayload(seed.credentialID, versionID)
			if err != nil {
				t.Fatalf("resolve version %s with the new key: %v", versionID, err)
			}
			if string(payload) != seed.payloads[i] {
				t.Fatalf("version %s payload = %q, want %q", versionID, payload, seed.payloads[i])
			}
		}
		if err := fresh.Close(); err != nil {
			t.Fatalf("close new-key store: %v", err)
		}

		// Only the new key, dry run: every credential and vault row is already
		// primary, including the historical v1 row that ResolveCredentialPayload
		// cannot read by design.
		run = runSecretKeyTestCLI(t, binary, map[string]string{
			secretKeyTestEnvPrimary: secretKeyTestNewKey,
		}, "secret-key", "rotate", "-db", dbPath, "-dry-run")
		if run.exitCode != 0 {
			t.Fatalf("new-key dry-run exit = %d, want 0\nstdout: %s\nstderr: %s", run.exitCode, run.stdout, run.stderr)
		}
		want = "secret-key rotate: dry_run=true credential_versions=0 machine_login_vault=0 skipped=0 already_primary=5\n"
		if !strings.Contains(run.stdout, want) {
			t.Fatalf("new-key dry-run stdout = %q, want line %q", run.stdout, want)
		}

		// Only the old key: the marker is the new key, so the store must refuse.
		t.Setenv(secretKeyTestEnvPrimary, secretKeyTestOldKey)
		t.Setenv(secretKeyTestEnvPrevious, "")
		oldStore := openSecretKeyTestStore(t, dbPath)
		if _, err := oldStore.VerifySecretKey(); !errors.Is(err, server.ErrSecretKeyMismatch) {
			t.Fatalf("VerifySecretKey with only the old key = %v, want ErrSecretKeyMismatch", err)
		}
		if err := oldStore.Close(); err != nil {
			t.Fatalf("close old-key store: %v", err)
		}

		// A second rotation rewrites nothing: all five rows stay byte-identical.
		stable := secretKeyTestCiphertexts(t, dbPath)
		run = runSecretKeyTestCLI(t, binary, map[string]string{
			secretKeyTestEnvPrimary:  secretKeyTestNewKey,
			secretKeyTestEnvPrevious: secretKeyTestOldKey,
		}, "secret-key", "rotate", "-db", dbPath)
		if run.exitCode != 0 {
			t.Fatalf("second rotate exit = %d, want 0\nstdout: %s\nstderr: %s", run.exitCode, run.stdout, run.stderr)
		}
		want = "secret-key rotate: credential_versions=0 machine_login_vault=0 skipped=0 already_primary=5\n"
		if !strings.Contains(run.stdout, want) {
			t.Fatalf("second rotate stdout = %q, want line %q", run.stdout, want)
		}
		if again := secretKeyTestCiphertexts(t, dbPath); !secretKeyTestSameCiphertexts(stable, again) {
			t.Fatalf("second rotate changed ciphertexts\nbefore: %v\nafter:  %v", stable, again)
		}
	})

	t.Run("dry-run counts pending rows and writes nothing", func(t *testing.T) {
		t.Setenv(secretKeyTestEnvPrimary, secretKeyTestOldKey)
		t.Setenv(secretKeyTestEnvPrevious, "")
		dbPath := filepath.Join(t.TempDir(), "gateway.db")
		seed := seedSecretKeyTestStore(t, dbPath)
		if err := os.Remove(dbPath + "-wal"); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("remove wal: %v", err)
		}
		beforeBytes, err := os.ReadFile(dbPath)
		if err != nil {
			t.Fatalf("read database: %v", err)
		}
		beforeCiphertexts := secretKeyTestCiphertexts(t, dbPath)
		beforeVersions, beforeAccounts := secretKeyTestRowCounts(t, dbPath)
		beforeGateway, beforeVault := secretKeyTestMarkerValues(t, dbPath)

		run := runSecretKeyTestCLI(t, binary, map[string]string{
			secretKeyTestEnvPrimary:  secretKeyTestNewKey,
			secretKeyTestEnvPrevious: secretKeyTestOldKey,
		}, "secret-key", "rotate", "-db", dbPath, "-dry-run")
		if run.exitCode != 0 {
			t.Fatalf("dry-run exit = %d, want 0\nstdout: %s\nstderr: %s", run.exitCode, run.stdout, run.stderr)
		}
		want := "secret-key rotate: dry_run=true credential_versions=3 machine_login_vault=2 skipped=0 already_primary=0\n"
		if !strings.Contains(run.stdout, want) {
			t.Fatalf("dry-run stdout = %q, want line %q", run.stdout, want)
		}
		if !strings.Contains(run.stdout, "secret-key marker: unchanged (dry-run)\n") {
			t.Fatalf("dry-run stdout = %q, want the unchanged marker", run.stdout)
		}
		t.Logf("dry-run exit=%d stdout=%q", run.exitCode, run.stdout)
		assertSecretKeyTestNoLeak(t, run, seed, beforeCiphertexts)

		afterBytes, err := os.ReadFile(dbPath)
		if err != nil {
			t.Fatalf("read database after dry-run: %v", err)
		}
		if !bytes.Equal(beforeBytes, afterBytes) {
			t.Fatalf("dry-run changed the database file: %d bytes before, %d bytes after", len(beforeBytes), len(afterBytes))
		}
		if afterVersions, afterAccounts := secretKeyTestRowCounts(t, dbPath); afterVersions != beforeVersions || afterAccounts != beforeAccounts {
			t.Fatalf("dry-run changed row counts: versions %d→%d, accounts %d→%d", beforeVersions, afterVersions, beforeAccounts, afterAccounts)
		}
		if afterCiphertexts := secretKeyTestCiphertexts(t, dbPath); !secretKeyTestSameCiphertexts(beforeCiphertexts, afterCiphertexts) {
			t.Fatalf("dry-run changed ciphertexts")
		}
		if gateway, vault := secretKeyTestMarkerValues(t, dbPath); gateway != beforeGateway || vault != beforeVault {
			t.Fatalf("dry-run changed markers: gateway %q→%q, vault %q→%q", beforeGateway, gateway, beforeVault, vault)
		}
	})

	t.Run("refuses key flags before opening the database", func(t *testing.T) {
		t.Setenv(secretKeyTestEnvPrimary, secretKeyTestOldKey)
		t.Setenv(secretKeyTestEnvPrevious, "")
		// Help must not win over the refusal: "any key flag exits 2" is absolute.
		if run := runSecretKeyTestCLI(t, binary, nil, "secret-key", "-h", "-key", "value"); run.exitCode != 2 || !strings.Contains(run.stderr, "argv is visible") {
			t.Fatalf("help with a key flag: exit=%d stderr=%q", run.exitCode, run.stderr)
		}
		for _, flag := range []string{"-key", "--key", "-secret-key", "-previous-key", "-primary-key", "--secret-key=value"} {
			flag := flag
			t.Run(flag, func(t *testing.T) {
				dbPath := filepath.Join(t.TempDir(), "never-created.db")
				run := runSecretKeyTestCLI(t, binary, map[string]string{
					secretKeyTestEnvPrimary:  secretKeyTestNewKey,
					secretKeyTestEnvPrevious: secretKeyTestOldKey,
				}, "secret-key", "rotate", "-db", dbPath, flag, "value")
				if run.exitCode != 2 {
					t.Fatalf("exit = %d, want 2\nstdout: %s\nstderr: %s", run.exitCode, run.stdout, run.stderr)
				}
				t.Logf("%s exit=%d stderr_first_line=%q", flag, run.exitCode, strings.SplitN(run.stderr, "\n", 2)[0])
				if !strings.Contains(run.stderr, "argv is visible to every local user in 'ps' and is recorded in shell history") {
					t.Fatalf("stderr = %q, want the argv-visibility refusal", run.stderr)
				}
				if _, err := os.Stat(dbPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("database at %s was created or opened before the key flag was refused", dbPath)
				}
			})
		}
	})

	t.Run("failed rotation leaves the marker and rows unchanged", func(t *testing.T) {
		cases := []struct {
			name     string
			env      map[string]string
			category string
		}{
			{
				name:     "previous key missing",
				env:      map[string]string{secretKeyTestEnvPrimary: secretKeyTestNewKey},
				category: "gateway_secret_key_previous_missing",
			},
			{
				name:     "identical keys conflict",
				env:      map[string]string{secretKeyTestEnvPrimary: secretKeyTestNewKey, secretKeyTestEnvPrevious: secretKeyTestNewKey},
				category: "gateway_secret_key_conflict",
			},
			{
				name:     "wrong previous key",
				env:      map[string]string{secretKeyTestEnvPrimary: secretKeyTestNewKey, secretKeyTestEnvPrevious: "{{SECRET_7hq8m2aa}}"},
				category: "gateway_secret_key_rotation_payload",
			},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Setenv(secretKeyTestEnvPrimary, secretKeyTestOldKey)
				t.Setenv(secretKeyTestEnvPrevious, "")
				dbPath := filepath.Join(t.TempDir(), "gateway.db")
				seed := seedSecretKeyTestStore(t, dbPath)
				before := secretKeyTestCiphertexts(t, dbPath)

				run := runSecretKeyTestCLI(t, binary, tc.env, "secret-key", "rotate", "-db", dbPath)
				if run.exitCode != 1 {
					t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", run.exitCode, run.stdout, run.stderr)
				}
				t.Logf("%s exit=%d stderr=%q", tc.name, run.exitCode, run.stderr)
				for _, want := range []string{
					"secret-key rotate: error=" + tc.category + "\n",
					"secret-key marker: unchanged reason=" + tc.category + "\n",
				} {
					if !strings.Contains(run.stderr, want) {
						t.Fatalf("stderr = %q, want line %q", run.stderr, want)
					}
				}
				assertSecretKeyTestNoLeak(t, run, seed, before)
				if after := secretKeyTestCiphertexts(t, dbPath); !secretKeyTestSameCiphertexts(before, after) {
					t.Fatalf("failed rotation changed ciphertexts")
				}
				if gateway, vault := secretKeyTestMarkerValues(t, dbPath); gateway != "" || vault != secretKeyTestVaultMAC(t, secretKeyTestOldKey) {
					t.Fatalf("failed rotation changed markers: gateway=%q vault=%q", gateway, vault)
				}
			})
		}
	})

	t.Run("usage for help and unknown verbs", func(t *testing.T) {
		run := runSecretKeyTestCLI(t, binary, nil, "secret-key", "-h")
		if run.exitCode != 0 || !strings.Contains(run.stdout, "usage: doops-gateway secret-key rotate -db <path> [-dry-run]") || !strings.Contains(run.stdout, "DOOPS_GATEWAY_SECRET_KEY_PREVIOUS") {
			t.Fatalf("secret-key -h: exit=%d stdout=%q stderr=%q", run.exitCode, run.stdout, run.stderr)
		}
		run = runSecretKeyTestCLI(t, binary, nil, "secret-key", "rotate", "-h")
		if run.exitCode != 0 || !strings.Contains(run.stdout, "usage: doops-gateway secret-key rotate -db <path> [-dry-run]") {
			t.Fatalf("rotate -h: exit=%d stdout=%q stderr=%q", run.exitCode, run.stdout, run.stderr)
		}
		run = runSecretKeyTestCLI(t, binary, nil, "secret-key")
		if run.exitCode != 2 || !strings.Contains(run.stderr, "usage: doops-gateway secret-key rotate -db <path> [-dry-run]") {
			t.Fatalf("secret-key with no verb: exit=%d stderr=%q", run.exitCode, run.stderr)
		}
		run = runSecretKeyTestCLI(t, binary, nil, "secret-key", "frobnicate")
		if run.exitCode != 2 || !strings.Contains(run.stderr, "unknown secret-key command: frobnicate") {
			t.Fatalf("unknown verb: exit=%d stderr=%q", run.exitCode, run.stderr)
		}
		run = runSecretKeyTestCLI(t, binary, nil, "secret-key", "rotate")
		if run.exitCode != 2 || !strings.Contains(run.stderr, "-db is required") {
			t.Fatalf("missing -db: exit=%d stderr=%q", run.exitCode, run.stderr)
		}
	})
}

func runSecretKeyTestCLI(t *testing.T, binary string, env map[string]string, args ...string) cliRun {
	t.Helper()
	base := cliEnv()
	filtered := make([]string, 0, len(base)+len(env))
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		if name == secretKeyTestEnvPrimary || name == secretKeyTestEnvPrevious {
			continue
		}
		filtered = append(filtered, entry)
	}
	for name, value := range env {
		filtered = append(filtered, name+"="+value)
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = filtered
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return cliRun{stdout: stdout.String(), stderr: stderr.String(), exitCode: code}
}

func seedSecretKeyTestStore(t *testing.T, dbPath string) secretKeyTestSeed {
	t.Helper()
	store, err := server.OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	now := time.Now().UTC()
	resource, err := store.CreateCredential(server.CredentialCreateRequest{
		Name:      "secret-key-cli",
		Scope:     server.CredentialScopePersonal,
		Type:      server.CredentialTypeOpaque,
		OwnerID:   "owner-1",
		CreatedBy: "owner-1",
	})
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	payloads := []string{
		`{"data":{"name":"first","value":"` + cliPasswordCanary + `"}}`,
		`{"data":{"name":"second","value":"` + cliPasswordCanary + `-second"}}`,
		`{"data":{"name":"third","value":"` + cliPasswordCanary + `-third"}}`,
	}
	var versionIDs []string
	for _, payload := range payloads {
		version, err := store.PutCredentialVersion(server.CredentialVersionPutRequest{
			CredentialID: resource.ID,
			Payload:      json.RawMessage(payload),
			CreatedBy:    "owner-1",
		})
		if err != nil {
			t.Fatalf("put credential version: %v", err)
		}
		versionIDs = append(versionIDs, version.ID)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close seeded store: %v", err)
	}

	db := openSecretKeyTestDB(t, dbPath)
	for _, statement := range secretKeyTestInventoryDDL {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create inventory table: %v", err)
		}
	}
	// Turn the third version into a pre-AAD v1 row: the rotation must keep the
	// v1 prefix while switching the key.
	seed := secretKeyTestSeed{
		credentialID: resource.ID,
		payloads:     payloads,
		v2VersionIDs: versionIDs[:2],
		v1VersionID:  versionIDs[2],
		v1Ciphertext: secretKeyTestV1Envelope(t, secretKeyTestOldKey, payloads[2]),
	}
	if _, err := db.Exec(`UPDATE credential_versions SET payload_ciphertext = ? WHERE id = ?`, seed.v1Ciphertext, seed.v1VersionID); err != nil {
		t.Fatalf("write v1 row: %v", err)
	}
	for i, username := range []string{"root", "ops"} {
		credentialID := fmt.Sprintf("cred_vault_%d", i+1)
		versionID := fmt.Sprintf("credver_vault_%d", i+1)
		assetID := fmt.Sprintf("asset_%d", i+1)
		digest := secretKeyTestDigest(fmt.Sprintf("vault-digest-%d", i+1))
		payload := `{"data":{"name":"` + username + `","value":"` + cliPasswordCanary + `-vault-` + username + `"}}`
		ciphertext := secretKeyTestV2Envelope(t, secretKeyTestOldKey, payload, secretKeyTestVaultAAD(credentialID, versionID, digest, assetID, username))
		stamp := now.Format(time.RFC3339Nano)
		if _, err := db.Exec(`INSERT INTO inventory_machines(asset_id, name, kind, address) VALUES(?, ?, 'vm', '10.0.0.1')`, assetID, "machine-"+assetID); err != nil {
			t.Fatalf("insert machine: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO credentials(id, name, scope, owner_id, type, state, created_by, created_at, updated_at)
			VALUES(?, ?, 'machine_login', '', 'opaque', 'active', 'owner-1', ?, ?)`, credentialID, "vault-"+credentialID, stamp, stamp); err != nil {
			t.Fatalf("insert vault credential: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO credential_versions(id, credential_id, payload_ciphertext, payload_digest, state, created_by, created_at)
			VALUES(?, ?, ?, ?, 'active', 'owner-1', ?)`, versionID, credentialID, ciphertext, digest, stamp); err != nil {
			t.Fatalf("insert vault version: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO inventory_machine_login_accounts(asset_id, username, credential_id, version_id) VALUES(?, ?, ?, ?)`, assetID, username, credentialID, versionID); err != nil {
			t.Fatalf("insert vault account: %v", err)
		}
		seed.vaultVersionIDs = append(seed.vaultVersionIDs, versionID)
		seed.vaultCiphertext = append(seed.vaultCiphertext, ciphertext)
	}
	if _, err := db.Exec(`INSERT INTO inventory_machine_login_key(singleton, key_mac) VALUES(1, ?)`, secretKeyTestVaultMAC(t, secretKeyTestOldKey)); err != nil {
		t.Fatalf("insert vault marker: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}
	return seed
}

func openSecretKeyTestStore(t *testing.T, dbPath string) *server.GatewayStore {
	t.Helper()
	store, err := server.OpenGatewayStore(dbPath)
	if err != nil {
		t.Fatalf("open store %s: %v", dbPath, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func openSecretKeyTestDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite %s: %v", dbPath, err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func secretKeyTestCiphertexts(t *testing.T, dbPath string) map[string]string {
	t.Helper()
	db := openSecretKeyTestDB(t, dbPath)
	rows, err := db.Query(`SELECT id, payload_ciphertext FROM credential_versions`)
	if err != nil {
		t.Fatalf("query ciphertexts: %v", err)
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

func secretKeyTestRowCounts(t *testing.T, dbPath string) (versions, accounts int) {
	t.Helper()
	db := openSecretKeyTestDB(t, dbPath)
	if err := db.QueryRow(`SELECT COUNT(*) FROM credential_versions`).Scan(&versions); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM inventory_machine_login_accounts`).Scan(&accounts); err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	return versions, accounts
}

func secretKeyTestMarkerValues(t *testing.T, dbPath string) (gateway, vault string) {
	t.Helper()
	db := openSecretKeyTestDB(t, dbPath)
	_ = db.QueryRow(`SELECT key_mac FROM gateway_secret_key_check WHERE singleton = 1`).Scan(&gateway)
	_ = db.QueryRow(`SELECT key_mac FROM inventory_machine_login_key WHERE singleton = 1`).Scan(&vault)
	return gateway, vault
}

func secretKeyTestSameCiphertexts(a, b map[string]string) bool {
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

func assertSecretKeyTestNoLeak(t *testing.T, run cliRun, seed secretKeyTestSeed, ciphertexts map[string]string) {
	t.Helper()
	assertNoCredentialInOutput(t, run, secretKeyTestOldKey, secretKeyTestNewKey, cliPasswordCanary, seed.v1Ciphertext)
	for _, ciphertext := range ciphertexts {
		if strings.Contains(run.stdout, ciphertext) || strings.Contains(run.stderr, ciphertext) {
			t.Fatalf("ciphertext leaked into output: %q", ciphertext)
		}
	}
	for _, secret := range []string{"v1:", "v2:"} {
		if strings.Contains(run.stdout, secret) || strings.Contains(run.stderr, secret) {
			t.Fatalf("envelope prefix %q leaked into output: stdout=%q stderr=%q", secret, run.stdout, run.stderr)
		}
	}
}

func secretKeyTestKey(raw string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return sum[:]
}

func secretKeyTestGatewayMAC(t *testing.T, raw string) string {
	t.Helper()
	mac := hmac.New(sha256.New, secretKeyTestKey(raw))
	mac.Write([]byte("doops/gateway-secret-key-check/v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

func secretKeyTestVaultMAC(t *testing.T, raw string) string {
	t.Helper()
	derive := hmac.New(sha256.New, secretKeyTestKey(raw))
	derive.Write([]byte("doops/machine-login/v1/vault-key"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write([]byte("v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

func secretKeyTestDigest(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func secretKeyTestVaultAAD(credentialID, versionID, digest, assetID, username string) []byte {
	return []byte(strings.Join([]string{"credential", credentialID, versionID, string(server.CredentialTypeOpaque), digest}, "\x00") + "\x00" + assetID + "\x00" + username)
}

func secretKeyTestV1Envelope(t *testing.T, rawKey, plaintext string) string {
	t.Helper()
	block, err := aes.NewCipher(secretKeyTestKey(rawKey))
	if err != nil {
		t.Fatalf("v1 cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("v1 gcm: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("v1 nonce: %v", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return "v1:" + base64.RawStdEncoding.EncodeToString(append(nonce, sealed...))
}

func secretKeyTestV2Envelope(t *testing.T, rawKey, plaintext string, aad []byte) string {
	t.Helper()
	block, err := aes.NewCipher(secretKeyTestKey(rawKey))
	if err != nil {
		t.Fatalf("v2 cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("v2 gcm: %v", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("v2 nonce: %v", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), aad)
	return "v2:" + base64.RawStdEncoding.EncodeToString(append(nonce, sealed...))
}
