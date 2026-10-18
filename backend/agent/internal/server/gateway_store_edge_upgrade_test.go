package server

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenGatewayStoreFreshInstallCreatesEdgeSchema(t *testing.T) {
	store := openEdgeTestStore(t)
	for _, table := range []string{
		"edge_routes",
		"edge_route_revision",
		"edge_enrollments",
		"edge_agent_status",
		"users",
		"tokens",
	} {
		var count int
		if err := store.db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&count); err != nil {
			t.Fatalf("query %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("fresh install missing table %s", table)
		}
	}
	if _, err := store.CreateEdgeEnrollment("jm", "gw-jm"); err != nil {
		t.Fatalf("fresh install must accept an edge enrollment: %v", err)
	}
}

func TestOpenGatewayStoreUpgradesPreEdgeDatabaseWithoutBreakingOpsTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenGatewayStore(path)
	if err != nil {
		t.Fatalf("install store: %v", err)
	}
	user, err := store.CreateUser("alice")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, err := store.CreateToken(CreateTokenRequest{
		Kind:      TokenKindUser,
		UserID:    user.ID,
		Name:      "alice laptop",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if err := store.GrantUser(user.ID, ScopeGrant{
		Cluster:  "jm",
		Instance: "gw-jm",
	}); err != nil {
		t.Fatalf("grant user: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close installed store: %v", err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	for _, table := range []string{
		"edge_agent_status",
		"edge_enrollments",
		"edge_routes",
		"edge_route_revision",
	} {
		if _, err := raw.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			t.Fatalf("drop %s to simulate a pre-edge database: %v", table, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw sqlite: %v", err)
	}

	upgraded, err := OpenGatewayStore(path)
	if err != nil {
		t.Fatalf("upgrade store: %v", err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })

	auth, err := upgraded.VerifyUserToken(token.Plaintext)
	if err != nil {
		t.Fatalf("ops token after upgrade: %v", err)
	}
	if auth.UserID != user.ID {
		t.Fatalf("upgraded token user = %s, want %s", auth.UserID, user.ID)
	}
	if !upgraded.UserCanUseTarget(auth.UserID, "jm", "gw-jm") {
		t.Fatal("upgrade must keep the existing target grant")
	}
	if _, err := upgraded.CreateEdgeEnrollment("jm", "gw-jm"); err != nil {
		t.Fatalf("upgrade must create edge enrollments: %v", err)
	}
	if _, err := upgraded.CreateEdgeRoute(CreateEdgeRouteRequest{
		Hostname: "amp.jm.aiedulab.cn",
		Cluster:  "jm",
		Instance: "gw-jm",
		Backend:  "192.168.150.228:443",
	}); err != nil {
		t.Fatalf("upgrade must accept an edge route: %v", err)
	}
}
