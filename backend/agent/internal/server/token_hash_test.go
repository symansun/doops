package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// S-04 acceptance cases. They run against a real GatewayStore (real tables,
// real SQL, real VerifyUserToken) so the revocation, expiry and disabled-user
// decisions are exercised exactly as a request would exercise them.

func openTokenHashStore(t *testing.T) *GatewayStore {
	t.Helper()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func newTokenHashUser(t *testing.T, store *GatewayStore, name string) CreatedToken {
	t.Helper()
	user, err := store.CreateUser(name)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	return token
}

func readTokenHash(t *testing.T, store *GatewayStore, tokenID string) string {
	t.Helper()
	var hash string
	if err := store.db.QueryRow("SELECT hash FROM tokens WHERE id = ?", tokenID).Scan(&hash); err != nil {
		t.Fatalf("read hash for %s: %v", tokenID, err)
	}
	return hash
}

func writeTokenHash(t *testing.T, store *GatewayStore, tokenID, hash string) {
	t.Helper()
	if _, err := store.db.Exec("UPDATE tokens SET hash = ? WHERE id = ?", hash, tokenID); err != nil {
		t.Fatalf("write hash for %s: %v", tokenID, err)
	}
}

// presetLegacyTokenHash turns a freshly issued row into the pre-S-04 shape: a
// real bcrypt hash over tokenPassword(plaintext), exactly what the old
// CreateToken wrote.
func presetLegacyTokenHash(t *testing.T, store *GatewayStore, token CreatedToken) string {
	t.Helper()
	legacy, err := bcrypt.GenerateFromPassword(tokenPassword(token.Plaintext), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	writeTokenHash(t, store, token.ID, string(legacy))
	return string(legacy)
}

func requireTokenAuthFailure(t *testing.T, err error, want AuthFailureReason) {
	t.Helper()
	var failure *TokenAuthError
	if !errors.As(err, &failure) {
		t.Fatalf("want TokenAuthError %q, got %v", want, err)
	}
	if failure.Reason != want {
		t.Fatalf("want reason %q, got %q", want, failure.Reason)
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("reason %q did not unwrap to ErrUnauthorized", want)
	}
}

// flipLastChar changes exactly one character of the token secret.
func flipLastChar(token string) string {
	if strings.HasSuffix(token, "0") {
		return token[:len(token)-1] + "1"
	}
	return token[:len(token)-1] + "0"
}

// 1: a newly issued token is stored in the fast form and still authenticates.
func TestTokenHashNewTokenUsesFastHash(t *testing.T) {
	store := openTokenHashStore(t)
	token := newTokenHashUser(t, store, "fast-hash-owner")

	hash := readTokenHash(t, store, token.ID)
	if !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("issued token hash %q is not in the sha256 form", hash)
	}
	sum := sha256.Sum256([]byte(token.Plaintext))
	if want := "sha256:" + hex.EncodeToString(sum[:]); hash != want {
		t.Fatalf("issued token hash = %q, want %q", hash, want)
	}
	if len(hash) != len("sha256:")+64 {
		t.Fatalf("issued token hash %q is not 64 hex characters after the prefix", hash)
	}
	auth, err := store.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatalf("fast token rejected: %v", err)
	}
	if auth.TokenID != token.ID || auth.UserID == "" || auth.Kind != TokenKindUser {
		t.Fatalf("fast token identity = %#v", auth)
	}
}

// 2: one changed character is invalid, on both hash generations.
func TestTokenHashTamperedTokenIsInvalid(t *testing.T) {
	store := openTokenHashStore(t)
	token := newTokenHashUser(t, store, "tamper-owner")

	_, err := store.VerifyUserToken(flipLastChar(token.Plaintext))
	requireTokenAuthFailure(t, err, AuthFailureInvalid)

	legacy := newTokenHashUser(t, store, "tamper-legacy-owner")
	presetLegacyTokenHash(t, store, legacy)
	_, err = store.VerifyUserToken(flipLastChar(legacy.Plaintext))
	requireTokenAuthFailure(t, err, AuthFailureInvalid)
}

