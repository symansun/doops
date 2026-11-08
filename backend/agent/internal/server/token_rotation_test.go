package server

import "testing"

func TestGatewayStoreTokenRotation(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	user, err := store.CreateUser("bob")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	old, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "old"})
	if err != nil {
		t.Fatalf("create old token: %v", err)
	}
	if _, err := store.VerifyUserToken(old.Plaintext); err != nil {
		t.Fatalf("old token should verify before rotation: %v", err)
	}

	// 轮换：签发新 token -> 吊销旧 token -> 旧 token 必须立即失效
	fresh, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "rotated"})
	if err != nil {
		t.Fatalf("create rotated token: %v", err)
	}
	if err := store.RevokeToken(old.ID); err != nil {
		t.Fatalf("revoke old token: %v", err)
	}
	if _, err := store.VerifyUserToken(old.Plaintext); err == nil {
		t.Fatal("revoked token must be rejected")
	}
	if _, err := store.VerifyUserToken(fresh.Plaintext); err != nil {
		t.Fatalf("rotated token should verify: %v", err)
	}

	tokens, err := store.ListTokens(TokenKindUser)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	var sawRevoked bool
	for _, tok := range tokens {
		if tok.ID == old.ID && tok.Revoked {
			sawRevoked = true
		}
	}
	if !sawRevoked {
		t.Fatal("list should show old token as revoked")
	}

	// 幂等性：重复吊销报错
	if err := store.RevokeToken(old.ID); err == nil {
		t.Fatal("double revoke should fail")
	}
	if err := store.RevokeToken(""); err == nil {
		t.Fatal("empty id should fail")
	}
}
