package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenGatewayStoreRestrictsPersistentFilePermissions(t *testing.T) {
	root := t.TempDir()
	sharedDir := filepath.Join(root, "shared")
	if err := os.Mkdir(sharedDir, 0755); err != nil {
		t.Fatalf("create permissive store directory: %v", err)
	}
	databasePath := filepath.Join(sharedDir, "gateway.db")
	if err := os.WriteFile(databasePath, nil, 0644); err != nil {
		t.Fatalf("create permissive database file: %v", err)
	}

	store, err := OpenGatewayStore(databasePath)
	if err != nil {
		t.Fatalf("open gateway store: %v", err)
	}
	defer store.Close()

	for path, want := range map[string]os.FileMode{
		sharedDir:    0755,
		databasePath: 0600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s permissions = %04o, want %04o", path, got, want)
		}
	}
	for _, sidecar := range []string{databasePath + "-wal", databasePath + "-shm"} {
		info, err := os.Stat(sidecar)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("stat %s: %v", sidecar, err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Fatalf("%s permissions = %04o, want 0600", sidecar, got)
		}
	}
}

func TestOpenGatewayStoreCreatesPrivateStoreDirectory(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "private")
	store, err := OpenGatewayStore(filepath.Join(storeDir, "gateway.db"))
	if err != nil {
		t.Fatalf("open gateway store: %v", err)
	}
	defer store.Close()

	info, err := os.Stat(storeDir)
	if err != nil {
		t.Fatalf("stat store directory: %v", err)
	}
	if got := info.Mode().Perm(); got != 0700 {
		t.Fatalf("store directory permissions = %04o, want 0700", got)
	}
}

func TestGatewayStoreOnlyIssuesUserTokensAndPermissions(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	userToken, err := store.CreateToken(CreateTokenRequest{
		Kind:      TokenKindUser,
		UserID:    user.ID,
		Name:      "alice laptop",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create user token: %v", err)
	}
	if _, err := store.CreateToken(CreateTokenRequest{Kind: TokenKind("agent")}); err == nil {
		t.Fatal("agent registration tokens must not be issued")
	}

	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster:  "dev",
		Instance: "local",
	}); err != nil {
		t.Fatalf("grant user: %v", err)
	}

	userAuth, err := store.VerifyUserToken(userToken.Plaintext)
	if err != nil {
		t.Fatalf("verify user token: %v", err)
	}
	if !store.UserCanUseTarget(userAuth.UserID, "dev", "local") {
		t.Fatal("expected alice to use dev/local")
	}
	if store.UserCanUseTarget(userAuth.UserID, "prod", "local") {
		t.Fatal("alice must not use prod/local")
	}
}

func TestGatewayStoreMigrationDropsRetiredCredentialRunAuthorizations(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if _, err := store.db.Exec(`CREATE TABLE credential_run_authorizations (run_id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create retired table: %v", err)
	}
	if err := store.migrate(); err != nil {
		t.Fatalf("rerun migration: %v", err)
	}

	var count int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'credential_run_authorizations'`,
	).Scan(&count); err != nil {
		t.Fatalf("query retired table: %v", err)
	}
	if count != 0 {
		t.Fatal("retired credential run authorization table must be removed")
	}
}

func TestGatewayStoreUserDefaultsToNoTargetAccess(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	user, err := store.CreateUser("operator")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, err := store.CreateToken(CreateTokenRequest{
		Kind:   TokenKindUser,
		UserID: user.ID,
		Name:   "operator laptop",
	})
	if err != nil {
		t.Fatalf("create user token: %v", err)
	}
	auth, err := store.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatalf("verify user token: %v", err)
	}

	if store.UserCanUseTarget(auth.UserID, "any-cluster", "any-node") {
		t.Fatal("new user without grants must not use any target")
	}
	if store.UserIsPlatformAdmin(auth.UserID) {
		t.Fatal("new user must not be a platform admin")
	}
}

