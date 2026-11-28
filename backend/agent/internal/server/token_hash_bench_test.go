package server

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// openTokenBenchStore opens a real GatewayStore (real tables, real SQL) in a
// throwaway directory. The benchmarks below must never talk to a stub: the
// per-request cost under review includes the token SELECT and the join on users.
func openTokenBenchStore(b *testing.B) *GatewayStore {
	b.Helper()
	store, err := OpenGatewayStore(filepath.Join(b.TempDir(), "gateway.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	b.Cleanup(func() { _ = store.Close() })
	return store
}

func benchToken(b *testing.B, store *GatewayStore, user string) CreatedToken {
	b.Helper()
	account, err := store.CreateUser(user)
	if err != nil {
		b.Fatalf("create user: %v", err)
	}
	token, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: account.ID})
	if err != nil {
		b.Fatalf("create token: %v", err)
	}
	return token
}

// BenchmarkVerifyTokenLegacyBcrypt measures the pre-S-04 cost: every request
// pays bcrypt.CompareHashAndPassword at the production cost factor.
//
// The row is restored to its bcrypt hash outside the timed region on every
// iteration. Without that reset the first iteration would upgrade the row to
// the fast hash and the remaining iterations would measure the wrong path.
func BenchmarkVerifyTokenLegacyBcrypt(b *testing.B) {
	store := openTokenBenchStore(b)
	token := benchToken(b, store, "bench-legacy-bcrypt")
	legacyHash, err := bcrypt.GenerateFromPassword(tokenPassword(token.Plaintext), bcrypt.DefaultCost)
	if err != nil {
		b.Fatalf("bcrypt hash: %v", err)
	}
	restore := func() {
		if _, err := store.db.Exec("UPDATE tokens SET hash = ? WHERE id = ?", string(legacyHash), token.ID); err != nil {
			b.Fatalf("restore legacy hash: %v", err)
		}
	}
	restore()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		restore()
		b.StartTimer()
		if _, err := store.VerifyUserToken(token.Plaintext); err != nil {
			b.Fatalf("legacy token rejected: %v", err)
		}
	}
}

// BenchmarkVerifyTokenFastHash measures the post-S-04 cost for a freshly issued
// token: sha256 of the full plaintext compared with hmac.Equal.
func BenchmarkVerifyTokenFastHash(b *testing.B) {
	store := openTokenBenchStore(b)
	token := benchToken(b, store, "bench-fast-hash")
	var hash string
	if err := store.db.QueryRow("SELECT hash FROM tokens WHERE id = ?", token.ID).Scan(&hash); err != nil {
		b.Fatalf("read token hash: %v", err)
	}
	sum := sha256.Sum256([]byte(token.Plaintext))
	want := "sha256:" + hex.EncodeToString(sum[:])
	if hash != want {
		b.Fatalf("CreateToken stored hash %q, want %q: the fast hash path is not in place", hash, want)
	}
	if !strings.HasPrefix(hash, "sha256:") {
		b.Fatalf("token hash %q is not in the sha256 form", hash)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := store.VerifyUserToken(token.Plaintext); err != nil {
			b.Fatalf("fast token rejected: %v", err)
		}
	}
}
