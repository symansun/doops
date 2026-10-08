package server

import "testing"

func TestDatabaseCredentialScopeRequiresExactAssetAndCanonicalKeys(t *testing.T) {
	base := CredentialGrantScope{
		Cluster: "cluster-a", Instance: "instance-a", Namespace: "apps",
		Use: CredentialUseDatabase, Workload: CredentialPlanWorkload{Kind: "deployment", Name: "orders"},
		DatabaseAssetID: "db_orders", EnvironmentID: "prod", DatabaseEngine: "postgres", DatabaseName: "orders",
		RequiredKeys: []string{"connection_uri"},
	}
	if err := validateCredentialGrantScope(base); err != nil {
		t.Fatalf("valid database scope rejected: %v", err)
	}
	for name, mutate := range map[string]func(*CredentialGrantScope){
		"missing asset":   func(scope *CredentialGrantScope) { scope.DatabaseAssetID = "" },
		"wrong engine":    func(scope *CredentialGrantScope) { scope.DatabaseEngine = "sqlite" },
		"unsafe database": func(scope *CredentialGrantScope) { scope.DatabaseName = "orders;drop" },
		"wrong keys":      func(scope *CredentialGrantScope) { scope.RequiredKeys = []string{"password"} },
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

func TestDatabaseCredentialTypeReusesOpaqueMaterializationOnly(t *testing.T) {
	if err := credentialTypeAllowsUse(CredentialTypeOpaque, CredentialUseDatabase); err != nil {
		t.Fatalf("opaque database credential rejected: %v", err)
	}
	if err := credentialTypeAllowsUse(CredentialTypeRegistry, CredentialUseDatabase); err == nil {
		t.Fatal("registry credential was accepted for database use")
	}
}
