package server

import "testing"

func TestHelmCredentialScopeAllowsVersionRotationWithinExactChartPath(t *testing.T) {
	base := CredentialGrantScope{
		Cluster: "cluster-a", Instance: "instance-a", Namespace: "apps", Use: CredentialUseHelmPull,
		Workload: CredentialPlanWorkload{Kind: "deployment", Name: "frontend"}, HelmRelease: "zhiyong",
	}
	oldScope := base
	oldScope.HelmChart = "https://helm.cnb.cool/charts/team/app/v/0.1.0-zyj.gold.tgz"
	newScope := base
	newScope.HelmChart = "https://helm.cnb.cool/charts/team/app/v/0.1.0-zyj.gnew.tgz"
	if !credentialScopeEqual(oldScope, newScope) {
		t.Fatal("version rotation within one chart path was rejected")
	}
	if got := helmChartScopeIdentity(oldScope.HelmChart); got != "https://helm.cnb.cool/charts/team/app" {
		t.Fatalf("unexpected chart identity: %s", got)
	}
}

func TestHelmCredentialScopeKeepsReleaseAndChartPathExact(t *testing.T) {
	base := CredentialGrantScope{
		Cluster: "cluster-a", Instance: "instance-a", Namespace: "apps", Use: CredentialUseHelmPull,
		Workload: CredentialPlanWorkload{Kind: "deployment", Name: "frontend"}, HelmRelease: "zhiyong",
		HelmChart: "https://helm.cnb.cool/charts/team/app/v/0.1.0-zyj.gold.tgz",
	}
	for name, mutate := range map[string]func(*CredentialGrantScope){
		"release": func(scope *CredentialGrantScope) { scope.HelmRelease = "other" },
		"repository path": func(scope *CredentialGrantScope) {
			scope.HelmChart = "https://helm.cnb.cool/charts/team/other/v/0.1.0-zyj.gnew.tgz"
		},
		"host": func(scope *CredentialGrantScope) {
			scope.HelmChart = "https://other.example/charts/team/app/v/0.1.0-zyj.gnew.tgz"
		},
		"non-versioned artifact": func(scope *CredentialGrantScope) {
			scope.HelmChart = "https://helm.cnb.cool/charts/team/app/0.1.0-zyj.gnew.tgz"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if credentialScopeEqual(base, changed) {
				t.Fatal("unrelated Helm scope change was accepted")
			}
		})
	}
}

func TestHelmCredentialGrantRotationReusesOneActiveGrant(t *testing.T) {
	t.Setenv("DOOPS_GATEWAY_SECRET_KEY", "credential-scope-test-master-key-32bytes")
	store := openCredentialTestStore(t)
	owner := createCredentialTestUser(t, store, "helm-owner")
	if err := store.GrantUser(owner.ID, ScopeGrant{
		Cluster: "*", Instance: "*", Actions: []GatewayAction{ActionAdmin},
	}); err != nil {
		t.Fatal(err)
	}
	resource, err := store.CreateCredential(CredentialCreateRequest{
		Name: "zhiyong-helm", Scope: CredentialScopePersonal, Type: CredentialTypeHelmRepository,
		OwnerID: owner.ID, CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := CredentialGrantScope{
		Cluster: "cluster-a", Instance: "instance-a", Namespace: "apps", Use: CredentialUseHelmPull,
		Workload: CredentialPlanWorkload{Kind: "deployment", Name: "frontend"}, HelmRelease: "zhiyong",
		HelmChart: "https://helm.cnb.cool/charts/team/app/v/0.1.0-zyj.gold.tgz",
	}
	first, err := store.CreateCredentialGrant(CredentialGrantCreateRequest{
		CredentialID: resource.ID, GranteeID: owner.ID, Scope: base, CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	rotated := base
	rotated.HelmChart = "https://helm.cnb.cool/charts/team/app/v/0.1.0-zyj.gnew.tgz"
	second, err := store.CreateCredentialGrant(CredentialGrantCreateRequest{
		CredentialID: resource.ID, GranteeID: owner.ID, Scope: rotated, CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Scope.HelmChart != base.HelmChart {
		t.Fatalf("Helm grant rotation created a new grant: first=%#v second=%#v", first, second)
	}
	grants, err := store.ActiveCredentialGrants(resource.ID)
	if err != nil || len(grants) != 1 {
		t.Fatalf("active grants after rotation = %#v, %v", grants, err)
	}
	matches, err := store.CredentialGrantsForContext(resource.ID, rotated)
	if err != nil || len(matches) != 1 || matches[0].ID != first.ID {
		t.Fatalf("rotated Helm grant was not reusable: %#v, %v", matches, err)
	}
}
