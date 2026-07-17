package server

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialScalarRejectsKnownSecretFormats(t *testing.T) {
	for _, value := range []string{
		"ghp_" + strings.Repeat("a", 36),
		"github_pat_" + strings.Repeat("a", 82),
		"glpat-" + strings.Repeat("a", 20),
		"AKIA" + strings.Repeat("A", 16),
		"https://user:synthetic@example.test/repo.git",
		"https://example.test/repo.git?access_token=synthetic",
		"https://example.test/repo.git?X-Goog-Signature=synthetic",
		"scheduled release Bearer synthetic-canary",
		"-----BEGIN PRIVATE KEY-----",
	} {
		if !credentialScalarContainsInlineMaterial(value) {
			t.Fatalf("inline credential scalar %q was accepted", value)
		}
	}
	for _, value := range []string{
		"https://example.test/repo.git",
		"release-20260716",
		"sha256:" + strings.Repeat("a", 64),
	} {
		if credentialScalarContainsInlineMaterial(value) {
			t.Fatalf("ordinary scalar %q was rejected", value)
		}
	}
}

func TestParseCredentialPlanRejectsV2WorkflowContracts(t *testing.T) {
	for _, kind := range []string{"DeploymentTemplate", "SemanticRelease"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			writeCredentialPlanTestFile(t, root, "deploy/release.yaml", `
apiVersion: doops.sh/v2
kind: `+kind+`
metadata:
  name: obsolete-release
`)
			if _, err := parseCredentialPlan(root, "deploy/release.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
				t.Fatalf("v2 %s error = %v, want ErrCredentialDeclarationInvalid", kind, err)
			}
		})
	}
}

func TestRepositoryDoesNotContainLegacyDeploymentWorkflows(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	workflowPaths, err := filepath.Glob(filepath.Join(repositoryRoot, "backend", "deploy", "workflows", "*.yaml"))
	if err != nil {
		t.Fatalf("list repository workflows: %v", err)
	}
	if len(workflowPaths) != 0 {
		t.Fatalf("legacy deployment workflows remain: %v", workflowPaths)
	}
}

func TestExternalRepositoryEnvironmentRegistryCompiles(t *testing.T) {
	repositoryRoot := strings.TrimSpace(os.Getenv("DOOPS_CREDENTIAL_CONTRACT_ROOT"))
	if repositoryRoot == "" {
		t.Skip("DOOPS_CREDENTIAL_CONTRACT_ROOT is not set")
	}
	manifestPath := "deploy/environments.yaml"
	data, err := readCredentialDeclarationFile(repositoryRoot, manifestPath)
	if err != nil {
		t.Fatalf("read external environment registry: %v", err)
	}
	var registry credentialEnvironmentRegistryV3
	if err := strictCredentialYAMLDecode(data, &registry); err != nil {
		t.Fatalf("decode external environment registry: %v", err)
	}
	if registry.APIVersion != "doops.sh/v3" || registry.Kind != "EnvironmentRegistry" {
		t.Fatalf("external repository must declare doops.sh/v3 EnvironmentRegistry")
	}
	for environmentName, environment := range registry.Environments {
		for serviceName := range environment.Services {
			t.Run(environmentName+"/"+serviceName, func(t *testing.T) {
				if _, err := parseCredentialPlanForInputs(repositoryRoot, manifestPath, map[string]string{
					"environment": environmentName,
					"service":     serviceName,
				}); err != nil {
					t.Fatalf("compile external environment service: %v", err)
				}
			})
		}
		if len(environment.Services) == 0 {
			t.Errorf("environment %q has no services", environmentName)
		}
	}
	if len(registry.Environments) == 0 {
		t.Fatal("external environment registry has no environments")
	}
}

