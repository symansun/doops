package server

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseCredentialPlanReturnsOnlyDeclaredReferenceMetadata(t *testing.T) {
	root := t.TempDir()
	writeCredentialPlanTestFile(t, root, "deploy/release.yaml", `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: oilan-agent-release
spec:
  application: oilan
  environment: production
  configurationSource: deploy/environments.yaml
  credentialRefs:
    - name: cnb-oci-pull
      use: imagePull
      namespace: kz-ops
      registryRepository: team/app
      registryReference: latest
      workload:
        kind: Deployment
        name: doops-agent-live
  credentialBundleRefs:
    - name: release-shared
`)
	writeCredentialPlanTestFile(t, root, "deploy/environments.yaml", `
environments:
  production:
    target:
      name: gw-edu-coder
      cluster: doops-edu
      instance: edu-coder
`)

	plan, err := parseCredentialPlan(root, "deploy/release.yaml")
	if err != nil {
		t.Fatalf("parse credential plan: %v", err)
	}
	if plan.Template != "oilan-agent-release" ||
		plan.Project != "oilan" ||
		plan.Environment != "production" ||
		plan.Target != "gw-edu-coder" ||
		plan.Cluster != "doops-edu" ||
		plan.Instance != "edu-coder" {
		t.Fatalf("unexpected deployment context: %#v", plan)
	}
	if len(plan.CredentialRefs) != 1 {
		t.Fatalf("credential refs = %#v", plan.CredentialRefs)
	}
	ref := plan.CredentialRefs[0]
	if ref.Name != "cnb-oci-pull" ||
		ref.Use != CredentialUseImagePull ||
		ref.Namespace != "kz-ops" ||
		ref.Workload.Kind != "Deployment" ||
		ref.Workload.Name != "doops-agent-live" {
		t.Fatalf("unexpected credential reference: %#v", ref)
	}
	if len(plan.BundleRefs) != 1 || plan.BundleRefs[0] != "release-shared" {
		t.Fatalf("bundle refs = %#v", plan.BundleRefs)
	}
}

func TestRepositoryReleaseRunnerBootstrapCredentialPlan(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	plan, err := parseCredentialPlan(
		repositoryRoot,
		"backend/deploy/workflows/oilan-release-runner-bootstrap.yaml",
	)
	if err != nil {
		t.Fatalf("parse release runner bootstrap credential plan: %v", err)
	}
	if plan.Template != "oilan-release-runner-bootstrap" ||
		plan.Project != "doops-release-runner" ||
		plan.Environment != "oilan-release-runner-bootstrap" ||
		plan.Target != "gw-edu-coder" ||
		plan.Cluster != "doops-edu" ||
		plan.Instance != "edu-coder" {
		t.Fatalf("unexpected release runner bootstrap context: %#v", plan)
	}
	if len(plan.CredentialRefs) != 1 {
		t.Fatalf("credential refs = %#v", plan.CredentialRefs)
	}
	ref := plan.CredentialRefs[0]
	if ref.Name != "doops-release-runner-runtime" ||
		ref.Use != CredentialUseOpaqueSecret ||
		ref.Namespace != "kz-ops" ||
		ref.Workload.Kind != "Deployment" ||
		ref.Workload.Name != "doops-release-runner" {
		t.Fatalf("unexpected release runner credential reference: %#v", ref)
	}
}

