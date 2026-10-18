package server

import (
	"path/filepath"
	"testing"
)

func TestUserCanUseTarget(t *testing.T) {
	t.Parallel()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	mustUser := func(name string) string {
		t.Helper()
		user, err := store.CreateUser(name)
		if err != nil {
			t.Fatal(err)
		}
		return user.ID
	}
	exact := mustUser("exact")
	clusterWide := mustUser("cluster-wide")
	admin := mustUser("admin")
	disabled := mustUser("disabled")
	none := mustUser("none")

	for _, g := range []struct {
		user  string
		grant ScopeGrant
	}{
		{exact, ScopeGrant{Cluster: "dev", Instance: "local"}},
		{clusterWide, ScopeGrant{Cluster: "dev", Instance: "*"}},
		{disabled, ScopeGrant{Cluster: "dev", Instance: "local"}},
	} {
		if err := store.GrantUser(g.user, g.grant); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetUserAdmin(admin, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserDisabled(disabled, true); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name              string
		user              string
		cluster, instance string
		want              bool
	}{
		{"exact grant matches", exact, "dev", "local", true},
		{"exact grant other instance", exact, "dev", "other", false},
		{"exact grant other cluster", exact, "prod", "local", false},
		{"instance wildcard matches", clusterWide, "dev", "any", true},
		{"instance wildcard keeps cluster", clusterWide, "prod", "any", false},
		{"admin uses every target", admin, "prod", "node", true},
		{"disabled user denied", disabled, "dev", "local", false},
		{"no grant denied", none, "dev", "local", false},
		{"empty cluster denied", exact, "", "local", false},
		{"empty instance denied", exact, "dev", "", false},
		{"empty user denied", "", "dev", "local", false},
	}
	for _, tt := range tests {
		if got := store.UserCanUseTarget(tt.user, tt.cluster, tt.instance); got != tt.want {
			t.Errorf("%s: UserCanUseTarget(%s/%s) = %v, want %v", tt.name, tt.cluster, tt.instance, got, tt.want)
		}
	}
	if store.UserIsPlatformAdmin(exact) || !store.UserIsPlatformAdmin(admin) {
		t.Fatal("admin flag must come from the user, not from target grants")
	}
}

func TestGrantUserIsIdempotent(t *testing.T) {
	t.Parallel()
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.CreateUser("repeat")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := store.GrantUser(user.ID, ScopeGrant{Cluster: "dev", Instance: "local"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := store.countGrants(user.ID); n != 1 {
		t.Fatalf("grant count = %d, want 1", n)
	}
	if err := store.GrantUser("", ScopeGrant{Cluster: "dev", Instance: "local"}); err == nil {
		t.Fatal("empty user id must be rejected")
	}
}

func TestMigrateGrantActionsConvertsLegacyGrants(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "gateway.db")
	store, err := OpenGatewayStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, name := range []string{"exec-user", "platform-admin", "empty-grant", "duplicate", "target-admin"} {
		user, err := store.CreateUser(name)
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = user.ID
	}
	// Recreate the pre-migration schema so the next open runs the conversion.
	for _, stmt := range []string{
		`DROP TABLE grants`,
		`CREATE TABLE grants (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			actions_json TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
	} {
		if _, err := store.db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		user, cluster, instance, actions string
	}{
		{"exec-user", "dev", "local", `["exec"]`},
		{"platform-admin", "*", "*", `["admin"]`},
		{"empty-grant", "dev", "local", `[]`},
		{"duplicate", "dev", "local", `["read"]`},
		{"duplicate", "dev", "local", `["exec","write"]`},
		{"target-admin", "dev", "local", `["admin"]`},
	} {
		if _, err := store.db.Exec(`INSERT INTO grants (user_id, cluster, instance, actions_json, created_at)
			VALUES (?, ?, ?, ?, '2026-01-01T00:00:00Z')`, ids[row.user], row.cluster, row.instance, row.actions); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	for pass := 1; pass <= 2; pass++ {
		migrated, err := OpenGatewayStore(path)
		if err != nil {
			t.Fatalf("pass %d: reopen: %v", pass, err)
		}
		if legacy, err := migrated.tableHasColumn("grants", "actions_json"); err != nil || legacy {
			t.Fatalf("pass %d: actions_json still present (err=%v)", pass, err)
		}
		checks := []struct {
			name              string
			user              string
			cluster, instance string
			want              bool
		}{
			{"exec grant keeps its target", "exec-user", "dev", "local", true},
			{"exec grant stays scoped", "exec-user", "prod", "local", false},
			{"platform admin keeps every target", "platform-admin", "prod", "node", true},
			{"empty grant is dropped", "empty-grant", "dev", "local", false},
			{"duplicate rows still grant", "duplicate", "dev", "local", true},
			{"target admin keeps its target", "target-admin", "dev", "local", true},
			{"target admin is not platform-wide", "target-admin", "prod", "node", false},
		}
		for _, c := range checks {
			if got := migrated.UserCanUseTarget(ids[c.user], c.cluster, c.instance); got != c.want {
				t.Errorf("pass %d: %s: got %v, want %v", pass, c.name, got, c.want)
			}
		}
		if !migrated.UserIsPlatformAdmin(ids["platform-admin"]) {
			t.Errorf("pass %d: */* admin grant must become the admin flag", pass)
		}
		if migrated.UserIsPlatformAdmin(ids["target-admin"]) || migrated.UserIsPlatformAdmin(ids["exec-user"]) {
			t.Errorf("pass %d: scoped grants must not become platform admin", pass)
		}
		if n := migrated.countGrants(ids["duplicate"]); n != 1 {
			t.Errorf("pass %d: duplicate legacy rows produced %d grants, want 1", pass, n)
		}
		if err := migrated.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