func TestParseCredentialPlanRejectsInlineSecretLikeFieldsAtAnyDepth(t *testing.T) {
	for _, field := range []string{
		"token", "password", "auth", "data", "stringData",
		"clientSecret", "apiKey", "privateKey", "registryPassword",
	} {
		t.Run(field, func(t *testing.T) {
			root := t.TempDir()
			writeCredentialPlanTestFile(t, root, "release.yaml", `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: release
spec:
  application: app
  environment: production
  configurationSource: environments.yaml
  executor:
    nested:
      `+field+`: canary-secret
`)
			writeCredentialPlanTestFile(t, root, "environments.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: test-environments
environments:
  production:
    target:
      name: target
      cluster: cluster
      instance: instance
`)
			if _, err := parseCredentialPlan(root, "release.yaml"); !errors.Is(err, ErrInlineCredentialMaterial) {
				t.Fatalf("inline field %s error = %v, want ErrInlineCredentialMaterial", field, err)
			}
		})
	}
}

func TestParseCredentialPlanRejectsParameterDefaults(t *testing.T) {
	root := t.TempDir()
	writeCredentialPlanTestFile(t, root, "release.yaml", `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: release
spec:
  application: app
  environment: production
  configurationSource: environments.yaml
  parameters:
    registryPassword:
      required: false
      default: canary-secret
`)
	writeCredentialPlanTestFile(t, root, "environments.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: test-environments
environments:
  production:
    target:
      name: target
      cluster: cluster
      instance: instance
`)
	if _, err := parseCredentialPlan(root, "release.yaml"); !errors.Is(err, ErrInlineCredentialMaterial) {
		t.Fatalf("parameter default error = %v, want ErrInlineCredentialMaterial", err)
	}
}

func TestParseCredentialPlanRejectsImagePullWithoutWorkload(t *testing.T) {
	root := t.TempDir()
	writeCredentialPlanTestFile(t, root, "release.yaml", `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: release
spec:
  application: app
  environment: production
  configurationSource: environments.yaml
  credentialRefs:
    - name: registry
      use: imagePull
      namespace: app
      registryRepository: team/app
      registryReference: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
`)
	writeCredentialPlanTestFile(t, root, "environments.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: test-environments
environments:
  production:
    target:
      name: target
      cluster: cluster
      instance: instance
`)
	if _, err := parseCredentialPlan(root, "release.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("imagePull without workload error = %v, want ErrCredentialDeclarationInvalid", err)
	}
}

func TestParseCredentialPlanRejectsDuplicateKeysAndPathEscape(t *testing.T) {
	root := t.TempDir()
	writeCredentialPlanTestFile(t, root, "duplicate.yaml", `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: one
  name: two
spec:
  application: app
  environment: production
  configurationSource: environments.yaml
`)
	if _, err := parseCredentialPlan(root, "duplicate.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("duplicate YAML error = %v, want ErrCredentialDeclarationInvalid", err)
	}
	if _, err := parseCredentialPlan(root, "../outside.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("path escape error = %v, want ErrCredentialDeclarationInvalid", err)
	}
}

func TestParseCredentialPlanRejectsUnknownReferenceFields(t *testing.T) {
	root := t.TempDir()
	writeCredentialPlanTestFile(t, root, "release.yaml", `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: release
spec:
  application: app
  environment: production
  configurationSource: environments.yaml
  credentialRefs:
    - name: registry
      use: imagePull
      namespace: app
      secretValue: canary-secret
`)
	writeCredentialPlanTestFile(t, root, "environments.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: test-environments
environments:
  production:
    target:
      name: target
      cluster: cluster
      instance: instance
`)
	if _, err := parseCredentialPlan(root, "release.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("unknown credentialRef field error = %v, want ErrCredentialDeclarationInvalid", err)
	}
}

func TestParseCredentialPlanRejectsUnknownDeploymentTemplateFields(t *testing.T) {
	root := t.TempDir()
	writeCredentialPlanTestFile(t, root, "release.yaml", `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: release
spec:
  application: app
  environment: production
  configurationSource: environments.yaml
  unrecognized: value
`)
	writeCredentialPlanTestFile(t, root, "environments.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: test-environments
environments:
  production:
    target:
      name: target
      cluster: cluster
      instance: instance
`)
	if _, err := parseCredentialPlan(root, "release.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("unknown DeploymentTemplate field error = %v, want ErrCredentialDeclarationInvalid", err)
	}
}

func TestParseCredentialPlanRejectsEnvironmentRegistryFileIndirection(t *testing.T) {
	root := t.TempDir()
	writeCredentialV3EnvironmentFixture(t, root)
	writeCredentialPlanTestFile(t, root, "deploy/workflows/release.yaml", `
apiVersion: doops.sh/v2
kind: SemanticRelease
metadata:
  name: zhiyong-oilan-release
spec:
  application: zhiyong
  release:
    revision: ${inputs.revision}
    version: ${inputs.version}
    reason: ${inputs.reason}
  environment: oilan
  configurationSource: deploy/environments.yaml
  serviceCatalog:
    frontend:
      uses: services/frontend.yaml
  flow:
    - intent: establish-release-context
`)
	writeCredentialServiceReleaseFixture(
		t,
		root,
		"deploy/workflows/services/frontend.yaml",
		"frontend",
		"zhiyong",
	)
	writeCredentialPlanTestFile(t, root, "deploy/environments.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: environment-index
environments:
  oilan:
    file: deploy/environments/oilan/environment.yaml
`)

	if _, err := parseCredentialPlan(root, "deploy/workflows/release.yaml"); !errors.Is(
		err,
		ErrCredentialDeclarationInvalid,
	) {
		t.Fatalf("obsolete environment file indirection error = %v", err)
	}
}

func TestParseCredentialPlanRejectsV2EnvironmentDefinitionConfigurationSource(t *testing.T) {
	root := t.TempDir()
	writeCredentialPlanTestFile(t, root, "deploy/workflows/oilan.yaml", `
apiVersion: doops.sh/v2
kind: SemanticRelease
metadata:
  name: zhiyong-oilan-release
spec:
  parameters:
    reason:
      required: true
  application: zhiyong
  release:
    revision: ${inputs.revision}
  environment: oilan
  configurationSource: deploy/environments/oilan/environment.yaml
  serviceCatalog:
    frontend:
      uses: services/frontend.yaml
  flow:
    - intent: establish-release-context
`)
	writeCredentialServiceReleaseFixture(t, root, "deploy/workflows/services/frontend.yaml", "frontend", "zhiyong")
	writeCredentialPlanTestFile(t, root, "deploy/environments/oilan/environment.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentDefinition
metadata:
  name: oilan
spec:
  application: zhiyong
  environment: oilan
  execution:
    deploy:
      target: gw-edu-coder
      cluster: doops-edu
      instance: edu-coder
  artifacts:
    runtimeImagePullSecret:
      kind: KubernetesSecret
      name: regcred
`)

	if _, err := parseCredentialPlan(root, "deploy/workflows/oilan.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("v2 EnvironmentDefinition error = %v, want ErrCredentialDeclarationInvalid", err)
	}
}

func TestParseCredentialPlanRejectsObsoleteV3EnvironmentDefinitionRoot(t *testing.T) {
	root := t.TempDir()
	writeCredentialV3EnvironmentFixture(t, root)
	if _, err := parseCredentialPlan(
		root,
		"deploy/environments/oilan/environment.yaml",
	); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("obsolete v3 environment root error = %v", err)
	}
}

func TestParseCredentialPlanRejectsUndeclaredV3CredentialReference(t *testing.T) {
	root := t.TempDir()
	writeCredentialV3EnvironmentFixture(t, root)
	path := filepath.Join(root, "deploy/environments/oilan/environment.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(
		string(data),
		"sourceCredentialRef:\n      kind: KubernetesSecret\n      name: cnb-regcred",
		"sourceCredentialRef:\n      status: undeclared\n      deploymentBlockedUntilDeclared: true",
		1,
	))
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := parseCredentialPlan(root, "deploy/environments/oilan/environment.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("undeclared v3 credential error = %v", err)
	}
}

func TestParseCredentialPlanRejectsMissingV3HealthChecks(t *testing.T) {
	root := t.TempDir()
	writeCredentialV3EnvironmentFixture(t, root)
	path := filepath.Join(root, "deploy/environments/oilan/environment.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(
		string(data),
		"  healthChecks:\n    public:\n      - id: frontend-health\n        url: https://example.test/healthz\n        expectedStatus: 200\n    workloads: []\n",
		"",
		1,
	))
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := parseCredentialPlan(root, "deploy/environments/oilan/environment.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("missing v3 health checks error = %v", err)
	}
}

func TestParseCredentialPlanRejectsDuplicateKeysInConfigurationSource(t *testing.T) {
	root := t.TempDir()
	writeCredentialPlanTestFile(t, root, "release.yaml", `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: release
spec:
  application: app
  environment: production
  configurationSource: environments.yaml
`)
	writeCredentialPlanTestFile(t, root, "environments.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: test-environments
environments:
  production:
    target:
      name: first
      name: second
      cluster: cluster
      instance: instance
`)
	if _, err := parseCredentialPlan(root, "release.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("configuration duplicate YAML error = %v, want ErrCredentialDeclarationInvalid", err)
	}
}

func TestParseCredentialPlanRejectsUnknownEnvironmentExecutorFields(t *testing.T) {
	root := t.TempDir()
	writeCredentialPlanTestFile(t, root, "release.yaml", `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: release
spec:
  application: app
  environment: production
  configurationSource: environments.yaml
`)
	writeCredentialPlanTestFile(t, root, "environments.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: test-environments
environments:
  production:
    target:
      name: target
      cluster: cluster
      instance: instance
    executor:
      type: helm
      lifecyle: detached-kubernetes-job
`)
	if _, err := parseCredentialPlan(root, "release.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("unknown executor field error = %v, want ErrCredentialDeclarationInvalid", err)
	}
}

func writeCredentialPlanTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create test directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func writeCredentialServiceReleaseFixture(t *testing.T, root, name, service, application string) {
	t.Helper()
	writeCredentialPlanTestFile(t, root, name, `
apiVersion: doops.sh/v2
kind: ServiceRelease
metadata:
  name: `+service+`
spec:
  service: `+service+`
  application: `+application+`
  source:
    repository: https://example.test/`+application+`.git
    revision: ${inputs.revision}
  artifact:
    type: image
    repository: registry.example.test/`+service+`
    version: ${inputs.version}
  binding:
    helmValue: `+service+`
    workload: deployment/`+service+`
    container: `+service+`
  intents:
    - build-artifact
  evidence:
    required:
      - artifact-identity
`)
}

func writeCredentialV3EnvironmentFixture(t *testing.T, root string) {
	t.Helper()
	writeCredentialPlanTestFile(t, root, "deploy/environments/oilan/environment.yaml", `
apiVersion: doops.sh/v3
kind: EnvironmentDefinition
metadata:
  name: oilan
spec:
  application: zhiyong
  parameters:
    reason:
      required: true
  source:
    repository: https://example.test/zhiyong.git
    branch: main
    buildManifest: deploy/deploy.yaml
    imageRegistry: registry.example.test/zhiyong
  execution:
    build:
      executor: cnb
      doopsTarget: null
    deploy:
      executor: doops
      target: gw-edu-coder
      cluster: doops-edu
      instance: edu-coder
      session: zhiyong-oilan-release
  helm:
    release: zhiyong
    namespace: oilan
    chart: deploy/environments/oilan/chart
    values: deploy/environments/oilan/chart/values.yaml
    atomic: true
    wait: true
    rollbackOnFailure: true
    sharedRelease: true
  artifacts:
    targetRegistry: registry.target.test/zhiyong
    sourceCredentialRef:
      kind: KubernetesSecret
      name: cnb-regcred
    targetCredentialRef:
      kind: KubernetesSecret
      name: regcred
    runtimeImagePullSecret:
      kind: KubernetesSecret
      name: regcred
  services:
    frontend: deploy/environments/oilan/services/zhiyong-frontend.yaml
  healthChecks:
    public:
      - id: frontend-health
        url: https://example.test/healthz
        expectedStatus: 200
    workloads: []
  releaseProcess:
    - validate image digest
  stopConditions:
    - stop on unknown outcome
`)
	writeCredentialPlanTestFile(t, root, "deploy/environments/oilan/services/zhiyong-frontend.yaml", `
apiVersion: doops.sh/v3
kind: EnvironmentService
metadata:
  name: zhiyong-frontend
  environment: oilan
spec:
  serviceDefinition: deploy/services/zhiyong-frontend.yaml
  environmentDefinition: deploy/environments/oilan/environment.yaml
  artifact:
    source: registry.example.test/zhiyong/frontend
    target: registry.target.test/zhiyong/frontend
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
`)
	writeCredentialPlanTestFile(t, root, "deploy/services/zhiyong-frontend.yaml", `
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
}
