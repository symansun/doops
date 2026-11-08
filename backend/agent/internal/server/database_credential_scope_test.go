package server

import "testing"

func TestDatabaseCredentialScopeOnlyBindsTargetAndMaterializationNamespace(t *testing.T) {
	base := CredentialGrantScope{
		Cluster: "cluster-a", Instance: "instance-a", Namespace: "apps", Use: CredentialUseDatabase,
	}
	if err := validateCredentialGrantScope(base); err != nil {
		t.Fatalf("valid database scope rejected: %v", err)
	}
	for name, mutate := range map[string]func(*CredentialGrantScope){
		"missing cluster":  func(scope *CredentialGrantScope) { scope.Cluster = "" },
		"missing instance": func(scope *CredentialGrantScope) { scope.Instance = "" },
		"invalid use":      func(scope *CredentialGrantScope) { scope.Use = CredentialUse("invalid") },
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			changed.RequiredKeys = append([]string(nil), base.RequiredKeys...)
			mutate(&changed)
			if err := validateCredentialGrantScope(changed); err == nil {
				t.Fatal("invalid database scope was accepted")
			}
		})
	}
}

func TestDatabaseCredentialScopeIgnoresDoSQLExecutionContext(t *testing.T) {
	minimal := CredentialGrantScope{Cluster: "cluster-a", Instance: "instance-a", Namespace: "apps", Use: CredentialUseDatabase}
	withContext := minimal
	withContext.Workload = CredentialPlanWorkload{Kind: "deployment", Name: "orders"}
	withContext.DatabaseAssetID = "db_orders"
	withContext.EnvironmentID = "prod"
	withContext.DatabaseEngine = "postgres"
	withContext.DatabaseName = "orders"
	withContext.RequiredKeys = []string{"connection_uri"}
	if !credentialScopeEqual(minimal, withContext) {
		t.Fatal("database grant equality must ignore DoSql execution context")
	}
}

func TestDatabaseCredentialTypeReusesOpaqueMaterializationOnly(t *testing.T) {
	if err := credentialTypeAllowsUse(CredentialTypeOpaque, CredentialUseDatabase); err != nil {
		t.Fatalf("opaque database credential rejected: %v", err)
	}
	if err := credentialTypeAllowsUse(CredentialTypeRegistry, CredentialUseDatabase); err == nil {
		t.Fatal("registry credential was accepted for database use")
	}
}

func TestDatabaseCredentialIDsSelectOnlyTargetDatabaseBindings(t *testing.T) {
	database := []CredentialGrant{{Scope: CredentialGrantScope{Use: CredentialUseDatabase}}}
	entries := []CredentialListEntry{
		{CredentialResource: CredentialResource{ID: "cred_a", Name: "postgres", Type: CredentialTypeOpaque, State: CredentialStateActive}, Grants: database},
		// Same resource listed twice (two grants) is selected once.
		{CredentialResource: CredentialResource{ID: "cred_a", Name: "postgres", Type: CredentialTypeOpaque, State: CredentialStateActive}, Grants: database},
		// Same name, different owner: still a distinct credential, selected by ID.
		{CredentialResource: CredentialResource{ID: "cred_b", Name: "postgres", Type: CredentialTypeOpaque, State: CredentialStateActive}, Grants: database},
		{CredentialResource: CredentialResource{ID: "cred_c", Name: "opaque", Type: CredentialTypeOpaque, State: CredentialStateActive}, Grants: []CredentialGrant{{Scope: CredentialGrantScope{Use: CredentialUseOpaqueSecret}}}},
		{CredentialResource: CredentialResource{ID: "cred_d", Name: "registry", Type: CredentialTypeRegistry, State: CredentialStateActive}, Grants: []CredentialGrant{{Scope: CredentialGrantScope{Use: CredentialUseImagePull}}}},
		{CredentialResource: CredentialResource{ID: "cred_e", Name: "revoked", Type: CredentialTypeOpaque, State: CredentialStateRevoked}, Grants: database},
	}
	got := databaseCredentialIDs(entries)
	if len(got) != 2 || got[0] != "cred_a" || got[1] != "cred_b" {
		t.Fatalf("database credential ids = %#v, want [cred_a cred_b]", got)
	}
}