// 3: revocation takes effect on the very next call with the same token. A TTL
// cache would keep answering "ok" here, which is why S-04 refused that option.
func TestTokenHashRevocationHasNoCacheWindow(t *testing.T) {
	store := openTokenHashStore(t)
	token := newTokenHashUser(t, store, "revoke-owner")

	if _, err := store.VerifyUserToken(token.Plaintext); err != nil {
		t.Fatalf("token rejected before revocation: %v", err)
	}
	if err := store.RevokeToken(token.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err := store.VerifyUserToken(token.Plaintext)
	requireTokenAuthFailure(t, err, AuthFailureRevoked)
	// Same token, second call: the decision is re-read, not remembered.
	_, err = store.VerifyUserToken(token.Plaintext)
	requireTokenAuthFailure(t, err, AuthFailureRevoked)
}

// 4: expiry is enforced per request.
func TestTokenHashExpiredToken(t *testing.T) {
	store := openTokenHashStore(t)
	user, err := store.CreateUser("expired-owner")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, err := store.CreateToken(CreateTokenRequest{
		Kind:      TokenKindUser,
		UserID:    user.ID,
		ExpiresAt: time.Now().UTC().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	_, err = store.VerifyUserToken(token.Plaintext)
	requireTokenAuthFailure(t, err, AuthFailureExpired)
}

// 5: a disabled user is rejected per request.
func TestTokenHashDisabledUser(t *testing.T) {
	store := openTokenHashStore(t)
	token := newTokenHashUser(t, store, "disabled-owner")
	auth, err := store.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatalf("enabled token rejected: %v", err)
	}
	if err := store.SetUserDisabled(auth.UserID, true); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	_, err = store.VerifyUserToken(token.Plaintext)
	requireTokenAuthFailure(t, err, AuthFailureUserDisabled)
}

// 6: a pre-existing bcrypt row keeps working and is upgraded in the same
// request, without a migration script and without invalidating the token.
func TestTokenHashLegacyRowUpgradesOnFirstUse(t *testing.T) {
	store := openTokenHashStore(t)
	token := newTokenHashUser(t, store, "legacy-owner")
	legacy := presetLegacyTokenHash(t, store, token)
	if !strings.HasPrefix(legacy, "$2") {
		t.Fatalf("test fixture is not a bcrypt hash: %q", legacy)
	}

	first, err := store.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatalf("legacy token rejected: %v", err)
	}
	upgraded := readTokenHash(t, store, token.ID)
	if !strings.HasPrefix(upgraded, "sha256:") {
		t.Fatalf("legacy hash was not upgraded, still %q", upgraded)
	}
	if want := tokenFastHash(token.Plaintext); upgraded != want {
		t.Fatalf("upgraded hash = %q, want %q", upgraded, want)
	}

	second, err := store.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatalf("upgraded token rejected: %v", err)
	}
	if second != first {
		t.Fatalf("identity changed across the upgrade: %#v then %#v", first, second)
	}
	if again := readTokenHash(t, store, token.ID); again != upgraded {
		t.Fatalf("second verification rewrote the hash: %q then %q", upgraded, again)
	}
}

// 7: a failed comparison never upgrades. The row must stay verifiable for the
// real holder of the token.
func TestTokenHashFailedVerificationDoesNotUpgrade(t *testing.T) {
	store := openTokenHashStore(t)
	token := newTokenHashUser(t, store, "no-upgrade-on-failure")
	legacy := presetLegacyTokenHash(t, store, token)

	_, err := store.VerifyUserToken(flipLastChar(token.Plaintext))
	requireTokenAuthFailure(t, err, AuthFailureInvalid)
	if got := readTokenHash(t, store, token.ID); got != legacy {
		t.Fatalf("failed verification rewrote the hash: %q -> %q", legacy, got)
	}
	if got := readTokenHash(t, store, token.ID); !strings.HasPrefix(got, "$2") {
		t.Fatalf("hash lost its bcrypt form after a failed attempt: %q", got)
	}

	// The genuine token still works and then upgrades.
	if _, err := store.VerifyUserToken(token.Plaintext); err != nil {
		t.Fatalf("genuine token rejected after a failed attempt: %v", err)
	}
	if got := readTokenHash(t, store, token.ID); !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("genuine token did not upgrade the row: %q", got)
	}
}

// 8: concurrent first use of one legacy row writes the hash once. The CAS
// predicate is what makes that true.
func TestTokenHashConcurrentUpgradeWritesOnce(t *testing.T) {
	store := openTokenHashStore(t)
	token := newTokenHashUser(t, store, "concurrent-upgrade")
	legacy := presetLegacyTokenHash(t, store, token)

	// Direct compare-and-set semantics: the first writer wins, the second sees
	// the row already changed and must not overwrite it.
	firstWrite := tokenFastHash("first-writer")
	secondWrite := tokenFastHash("second-writer")
	if got := store.upgradeTokenHash(token.ID, legacy, firstWrite); got != 1 {
		t.Fatalf("first upgrade affected %d rows, want 1", got)
	}
	if got := store.upgradeTokenHash(token.ID, legacy, secondWrite); got != 0 {
		t.Fatalf("stale upgrade affected %d rows, want 0", got)
	}
	if got := readTokenHash(t, store, token.ID); got != firstWrite {
		t.Fatalf("stale writer overwrote the hash: %q, want %q", got, firstWrite)
	}

	// Real concurrency: count hash rewrites with a trigger so the assertion is
	// about writes, not about which goroutine happened to win.
	race := newTokenHashUser(t, store, "concurrent-race")
	presetLegacyTokenHash(t, store, race)
	if _, err := store.db.Exec("CREATE TABLE token_hash_writes (n INTEGER NOT NULL)"); err != nil {
		t.Fatalf("create counter: %v", err)
	}
	if _, err := store.db.Exec("INSERT INTO token_hash_writes (n) VALUES (0)"); err != nil {
		t.Fatalf("seed counter: %v", err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER token_hash_write_count AFTER UPDATE OF hash ON tokens WHEN OLD.hash <> NEW.hash BEGIN UPDATE token_hash_writes SET n = n + 1; END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = store.VerifyUserToken(race.Plaintext)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d rejected the legacy token: %v", i, err)
		}
	}
	var writes int
	if err := store.db.QueryRow("SELECT n FROM token_hash_writes").Scan(&writes); err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if writes != 1 {
		t.Fatalf("concurrent upgrade wrote the hash %d times, want 1", writes)
	}
	if got := readTokenHash(t, store, race.ID); got != tokenFastHash(race.Plaintext) {
		t.Fatalf("final hash = %q, want %q", got, tokenFastHash(race.Plaintext))
	}
}
