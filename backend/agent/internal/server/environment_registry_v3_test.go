package server

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnvironmentRegistryV3SelectsOneService(t *testing.T) {
	root := writeEnvironmentRegistryV3Fixture(t)
	inputs := map[string]string{
		"environment": "oilan",
		"service":     "frontend",
		"reason":      "release-test",
	}
	plan, err := parseCredentialPlanForInputs(root, "deploy/environments.yaml", inputs)
	if err != nil {
		t.Fatalf("parse v3 registry plan: %v", err)
	}
	if plan.Template != "zhiyong-oilan-frontend" ||
		plan.Project != "zhiyong" ||
		plan.Environment != "oilan" ||
		plan.Target != "gw-edu-coder" ||
		plan.Cluster != "doops-edu" ||
		plan.Instance != "edu-coder" ||
		plan.Session != "zhiyong-oilan-release" {
		t.Fatalf("unexpected v3 registry plan: %#v", plan)
	}
	if len(plan.CredentialRefs) != 1 {
		t.Fatalf("credential refs = %#v", plan.CredentialRefs)
	}
	ref := plan.CredentialRefs[0]
	if ref.Name != "target-regcred" ||
		ref.Use != CredentialUseImagePull ||
		ref.Namespace != "oilan" ||
		ref.RegistryRepository != "oilan/zhiyong-frontend" ||
		ref.RegistryReference != "sha256:"+strings.Repeat("a", 64) ||
		ref.Workload.Kind != "deployment" ||
		ref.Workload.Name != "zhiyong-frontend" {
		t.Fatalf("unexpected runtime credential ref: %#v", ref)
	}
}

func TestEnvironmentRegistryV3RequiresExplicitSelection(t *testing.T) {
	root := writeEnvironmentRegistryV3Fixture(t)
	if _, err := parseCredentialPlanForInputs(
		root,
		"deploy/environments.yaml",
		map[string]string{"environment": "oilan"},
	); err == nil || !strings.Contains(err.Error(), "requires environment and service inputs") {
		t.Fatalf("missing service input error = %v", err)
	}
}

func TestEnvironmentRegistryV3AllowsPublicRegistriesWithoutCredentials(t *testing.T) {
	root := writeEnvironmentRegistryV3Fixture(t)
	path := filepath.Join(root, "deploy", "environments.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.ReplaceAll(
		string(data),
		"      runtimeImagePullSecret: {kind: KubernetesSecret, name: target-regcred}\n",
		"",
	))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseCredentialPlanForInputs(root, "deploy/environments.yaml", map[string]string{
		"environment": "oilan",
		"service":     "frontend",
	}); err != nil {
		t.Fatalf("public registry plan: %v", err)
	}
}

func TestEnvironmentRegistryV3RejectsUnsupportedSourceAndTargetCredentials(t *testing.T) {
	for _, field := range []string{"sourceCredentialRef", "targetCredentialRef"} {
		t.Run(field, func(t *testing.T) {
			root := writeEnvironmentRegistryV3Fixture(t)
			path := filepath.Join(root, "deploy", "environments.yaml")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.ReplaceAll(
				string(data),
				"      runtimeImagePullSecret: {kind: KubernetesSecret, name: target-regcred}\n",
				"      "+field+": {kind: KubernetesSecret, name: registry-credential}\n"+
					"      runtimeImagePullSecret: {kind: KubernetesSecret, name: target-regcred}\n",
			))
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := parseCredentialPlanForInputs(root, "deploy/environments.yaml", map[string]string{
				"environment": "oilan",
				"service":     "frontend",
			}); err == nil || !strings.Contains(err.Error(), field+" is not supported") {
				t.Fatalf("unsupported credential reference error = %v", err)
			}
		})
	}
}

func TestEnvironmentRegistryV3RejectsPartialCredentialReference(t *testing.T) {
	root := writeEnvironmentRegistryV3Fixture(t)
	path := filepath.Join(root, "deploy", "environments.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.ReplaceAll(
		string(data),
		"      runtimeImagePullSecret: {kind: KubernetesSecret, name: target-regcred}\n",
		"      runtimeImagePullSecret: {kind: KubernetesSecret}\n",
	))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseCredentialPlanForInputs(root, "deploy/environments.yaml", map[string]string{
		"environment": "oilan",
		"service":     "frontend",
	}); err == nil || !strings.Contains(err.Error(), "runtimeImagePullSecret is incomplete") {
		t.Fatalf("partial credential reference error = %v", err)
	}
}

