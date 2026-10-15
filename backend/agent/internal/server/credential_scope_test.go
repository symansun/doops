package server

import "testing"

func TestHelmCredentialScopeOnlyBindsTargetAndUse(t *testing.T) {
	base := CredentialGrantScope{
		Cluster: "cluster-a", Instance: "instance-a", Namespace: "apps", Use: CredentialUseHelmPull,
		Workload: CredentialPlanWorkload{Kind: "deployment", Name: "frontend"}, HelmRelease: "zhiyong",
		HelmChart: "https://helm.cnb.cool/charts/team/app/v/0.1.0-zyj.gold.tgz",
	}
	for name, mutate := range map[string]func(*CredentialGrantScope){
		"namespace": func(scope *CredentialGrantScope) { scope.Namespace = "other" },
		"workload": func(scope *CredentialGrantScope) {
			scope.Workload = CredentialPlanWorkload{Kind: "statefulset", Name: "other"}
		},
		"release": func(scope *CredentialGrantScope) { scope.HelmRelease = "other" },
		"chart": func(scope *CredentialGrantScope) {
			scope.HelmChart = "https://other.example/charts/other/v/1.0.0.tgz"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			if !credentialScopeEqual(base, changed) {
				t.Fatal("unrelated Helm scope field blocked target-scoped credential use")
			}
		})
	}
}

func TestHelmCredentialScopeKeepsTargetExact(t *testing.T) {
	base := CredentialGrantScope{
		Cluster: "cluster-a", Instance: "instance-a", Namespace: "apps", Use: CredentialUseHelmPull,
		Workload: CredentialPlanWorkload{Kind: "deployment", Name: "frontend"}, HelmRelease: "zhiyong",
		HelmChart: "https://helm.cnb.cool/charts/team/app/v/0.1.0-zyj.gold.tgz",
	}
	for name, mutate := range map[string]func(*CredentialGrantScope){
		"cluster":  func(scope *CredentialGrantScope) { scope.Cluster = "cluster-b" },
		"instance": func(scope *CredentialGrantScope) { scope.Instance = "instance-b" },
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
		CredentialID: resource.ID, Scope: base, CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	rotated := base
	rotated.Namespace = "other"
	rotated.Workload = CredentialPlanWorkload{Kind: "statefulset", Name: "other"}
	rotated.HelmRelease = "other"
	rotated.HelmChart = "https://other.example/charts/other/v/1.0.0.tgz"
	second, err := store.CreateCredentialGrant(CredentialGrantCreateRequest{
		CredentialID: resource.ID, Scope: rotated, CreatedBy: owner.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Scope.HelmChart != rotated.HelmChart || second.Scope.Namespace != rotated.Namespace {
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
