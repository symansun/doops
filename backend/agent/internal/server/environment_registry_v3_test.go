package server

import (
	"os"
	"path/filepath"
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
		plan.Instance != "edu-coder" {
		t.Fatalf("unexpected v3 registry plan: %#v", plan)
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
      sourceCredentialRef: {kind: KubernetesSecret, name: source-regcred}
      targetCredentialRef: {kind: KubernetesSecret, name: target-regcred}
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
	write("deploy/deploy.yaml", "apiVersion: doops.sh/v3\nkind: BuildDefinition\n")
	write("deploy/environments/oilan/chart/values.yaml", "frontend: {}\n")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve fixture root: %v", err)
	}
	return resolved
}