func TestCredentialRegistryRepositoryAcceptsLocalRegistryPort(t *testing.T) {
	repository, err := credentialRegistryRepository("localhost:5000/team/app")
	if err != nil || repository != "team/app" {
		t.Fatalf("repository=%q err=%v", repository, err)
	}
}

func TestRepositoryEnvironmentRegistryV3CompilesOilanDoopsAgent(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
	plan, err := parseCredentialPlanForInputs(root, "deploy/environments.yaml", map[string]string{
		"environment": "oilan",
		"service":     "doops-agent",
	})
	if err != nil {
		t.Fatalf("compile repository Oilan declaration: %v", err)
	}
	if plan.Template != "doops-oilan-doops-agent" ||
		plan.Target != "gw-edu-release-runner" ||
		plan.Cluster != "doops-edu" ||
		plan.Instance != "release-runner" ||
		plan.Session != "doops-oilan-release" {
		t.Fatalf("unexpected repository plan: %#v", plan)
	}
	if len(plan.CredentialRefs) != 0 {
		t.Fatalf("repository credential refs = %#v", plan.CredentialRefs)
	}
}

func writeEnvironmentRegistryV3Fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		if err := os.WriteFile(absolute, []byte(content), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", path, err)
		}
	}
	write("deploy/environments.yaml", `
apiVersion: doops.sh/v3
kind: EnvironmentRegistry
metadata:
  name: zhiyong
environments:
  oilan:
    target:
      name: gw-edu-coder
      cluster: doops-edu
      instance: edu-coder
      session: zhiyong-oilan-release
    source:
      repository: https://example.test/zhiyong.git
      branch: main
      build: deploy/deploy.yaml
    helm:
      release: zhiyong
      namespace: oilan
      chart: deploy/environments/oilan/chart
      values: deploy/environments/oilan/chart/values.yaml
      atomic: true
      wait: true
    artifacts:
      targetRegistry: registry.example.test/oilan
      runtimeImagePullSecret: {kind: KubernetesSecret, name: target-regcred}
    services:
      frontend: deploy/environments/oilan/services/zhiyong-frontend.yaml
    healthChecks:
      - https://app.example.test/healthz
`)
	write("deploy/environments/oilan/services/zhiyong-frontend.yaml", `
apiVersion: doops.sh/v3
kind: EnvironmentService
metadata:
  name: zhiyong-frontend
  environment: oilan
spec:
  serviceDefinition: deploy/services/zhiyong-frontend.yaml
  artifact:
    source: source.example.test/zhiyong-frontend
    target: registry.example.test/oilan/zhiyong-frontend
    identity: sha256-digest
    digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  helm:
    chart: deploy/environments/oilan/chart
    values: deploy/environments/oilan/chart/values.yaml
    value: frontend
    enabled: true
    replicas: 2
    workload: deployment/zhiyong-frontend
    container: zhiyong-frontend
  configuration:
    valuesPath: frontend
    files: []
  verification:
    minReadyReplicas: 2
    requireEndpoints: true
    publicUrl: https://app.example.test/healthz
`)
	write("deploy/services/zhiyong-frontend.yaml", `
apiVersion: doops.sh/v3
kind: ServiceDefinition
metadata:
  name: zhiyong-frontend
spec:
  service: frontend
  artifact: zhiyong-frontend
  buildManifest: deploy/deploy.yaml
  sourceContext: frontend
  dockerfile: frontend/Dockerfile
  helm:
    value: frontend
    workload: deployment/zhiyong-frontend
    container: zhiyong-frontend
  verification:
    endpointRequired: true
`)
	write("deploy/deploy.yaml", `
apiVersion: doops.sh/v3
kind: BuildDefinition
metadata:
  name: zhiyong
spec:
  services:
    frontend:
      context: frontend
      dockerfile: frontend/Dockerfile
      image: source.example.test/zhiyong-frontend
`)
	write("deploy/environments/oilan/chart/values.yaml", "frontend: {}\n")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve fixture root: %v", err)
	}
	return resolved
}

func TestEnvironmentRegistryV3RejectsInconsistentBuildDefinition(t *testing.T) {
	root := writeEnvironmentRegistryV3Fixture(t)
	path := filepath.Join(root, "deploy", "deploy.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.ReplaceAll(
		string(data),
		"      dockerfile: frontend/Dockerfile\n",
		"      dockerfile: frontend/Other.Dockerfile\n",
	))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parseCredentialPlanForInputs(root, "deploy/environments.yaml", map[string]string{
		"environment": "oilan",
		"service":     "frontend",
	}); err == nil || !strings.Contains(err.Error(), "build definition is incomplete or inconsistent") {
		t.Fatalf("inconsistent build definition error = %v", err)
	}
}
