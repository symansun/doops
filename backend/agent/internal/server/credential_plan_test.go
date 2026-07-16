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
			writeCredentialPlanTestFile(t, root, "deploy/environments.yaml", `
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

func TestParseCredentialPlanAcceptsSemanticReleaseEnvironmentDefinition(t *testing.T) {
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
	writeCredentialPlanTestFile(t, root, "deploy/environments/oilan/environment.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentDefinition
metadata:
  name: oilan
spec:
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

	plan, err := parseCredentialPlan(root, "deploy/workflows/oilan.yaml")
	if err != nil {
		t.Fatalf("parse SemanticRelease EnvironmentDefinition: %v", err)
	}
	if plan.Template != "zhiyong-oilan-release" || plan.Target != "gw-edu-coder" ||
		plan.Cluster != "doops-edu" || plan.Instance != "edu-coder" {
		t.Fatalf("unexpected plan: %#v", plan)
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

func TestParseCredentialPlanAcceptsV3EnvironmentDefinitionRoot(t *testing.T) {
	root := t.TempDir()
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
  execution:
    deploy:
      target: gw-edu-coder
      cluster: doops-edu
      instance: edu-coder
  helm:
    namespace: oilan
`)
	plan, err := parseCredentialPlan(root, "deploy/environments/oilan/environment.yaml")
	if err != nil {
		t.Fatalf("parse v3 credential plan: %v", err)
	}
	if plan.Template != "oilan" || plan.Project != "zhiyong" ||
		plan.Environment != "oilan" || plan.Target != "gw-edu-coder" ||
		plan.Cluster != "doops-edu" || plan.Instance != "edu-coder" {
		t.Fatalf("unexpected v3 plan: %#v", plan)
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