func TestGatewayStoreTokenIDFastPathAndExpiredCleanup(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	expired, err := store.CreateToken(CreateTokenRequest{
		Kind:      TokenKindUser,
		UserID:    user.ID,
		Name:      "expired",
		ExpiresAt: time.Now().Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("create expired token: %v", err)
	}
	active, err := store.CreateToken(CreateTokenRequest{
		Kind:      TokenKindUser,
		UserID:    user.ID,
		Name:      "active",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create active token: %v", err)
	}
	if got := tokenIDFromPlaintext(active.Plaintext); got != active.ID {
		t.Fatalf("token id parse mismatch: got %q want %q", got, active.ID)
	}
	if _, err := store.VerifyUserToken(expired.Plaintext); err == nil {
		t.Fatal("expired token must be rejected")
	}
	auth, err := store.VerifyUserToken(active.Plaintext)
	if err != nil {
		t.Fatalf("active token should verify: %v", err)
	}
	if auth.TokenID != active.ID {
		t.Fatalf("verify should return active token id, got %#v", auth)
	}
	deleted, err := store.DeleteExpiredTokens(time.Now())
	if err != nil {
		t.Fatalf("delete expired tokens: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected one expired token deleted, got %d", deleted)
	}
	if _, err := store.VerifyUserToken(active.Plaintext); err != nil {
		t.Fatalf("active token should survive cleanup: %v", err)
	}
}

func TestGatewayStoreTokenAuthFailureReasons(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.CreateUser("auth-reasons")
	if err != nil {
		t.Fatal(err)
	}

	reasonOf := func(token string) AuthFailureReason {
		t.Helper()
		_, err := store.VerifyUserToken(token)
		var authErr *TokenAuthError
		if !errors.As(err, &authErr) {
			t.Fatalf("error %v is not a TokenAuthError", err)
		}
		return authErr.Reason
	}
	if got := reasonOf(""); got != AuthFailureMissing {
		t.Fatalf("missing token reason = %q", got)
	}
	if got := reasonOf("not-a-gateway-token"); got != AuthFailureInvalid {
		t.Fatalf("unknown token reason = %q", got)
	}

	newToken := func(name string, expires time.Time) CreatedToken {
		t.Helper()
		token, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: name, ExpiresAt: expires})
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	expired := newToken("expired", time.Now().Add(-time.Minute))
	if got := reasonOf(expired.Plaintext); got != AuthFailureExpired {
		t.Fatalf("expired token reason = %q", got)
	}
	revoked := newToken("revoked", time.Time{})
	if err := store.RevokeToken(revoked.ID); err != nil {
		t.Fatal(err)
	}
	if got := reasonOf(revoked.Plaintext); got != AuthFailureRevoked {
		t.Fatalf("revoked token reason = %q", got)
	}
	disabled := newToken("disabled", time.Time{})
	if err := store.SetUserDisabled(user.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := reasonOf(disabled.Plaintext); got != AuthFailureUserDisabled {
		t.Fatalf("disabled user reason = %q", got)
	}
	if err := store.SetUserDisabled(user.ID, false); err != nil {
		t.Fatal(err)
	}
	invalidExpiry := newToken("invalid-expiry", time.Time{})
	if _, err := store.db.Exec("UPDATE tokens SET expires_at = ? WHERE id = ?", "invalid-expiry", invalidExpiry.ID); err != nil {
		t.Fatal(err)
	}
	if got := reasonOf(invalidExpiry.Plaintext); got != AuthFailureInvalidExpiry {
		t.Fatalf("invalid expiry reason = %q", got)
	}
	if _, err := store.db.Exec("UPDATE tokens SET expires_at = ? WHERE id = ?", " ", invalidExpiry.ID); err != nil {
		t.Fatal(err)
	}
	if got := reasonOf(invalidExpiry.Plaintext); got != AuthFailureInvalidExpiry {
		t.Fatalf("whitespace expiry became an unlimited token: %q", got)
	}
	if _, err := store.db.Exec("UPDATE tokens SET expires_at = '', revoked_at = ' ' WHERE id = ?", invalidExpiry.ID); err != nil {
		t.Fatal(err)
	}
	if got := reasonOf(invalidExpiry.Plaintext); got != AuthFailureRevoked {
		t.Fatalf("nonempty revocation became an active token: %q", got)
	}
	wrongSecret := invalidExpiry.Plaintext[:len(invalidExpiry.Plaintext)-1] + "x"
	if got := reasonOf(wrongSecret); got != AuthFailureInvalid {
		t.Fatalf("hash mismatch reason = %q", got)
	}
}

func TestGatewayStoreTokenListOmitsExpiryForNonExpiringToken(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.CreateUser("token-list")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "never"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: "one-hour", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	tokens, err := store.ListTokens(TokenKindUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 2 {
		t.Fatalf("token count = %d", len(tokens))
	}
	data, err := json.Marshal(tokens)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, item := range decoded {
		if item["name"] == "never" {
			if _, ok := item["expires_at"]; ok {
				t.Fatalf("non-expiring token exposed a zero expiry: %s", data)
			}
		}
		if item["name"] == "one-hour" {
			if _, ok := item["expires_at"]; !ok {
				t.Fatalf("expiring token omitted expiry: %s", data)
			}
		}
	}
}

func TestGatewayStoreTokenListRejectsInvalidExpiryMetadata(t *testing.T) {
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.CreateUser("token-list-corrupt-expiry")
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("UPDATE tokens SET expires_at = ? WHERE id = ?", "invalid-expiry", token.ID); err != nil {
		t.Fatal(err)
	}
	_, err = store.ListTokens(TokenKindUser)
	var failure *TokenAuthError
	if !errors.As(err, &failure) || failure.Reason != AuthFailureInvalidExpiry {
		t.Fatalf("corrupted expiry must not become an unlimited token: %v", err)
	}
}

func TestGatewayStoreUserPasswordLogin(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	user, err := store.CreateUserWithPassword(CreateUserRequest{Name: "alice", Password: "Ab123456"})
	if err != nil {
		t.Fatalf("create user with password: %v", err)
	}
	if user.PasswordHash == "" {
		t.Fatal("expected password hash to be stored")
	}

	if _, err := store.VerifyUserPassword("alice", "wrong"); err == nil {
		t.Fatal("wrong password must be rejected")
	}
	okUser, err := store.VerifyUserPassword("alice", "Ab123456")
	if err != nil {
		t.Fatalf("verify password: %v", err)
	}
	if okUser.ID != user.ID {
		t.Fatalf("unexpected user after verify: %#v", okUser)
	}

	if err := store.SetUserPassword(user.ID, "NewPass123"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if _, err := store.VerifyUserPassword("alice", "Ab123456"); err == nil {
		t.Fatal("old password must stop working")
	}
	if _, err := store.VerifyUserPassword("alice", "NewPass123"); err != nil {
		t.Fatalf("new password should work: %v", err)
	}
}

func TestGatewayStoreListUsersDoesNotDeadlockSingleConnection(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	alice, err := store.CreateUser("alice")
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := store.CreateUser("bob")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	if err := store.GrantUser(alice.ID, ScopeGrant{
		Cluster:  "dev",
		Instance: "node-1",
	}); err != nil {
		t.Fatalf("grant alice exec: %v", err)
	}
	if err := store.GrantUser(alice.ID, ScopeGrant{
		Cluster:  "dev",
		Instance: "node-2",
	}); err != nil {
		t.Fatalf("grant alice read: %v", err)
	}
	if err := store.GrantUser(bob.ID, ScopeGrant{Cluster: "*", Instance: "*"}); err != nil {
		t.Fatalf("grant bob targets: %v", err)
	}
	if err := store.SetUserAdmin(bob.ID, true); err != nil {
		t.Fatalf("set bob admin: %v", err)
	}

	type result struct {
		users []UserSummary
		err   error
	}
	done := make(chan result, 1)
	go func() {
		users, err := store.ListUsers()
		done <- result{users: users, err: err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("list users: %v", got.err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
		if len(got.users) != 2 {
			t.Fatalf("expected 2 users, got %#v", got.users)
		}
		byName := make(map[string]UserSummary, len(got.users))
		for _, user := range got.users {
			byName[user.Name] = user
		}
		if byName["alice"].GrantCount != 2 || byName["alice"].IsAdmin {
			t.Fatalf("unexpected alice summary: %#v", byName["alice"])
		}
		if byName["bob"].GrantCount != 1 || !byName["bob"].IsAdmin {
			t.Fatalf("unexpected bob summary: %#v", byName["bob"])
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ListUsers blocked while holding the only SQLite connection")
	}
}

func TestGatewayStoreAuditCRUD(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	user, err := store.CreateUser("bob")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	started := time.Now().UTC()
	auditID, err := store.StartAudit(AuditEvent{
		UserID:         user.ID,
		TokenID:        "tok_123",
		Cluster:        "dev",
		Instance:       "local",
		Action:         ActionExec,
		Session:        "smoke",
		CommandSummary: "hostname",
		StartedAt:      started,
	})
	if err != nil {
		t.Fatalf("start audit: %v", err)
	}
	if err := store.FinishAudit(auditID, AuditFinish{
		Status:   "success",
		Tail:     "node-a\n",
		BytesIn:  12,
		BytesOut: 7,
		EndedAt:  started.Add(time.Second),
	}); err != nil {
		t.Fatalf("finish audit: %v", err)
	}

	events, err := store.ListAudit(10)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %d", len(events))
	}
	if events[0].Status != "success" || events[0].Tail != "node-a\n" {
		t.Fatalf("unexpected audit event: %#v", events[0])
	}
}

func TestGatewayStoreAuditFilterAndPurge(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	alice, err := store.CreateUser("alice")
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := store.CreateUser("bob")
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}

	oldTime := time.Now().UTC().Add(-48 * time.Hour)
	newTime := time.Now().UTC()

	oldID, err := store.StartAudit(AuditEvent{
		UserID:         alice.ID,
		TokenID:        "tok_old",
		Cluster:        "doops-oilan",
		Instance:       "oilan-node",
		Action:         ActionExec,
		Session:        "old-session",
		CommandSummary: "hostname",
		StartedAt:      oldTime,
	})
	if err != nil {
		t.Fatalf("start old audit: %v", err)
	}
	if err := store.FinishAudit(oldID, AuditFinish{
		Status:   "success",
		Tail:     "old\n",
		BytesIn:  1,
		BytesOut: 4,
		EndedAt:  oldTime.Add(time.Second),
	}); err != nil {
		t.Fatalf("finish old audit: %v", err)
	}

	newID, err := store.StartAudit(AuditEvent{
		UserID:         bob.ID,
		TokenID:        "tok_new",
		Cluster:        "doops-edu",
		Instance:       "edu-coder",
		Action:         ActionAsk,
		Session:        "new-session",
		CommandSummary: "readonly check",
		StartedAt:      newTime,
	})
	if err != nil {
		t.Fatalf("start new audit: %v", err)
	}
	if err := store.FinishAudit(newID, AuditFinish{
		Status:   "error",
		Error:    "denied",
		Tail:     "new\n",
		BytesIn:  2,
		BytesOut: 4,
		EndedAt:  newTime.Add(time.Second),
	}); err != nil {
		t.Fatalf("finish new audit: %v", err)
	}

	filtered, err := store.ListAuditFiltered(AuditFilter{
		UserID:  bob.ID,
		Cluster: "doops-edu",
		Action:  ActionAsk,
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("filter audit: %v", err)
	}
	if len(filtered) != 1 || filtered[0].Session != "new-session" {
		t.Fatalf("unexpected filtered audit rows: %#v", filtered)
	}

	deleted, err := store.DeleteAuditBefore(time.Now().UTC().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("purge old audit: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted row, got %d", deleted)
	}

	remaining, err := store.ListAudit(10)
	if err != nil {
		t.Fatalf("list remaining audit: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Session != "new-session" {
		t.Fatalf("unexpected remaining audit rows: %#v", remaining)
	}
}

func TestGatewayStoreAgentStatusLifecycle(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.MarkAgentOnline(AgentStatus{
		Cluster:    "dev",
		Instance:   "local",
		TokenID:    "tok_agent",
		Remote:     "127.0.0.1:1234",
		Generation: 2,
		LastSeen:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("mark online: %v", err)
	}
	agents, err := store.ListAgentStatus()
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	if len(agents) != 1 || agents[0].Status != "online" || agents[0].Generation != 2 {
		t.Fatalf("expected one online agent, got %#v", agents)
	}

	if err := store.MarkAgentOffline("dev", "local", 1, agents[0].LastSeen.Add(-time.Minute)); err != nil {
		t.Fatalf("mark stale generation offline: %v", err)
	}
	agents, err = store.ListAgentStatus()
	if err != nil {
		t.Fatalf("list agents after stale offline: %v", err)
	}
	if len(agents) != 1 || agents[0].Status != "online" {
		t.Fatalf("stale generation must not mark current agent offline: %#v", agents)
	}

	finalLastSeen := agents[0].LastSeen.Add(time.Second)
	if err := store.MarkAgentOffline("dev", "local", 2, finalLastSeen); err != nil {
		t.Fatalf("mark offline: %v", err)
	}
	agents, err = store.ListAgentStatus()
	if err != nil {
		t.Fatalf("list agents after offline: %v", err)
	}
	if len(agents) != 1 || agents[0].Status != "offline" || !agents[0].LastSeen.Equal(finalLastSeen) {
		t.Fatalf("expected offline agent retained in store, got %#v", agents)
	}
}

func TestGatewayStoreDoesNotLetStaleAgentTouchOverwriteLifecycle(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	oldConnectedAt := time.Now().UTC().Add(-time.Minute)
	newConnectedAt := oldConnectedAt.Add(30 * time.Second)
	newLastSeen := newConnectedAt.Add(time.Second)
	if err := store.MarkAgentOnline(AgentStatus{
		Cluster:     "dev",
		Instance:    "local",
		Generation:  1,
		ConnectedAt: oldConnectedAt,
		LastSeen:    oldConnectedAt,
	}); err != nil {
		t.Fatalf("mark old agent online: %v", err)
	}
	if err := store.MarkAgentOnline(AgentStatus{
		Cluster:     "dev",
		Instance:    "local",
		Generation:  2,
		ConnectedAt: newConnectedAt,
		LastSeen:    newLastSeen,
	}); err != nil {
		t.Fatalf("mark replacement agent online: %v", err)
	}
	if err := store.TouchAgentContext(
		context.Background(),
		"dev",
		"local",
		1,
		oldConnectedAt.Add(10*time.Second),
	); err != nil {
		t.Fatalf("touch stale agent: %v", err)
	}

	agents, err := store.ListAgentStatus()
	if err != nil {
		t.Fatalf("list replacement agent: %v", err)
	}
	if len(agents) != 1 || !agents[0].ConnectedAt.Equal(newConnectedAt) || !agents[0].LastSeen.Equal(newLastSeen) {
		t.Fatalf("stale touch overwrote replacement agent: %#v", agents)
	}

	finalLastSeen := newConnectedAt.Add(10 * time.Second)
	if err := store.MarkAgentOffline("dev", "local", 2, finalLastSeen); err != nil {
		t.Fatalf("mark replacement agent offline: %v", err)
	}
	offline, err := store.ListAgentStatus()
	if err != nil {
		t.Fatalf("list offline agent: %v", err)
	}
	if len(offline) != 1 {
		t.Fatalf("expected one offline agent, got %#v", offline)
	}
	if !offline[0].LastSeen.Equal(finalLastSeen) {
		t.Fatalf("offline last-seen = %s, want %s", offline[0].LastSeen, finalLastSeen)
	}
	if err := store.TouchAgentContext(
		context.Background(),
		"dev",
		"local",
		2,
		newConnectedAt.Add(20*time.Second),
	); err != nil {
		t.Fatalf("touch offline agent: %v", err)
	}
	offline, err = store.ListAgentStatus()
	if err != nil {
		t.Fatalf("list offline agent after stale touch: %v", err)
	}
	if len(offline) != 1 || !offline[0].LastSeen.Equal(finalLastSeen) {
		t.Fatalf("stale touch overwrote offline agent: %#v", offline)
	}
}

func TestGatewayStoreRecoversUpgradeAuditWithoutAnUnusedLedger(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	var tables int
	if err := store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'upgrade_operations'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("unexpected unused upgrade ledger: count=%d err=%v", tables, err)
	}
	id, err := store.StartAudit(AuditEvent{
		Action: ActionAgentUpgrade, Cluster: "dev", Instance: "local",
		CorrelationID: "upg-restart", CommandSummary: "doops_agent_upgrade phase=apply",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkExecutionDispatched(id); err != nil {
		t.Fatal(err)
	}
	if err := store.RecoverInterruptedExecutions(); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListAuditFiltered(AuditFilter{CorrelationID: "upg-restart"})
	if err != nil || len(events) != 1 || events[0].Status != "outcome_unknown" || events[0].CommandSummary != "doops_agent_upgrade phase=apply" {
		t.Fatalf("restart lost upgrade audit identity: events=%#v err=%v", events, err)
	}
}
