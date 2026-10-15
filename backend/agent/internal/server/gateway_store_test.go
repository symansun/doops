package server

import (
	"context"
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
		Actions:  []GatewayAction{ActionExec, ActionRead, ActionTargetsList},
	}); err != nil {
		t.Fatalf("grant user: %v", err)
	}

	userAuth, err := store.VerifyUserToken(userToken.Plaintext)
	if err != nil {
		t.Fatalf("verify user token: %v", err)
	}
	if !store.UserCan(userAuth.UserID, "dev", "local", ActionExec) {
		t.Fatal("expected alice to execute dev/local")
	}
	if store.UserCan(userAuth.UserID, "prod", "local", ActionExec) {
		t.Fatal("alice must not execute prod/local")
	}
	if store.UserCan(userAuth.UserID, "dev", "local", ActionWrite) {
		t.Fatal("targets:list must not imply write access")
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

	for _, action := range defaultGatewayUserActions {
		if store.UserCan(auth.UserID, "any-cluster", "any-node", action) {
			t.Fatalf("new user without grants must not have target action %q", action)
		}
	}
	if store.UserCan(auth.UserID, "any-cluster", "any-node", ActionAdmin) {
		t.Fatal("default target access must not imply gateway admin access")
	}
}

func TestGatewayStoreTargetUseGrantsStandardTargetAccessOnly(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	user, err := store.CreateUser("target-user")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster: "edu", Instance: "coder", Actions: []GatewayAction{ActionTargetUse},
	}); err != nil {
		t.Fatalf("grant target: %v", err)
	}
	for _, action := range []GatewayAction{
		ActionTargetsList, ActionExec, ActionAsk, ActionRead, ActionWrite,
		ActionPush, ActionPull, ActionInfo, ActionCheck, ActionClean,
		ActionCredentialUse,
	} {
		if !store.UserCan(user.ID, "edu", "coder", action) {
			t.Fatalf("target use must allow %q", action)
		}
	}
	for _, action := range []GatewayAction{ActionAdmin, ActionAgentUpgrade, ActionCredentialGrant} {
		if store.UserCan(user.ID, "edu", "coder", action) {
			t.Fatalf("target use must not allow %q", action)
		}
	}
	if store.UserCan(user.ID, "prod", "coder", ActionExec) {
		t.Fatal("target use must remain scoped to its target")
	}
}

func TestGatewayStoreImplicitGrantExcludesAgentUpgrade(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	user, err := store.CreateUser("operator")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "prod", Instance: "node-1"}); err != nil {
		t.Fatalf("grant user: %v", err)
	}
	if !store.UserCan(user.ID, "prod", "node-1", ActionExec) {
		t.Fatal("implicit grant should retain ordinary operator actions")
	}
	if store.UserCan(user.ID, "prod", "node-1", ActionAgentUpgrade) {
		t.Fatal("agent upgrade must require an explicit privileged grant")
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
		Actions:  []GatewayAction{ActionExec},
	}); err != nil {
		t.Fatalf("grant alice exec: %v", err)
	}
	if err := store.GrantUser(alice.ID, ScopeGrant{
		Cluster:  "dev",
		Instance: "node-2",
		Actions:  []GatewayAction{ActionRead},
	}); err != nil {
		t.Fatalf("grant alice read: %v", err)
	}
	if err := store.GrantUser(bob.ID, ScopeGrant{
		Cluster:  "*",
		Instance: "*",
		Actions:  []GatewayAction{ActionAdmin},
	}); err != nil {
		t.Fatalf("grant bob admin: %v", err)
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

func TestGatewayStoreRetiresRunningUpgradeOperations(t *testing.T) {
	store, err := OpenGatewayStore(t.TempDir() + "/gateway.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	now := formatTime(time.Now().UTC())
	if _, err := store.db.Exec(`INSERT INTO upgrade_operations
		(id, cluster, instance, image, old_generation, old_runtime_id, status, phase, error, started_at, updated_at)
		VALUES ('upg_test', 'dev', 'local', 'registry.example/doops@sha256:abc', 3, 'runtime-old',
		        'running', 'waiting_reconnect', '', ?, ?)`, now, now); err != nil {
		t.Fatalf("seed legacy upgrade operation: %v", err)
	}
	const reason = interruptedUpgradeRetiredReason
	if err := store.RetireRunningUpgradeOperations(reason); err != nil {
		t.Fatalf("retire running upgrades: %v", err)
	}
	var phase, status, errorText string
	if err := store.db.QueryRow(`SELECT phase, status, error FROM upgrade_operations WHERE id = 'upg_test'`).
		Scan(&phase, &status, &errorText); err != nil {
		t.Fatalf("read retired upgrade operation: %v", err)
	}
	if phase != "self_upgrade_retired" || status != "error" || errorText != reason {
		t.Fatalf("unexpected retired operation: phase=%q status=%q error=%q", phase, status, errorText)
	}
}
