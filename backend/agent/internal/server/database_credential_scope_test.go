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

func TestDatabaseCredentialNamesSelectOnlyTargetDatabaseBindings(t *testing.T) {
	entries := []CredentialListEntry{
		{CredentialResource: CredentialResource{Name: "postgres", Type: CredentialTypeOpaque, State: CredentialStateActive}, Grants: []CredentialGrant{{Scope: CredentialGrantScope{Use: CredentialUseDatabase}}}},
		{CredentialResource: CredentialResource{Name: "postgres", Type: CredentialTypeOpaque, State: CredentialStateActive}, Grants: []CredentialGrant{{Scope: CredentialGrantScope{Use: CredentialUseDatabase}}}},
		{CredentialResource: CredentialResource{Name: "opaque", Type: CredentialTypeOpaque, State: CredentialStateActive}, Grants: []CredentialGrant{{Scope: CredentialGrantScope{Use: CredentialUseOpaqueSecret}}}},
		{CredentialResource: CredentialResource{Name: "registry", Type: CredentialTypeRegistry, State: CredentialStateActive}, Grants: []CredentialGrant{{Scope: CredentialGrantScope{Use: CredentialUseImagePull}}}},
		{CredentialResource: CredentialResource{Name: "revoked", Type: CredentialTypeOpaque, State: CredentialStateRevoked}, Grants: []CredentialGrant{{Scope: CredentialGrantScope{Use: CredentialUseDatabase}}}},
	}
	got := databaseCredentialNames(entries)
	if len(got) != 1 || got[0] != "postgres" {
		t.Fatalf("database credential names = %#v, want [postgres]", got)
	}
}
