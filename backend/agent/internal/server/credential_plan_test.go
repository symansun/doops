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
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: test-environments
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

func TestRepositoryDeploymentWorkflowsCompileCredentialPlans(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	workflowPaths, err := filepath.Glob(filepath.Join(repositoryRoot, "backend", "deploy", "workflows", "*.yaml"))
	if err != nil {
		t.Fatalf("list repository workflows: %v", err)
	}
	if len(workflowPaths) == 0 {
		t.Fatal("repository must contain deployment workflows")
	}
	for _, path := range workflowPaths {
		relative, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			t.Fatalf("resolve workflow path %s: %v", path, err)
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			if _, err := parseCredentialPlan(repositoryRoot, filepath.ToSlash(relative)); err != nil {
				t.Fatalf("compile repository workflow: %v", err)
			}
		})
	}
}

func TestExternalRepositoryDeploymentWorkflowsCompileCredentialPlans(t *testing.T) {
	repositoryRoot := strings.TrimSpace(os.Getenv("DOOPS_CREDENTIAL_CONTRACT_ROOT"))
	if repositoryRoot == "" {
		t.Skip("DOOPS_CREDENTIAL_CONTRACT_ROOT is not set")
	}
	workflowPattern := strings.TrimSpace(os.Getenv("DOOPS_CREDENTIAL_CONTRACT_WORKFLOWS"))
	if workflowPattern == "" {
		workflowPattern = "deploy/workflows/*.yaml"
	}
	workflowPaths, err := filepath.Glob(filepath.Join(
		repositoryRoot,
		filepath.FromSlash(workflowPattern),
	))
	if err != nil {
		t.Fatalf("list external repository workflows: %v", err)
	}
	if len(workflowPaths) == 0 {
		t.Fatalf("external repository has no workflows matching %q", workflowPattern)
	}
	for _, path := range workflowPaths {
		relative, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			t.Fatalf("resolve external workflow path %s: %v", path, err)
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			if _, err := parseCredentialPlan(repositoryRoot, filepath.ToSlash(relative)); err != nil {
				t.Fatalf("compile external repository workflow: %v", err)
			}
		})
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