func TestParseCredentialPlanSupportsSemanticRelease(t *testing.T) {
	root := t.TempDir()
	writeCredentialPlanTestFile(t, root, "deploy/workflows/release.yaml", `
apiVersion: doops.sh/v2
kind: SemanticRelease
metadata:
  name: application-release
spec:
  parameters:
    revision:
      required: true
    version:
      required: true
    reason:
      required: true
    services:
      required: true
  application: application
  release:
    revision: ${inputs.revision}
    version: ${inputs.version}
    reason: ${inputs.reason}
    selectedServices: ${inputs.services}
  environment: production
  configurationSource: deploy/environments/production.yaml
  serviceCatalog:
    api:
      uses: services/api.yaml
  flow:
    - intent: materialize-selected-artifacts
      forEach: selected-services
      produces:
        - source-artifact
`)
	writeCredentialPlanTestFile(t, root, "deploy/workflows/services/api.yaml", `
apiVersion: doops.sh/v2
kind: ServiceRelease
metadata:
  name: application-api
spec:
  service: api
  application: application
  source:
    repository: https://example.invalid/application.git
    branch: main
    revision: ${inputs.revision}
    context: api
  artifact:
    type: oci-image
    repository: application-api
    version: ${inputs.version}
  binding:
    helmValue: api
    workload: deployment/application-api
    container: application-api
  intents:
    - materialize-artifact
  evidence:
    required:
      - target-image-digest
`)
	writeCredentialPlanTestFile(t, root, "deploy/environments/production.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentDefinition
metadata:
  name: production
spec:
  execution:
    deploy:
      executor: doops
      target: gw-edu-coder
      cluster: doops-edu
      instance: edu-coder
  artifacts:
    runtimeImagePullSecret:
      kind: KubernetesSecret
      name: regcred
`)

	plan, err := parseCredentialPlan(root, "deploy/workflows/release.yaml")
	if err != nil {
		t.Fatalf("parse SemanticRelease credential plan: %v", err)
	}
	if plan.Template != "application-release" ||
		plan.Project != "application" ||
		plan.Environment != "production" ||
		plan.Target != "gw-edu-coder" ||
		plan.Cluster != "doops-edu" ||
		plan.Instance != "edu-coder" {
		t.Fatalf("unexpected SemanticRelease context: %#v", plan)
	}
	if len(plan.CredentialRefs) != 0 || len(plan.BundleRefs) != 0 {
		t.Fatalf("unexpected SemanticRelease credential references: %#v", plan)
	}
}

func TestParseCredentialPlanRejectsSemanticServiceIdentityMismatch(t *testing.T) {
	root := t.TempDir()
	writeSemanticCredentialPlanFixture(t, root, "different-service")

	if _, err := parseCredentialPlan(root, "deploy/workflows/release.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("service identity mismatch error = %v, want ErrCredentialDeclarationInvalid", err)
	}
}

func TestParseCredentialPlanRejectsEmptySemanticEnvironmentDefinition(t *testing.T) {
	root := t.TempDir()
	writeSemanticCredentialPlanFixture(t, root, "api")
	writeCredentialPlanTestFile(t, root, "deploy/environments/production.yaml", "")

	if _, err := parseCredentialPlan(root, "deploy/workflows/release.yaml"); !errors.Is(err, ErrCredentialDeclarationInvalid) {
		t.Fatalf("empty EnvironmentDefinition error = %v, want ErrCredentialDeclarationInvalid", err)
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

func TestParseCredentialPlanRejectsInlineSecretLikeFieldsInConfigurationSource(t *testing.T) {
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
environments:
  production:
    target:
      name: target
      cluster: cluster
      instance: instance
    nested:
      StringData: canary-secret
`)
	if _, err := parseCredentialPlan(root, "release.yaml"); !errors.Is(err, ErrInlineCredentialMaterial) {
		t.Fatalf("configuration secret-like field error = %v, want ErrInlineCredentialMaterial", err)
	}
}

func writeSemanticCredentialPlanFixture(t *testing.T, root, serviceIdentity string) {
	t.Helper()
	writeCredentialPlanTestFile(t, root, "deploy/workflows/release.yaml", `
apiVersion: doops.sh/v2
kind: SemanticRelease
metadata:
  name: application-release
spec:
  application: application
  release:
    revision: ${inputs.revision}
    version: ${inputs.version}
    reason: ${inputs.reason}
    selectedServices: ${inputs.services}
  environment: production
  configurationSource: deploy/environments/production.yaml
  serviceCatalog:
    api:
      uses: services/api.yaml
  flow:
    - intent: materialize-selected-artifacts
`)
	writeCredentialPlanTestFile(t, root, "deploy/workflows/services/api.yaml", `
apiVersion: doops.sh/v2
kind: ServiceRelease
metadata:
  name: application-api
spec:
  service: `+serviceIdentity+`
  application: application
  source:
    repository: https://example.invalid/application.git
    revision: ${inputs.revision}
  artifact:
    type: oci-image
    repository: application-api
    version: ${inputs.version}
  binding:
    helmValue: api
    workload: deployment/application-api
    container: application-api
  intents:
    - materialize-artifact
  evidence:
    required:
      - target-image-digest
`)
	writeCredentialPlanTestFile(t, root, "deploy/environments/production.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentDefinition
metadata:
  name: production
spec:
  execution:
    deploy:
      target: gw-edu-coder
      cluster: doops-edu
      instance: edu-coder
`)
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