func TestParseCredentialPlanAcceptsNonSensitiveParameterDefaults(t *testing.T) {
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
    reason:
      required: false
      default: routine-release
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
	if _, err := parseCredentialPlan(root, "release.yaml"); err != nil {
		t.Fatalf("non-sensitive parameter default: %v", err)
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

func TestParseCredentialPlanAcceptsCurrentReleaseShapesWithoutManagedCredentials(t *testing.T) {
	for _, test := range []struct {
		name     string
		workflow string
	}{
		{
			name: "deployment template version release",
			workflow: `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: ecp-test-deploy
spec:
  application: ecp
  release:
    version: ${inputs.version}
  environment: test
  configurationSource: deploy/environments.yaml
`,
		},
		{
			name: "semantic release",
			workflow: `
apiVersion: doops.sh/v2
kind: SemanticRelease
metadata:
  name: zhiyong-test-release
spec:
  application: zhiyong
  release:
    revision: ${inputs.revision}
    version: ${inputs.version}
    reason: ${inputs.reason}
  environment: test
  configurationSource: deploy/environments.yaml
  serviceCatalog:
    frontend:
      uses: services/frontend.yaml
  flow:
    - intent: establish-release-context
`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeCredentialPlanTestFile(t, root, "deploy/release.yaml", test.workflow)
			if test.name == "semantic release" {
				writeCredentialServiceReleaseFixture(t, root, "deploy/services/frontend.yaml", "frontend", "zhiyong")
			}
			writeCredentialPlanTestFile(t, root, "deploy/environments.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: test-environments
environments:
  test:
    target:
      name: gw-oilan-node
      cluster: doops-oilan
      instance: oilan-node
`)

			plan, err := parseCredentialPlan(root, "deploy/release.yaml")
			if err != nil {
				t.Fatalf("parse credential plan: %v", err)
			}
			if plan.Template == "" || plan.Project == "" || plan.Environment != "test" ||
				plan.Target != "gw-oilan-node" || plan.Cluster != "doops-oilan" ||
				plan.Instance != "oilan-node" {
				t.Fatalf("unexpected plan: %#v", plan)
			}
			if len(plan.CredentialRefs) != 0 || len(plan.BundleRefs) != 0 {
				t.Fatalf("expected no managed credentials, got %#v", plan)
			}
		})
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

func TestParseCredentialPlanAcceptsCurrentEnvironmentRegistryFields(t *testing.T) {
	root := t.TempDir()
	writeCredentialPlanTestFile(t, root, "deploy/release.yaml", `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: ecp-oilan-deploy
spec:
  parameters:
    version:
      required: true
    reason:
      required: true
  application: ecp
  release:
    version: ${inputs.version}
  environment: oilan
  configurationSource: deploy/environments.yaml
`)
	writeCredentialPlanTestFile(t, root, "deploy/environments.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: current-environments
  updatedAt: "2026-07-13"
identityRules:
  - Environment keys identify release destinations.
artifactContract:
  type: image-set
  sourceRegistry: docker.cnb.cool/l8ai/app
  sourceRepository: https://cnb.cool/l8ai/app.git
  sourceBranch: main
  services:
    - frontend
  sourceArtifactNames:
    frontend: app-frontend
  imageTagPattern: "^release-[0-9]{8}$"
  imageReferenceFormat: registry/repository:tag
  imageTagTimeZone: Asia/Shanghai
serviceCatalog:
  frontend:
    artifact: app-frontend
    sourceContext: app-frontend
    helmValue: frontend
    workload: deployment/app-frontend
    container: app-frontend
    endpointRequired: true
verificationProfiles:
  application:
    requiredEvidence:
      - runtime-state
      - public-http
environments:
  oilan:
    displayName: Oilan
    services:
      - frontend
    target:
      name: gw-edu-coder
      cluster: doops-edu
      instance: edu-coder
    deploymentMode: application
    controlPlaneEnvironment: oilan
    publicHosts:
      - app.oilan.ai
    executor:
      type: helm
      lifecycle: detached-kubernetes-job
      config:
        namespace: oilan
        release: app
        workload: deployment/app-frontend
        container: app-frontend
        registry: repo.aiedulab.cn:8443/oilan-system
        registryCredential:
          namespace: oilan
          secretName: regcred
          key: .dockerconfigjson
        chart: deploy/chart
        values: deploy/values.yaml
        runtimeFiles: deploy/runtime
        deploymentMode: application
        imageBindings:
          app-frontend: frontend
        workloadNamespaces:
          app-frontend: oilan
        artifactSync:
          sourceCredentialRef:
            kind: KubernetesSecret
            name: cnb-regcred
          registryCredentialRef:
            kind: KubernetesSecret
            name: regcred
        publicHosts:
          - app.oilan.ai
        healthChecks:
          public:
            - id: frontend-health
              url: https://app.oilan.ai/healthz
              expectedStatus: 200
              bodyContains: healthy
          workloads:
            - service: app-frontend
              minReadyReplicas: 1
              requireEndpoints: true
        retiredResources:
          - apiVersion: networking.k8s.io/v1
            kind: Ingress
            name: old-app
            deletionPolicy: require-unmanaged-exact-match
            expected:
              manager: kubectl-client-side-apply
              ingressClassName: oilan-edge
              hosts:
                - old-app.oilan.ai
    verificationProfile: application
`)

	plan, err := parseCredentialPlan(root, "deploy/release.yaml")
	if err != nil {
		t.Fatalf("parse current environment registry: %v", err)
	}
	if plan.Target != "gw-edu-coder" || plan.Cluster != "doops-edu" || plan.Instance != "edu-coder" {
		t.Fatalf("unexpected plan target: %#v", plan)
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
    nested:
      StringData: canary-secret
`)
	if _, err := parseCredentialPlan(root, "release.yaml"); !errors.Is(err, ErrInlineCredentialMaterial) {
		t.Fatalf("configuration secret-like field error = %v, want ErrInlineCredentialMaterial", err)
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
