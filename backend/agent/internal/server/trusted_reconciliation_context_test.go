package server

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBuildTrustedReconciliationAdmissionBindsWorkspacePlanAndExactTools(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	sessionID := "release-session"
	workspace := filepath.Join(root, sessionID)
	if err := os.MkdirAll(filepath.Join(workspace, "deploy"), 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workspaceCommit := strings.Repeat("a", 40)
	revision := strings.Repeat("b", 40)
	writeCredentialPlanTestFile(t, workspace, "deploy/release.yaml", `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: release
spec:
  parameters:
    releaseId:
      required: true
  application: app
  release:
    source:
      repository: https://example.test/app.git
      revision: ${inputs.releaseId}
      branch: main
  environment: production
  configurationSource: deploy/environments.yaml
`)
	writeCredentialPlanTestFile(t, workspace, "deploy/environments.yaml", `
artifactContract:
  type: image-set
  sourceRegistry: registry.example.test/source
  sourceRepository: https://example.test/app.git
  sourceBranch: main
  services: [app]
  sourceArtifactNames:
    app: app
  imageTagPattern: "^[0-9a-f]{40}$"
  imageReferenceFormat: repository@digest
verificationProfiles:
  production:
    requiredEvidence:
      - source-identity
      - image-set
      - gitops-render
      - runtime-state
      - authorization-state
      - public-contract
      - post-deploy-log-scan
      - release-manifest
environments:
  production:
    deploymentPlatform: linux/amd64
    target:
      name: control
      cluster: doops
      instance: runner
    deploymentTarget:
      name: workload
      cluster: doops
      instance: app
    executor:
      type: helm
      lifecycle: in-process
      config:
        namespace: apps
        release: app
        workload: deployment/app
        container: app
        registry: registry.example.test/target
        releaseManifestRepository: https://example.test/app.git
        chart: deploy/chart
        values: deploy/values.yaml
        imageBindings:
          app: ""
        healthChecks:
          public:
            - id: health
              url: https://app.example.test/health
              expectedStatus: 200
        authz:
          appCode: APP
          environmentCode: PROD
    verificationProfile: production
`)

	instruction := `{
		"task":"execute-doops-cicd-workflow",
		"skill":"$doops-cicd",
		"executionMode":"dry-run",
		"runId":"release-session-0123456789abcdef",
		"workflowPath":"deploy/release.yaml",
		"workspaceCommit":"` + workspaceCommit + `",
		"inputs":{"releaseId":"` + revision + `"},
		"credentialRun":{
			"apiVersion":"doops.sh/v2",
			"kind":"CredentialRun",
			"id":"credrun_123",
			"mode":"dry-run",
			"cluster":"doops",
			"instance":"runner",
			"template":"release",
			"project":"app",
			"environment":"production",
			"mutationCount":0,
			"materializations":[],
			"createdAt":"2026-07-16T00:00:00Z"
		},
		"resultContract":{"apiVersion":"doops.sh/v2","kind":"DeploymentRun","requireEvidence":true}
	}`
	admission, err := buildTrustedReconciliationAdmission(sessionID, instruction, workspaceCommit)
	if err != nil {
		t.Fatalf("build trusted admission: %v", err)
	}
	wantTools := []string{
		"mcp_doops_plan_ObserveAuthorizationState",
		"mcp_doops_plan_ObserveHTTPContract",
		"mcp_doops_plan_ObserveHelmRelease",
		"mcp_doops_plan_ObserveKubernetesLogs",
		"mcp_doops_plan_ObserveKubernetesWorkload",
		"mcp_doops_plan_ObserveSourceRegistryImageSet",
		"mcp_doops_plan_ObserveTargetRegistryImageSet",
		"mcp_doops_plan_ObserveWorkspaceSource",
		"mcp_doops_plan_RenderHelmRelease",
	}
	if !reflect.DeepEqual(admission.AllowedTools, wantTools) {
		t.Fatalf("allowed tools = %#v, want %#v", admission.AllowedTools, wantTools)
	}
	if admission.Context.SchemaVersion != "doops.reconciliation-context/v1" ||
		admission.Context.ExecutionMode != "dry-run" ||
		admission.Context.MutationAuthorized ||
		admission.Context.Source.Repository != "https://example.test/app.git" ||
		admission.Context.Source.Revision != revision ||
		admission.Context.Source.Branch != "main" ||
		admission.Context.Source.WorkspaceCommit != workspaceCommit ||
		!strings.HasPrefix(admission.Context.OperationID, "op_") ||
		!validSHA256Digest(admission.Context.PlanDigest) ||
		!validSHA256Digest(admission.Context.PlanBindingDigest) ||
		!validSHA256Digest(admission.Context.CredentialRunDigest) ||
		!validSHA256Digest(admission.Context.ContextDigest) {
		t.Fatalf("unexpected trusted context: %#v", admission.Context)
	}
	if len(admission.Context.Capabilities) != len(wantTools) {
		t.Fatalf("capability count = %d, want %d", len(admission.Context.Capabilities), len(wantTools))
	}
	for key, capability := range admission.Context.Capabilities {
		if capability.Tool == "SyncImageSetToTarget" || capability.Tool == "ReconcileHelmRelease" {
			t.Fatalf("dry-run capability %q contains executor %#v", key, capability)
		}
		if !validSHA256Digest(capability.ScopeDigest) || capability.CanonicalScope == nil {
			t.Fatalf("capability %q is not scope-bound: %#v", key, capability)
		}
	}
	if err := admission.Context.ValidateDigest(); err != nil {
		t.Fatalf("context digest does not validate: %v", err)
	}
}

func TestBuildTrustedReconciliationAdmissionAddsExecutorsOnlyForApply(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	sessionID := "apply-session"
	workspace := filepath.Join(root, sessionID)
	if err := os.MkdirAll(filepath.Join(workspace, "deploy"), 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	workspaceCommit := strings.Repeat("a", 40)
	revision := strings.Repeat("b", 40)
	writeTrustedAdmissionFixture(t, workspace)
	instruction := strings.NewReplacer(
		"${MODE}", "apply",
		"${RUN_ID}", "apply-session-0123456789abcdef",
		"${WORKSPACE_COMMIT}", workspaceCommit,
		"${REVISION}", revision,
	).Replace(trustedAdmissionInstructionFixture)

	admission, err := buildTrustedReconciliationAdmission(sessionID, instruction, workspaceCommit)
	if err != nil {
		t.Fatalf("build apply trusted admission: %v", err)
	}
	if !admission.Context.MutationAuthorized {
		t.Fatal("apply admission must authorize mutation")
	}
	for _, tool := range []string{
		"mcp_doops_plan_SyncImageSetToTarget",
		"mcp_doops_plan_ReconcileHelmRelease",
	} {
		if !containsString(admission.AllowedTools, tool) {
			t.Fatalf("apply allowed tools missing %s: %#v", tool, admission.AllowedTools)
		}
	}
}

func TestBuildTrustedReconciliationAdmissionRejectsSecretInputNames(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	sessionID := "secret-input"
	workspace := filepath.Join(root, sessionID)
	if err := os.MkdirAll(filepath.Join(workspace, "deploy"), 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	writeTrustedAdmissionFixture(t, workspace)
	instruction := strings.NewReplacer(
		"${MODE}", "dry-run",
		"${RUN_ID}", "secret-input-0123456789abcdef",
		"${WORKSPACE_COMMIT}", strings.Repeat("a", 40),
		"${REVISION}", strings.Repeat("b", 40),
	).Replace(strings.Replace(
		trustedAdmissionInstructionFixture,
		`"inputs":{"releaseId":"${REVISION}"}`,
		`"inputs":{"releaseId":"${REVISION}","registryPassword":"forbidden"}`,
		1,
	))
	if _, err := buildTrustedReconciliationAdmission(
		sessionID,
		instruction,
		strings.Repeat("a", 40),
	); err == nil {
		t.Fatal("secret-like workflow input must be rejected")
	}
}

func TestBuildTrustedReconciliationAdmissionRejectsSecretInputValues(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	sessionID := "secret-value"
	workspace := filepath.Join(root, sessionID)
	if err := os.MkdirAll(filepath.Join(workspace, "deploy"), 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	writeTrustedAdmissionFixture(t, workspace)
	instruction := strings.NewReplacer(
		"${MODE}", "dry-run",
		"${RUN_ID}", "secret-value-0123456789abcdef",
		"${WORKSPACE_COMMIT}", strings.Repeat("a", 40),
		"${REVISION}", "https://example.test/repo.git?access_token=synthetic",
	).Replace(trustedAdmissionInstructionFixture)
	if _, err := buildTrustedReconciliationAdmission(
		sessionID,
		instruction,
		strings.Repeat("a", 40),
	); err == nil {
		t.Fatal("secret-bearing workflow input value must be rejected")
	}
}

func TestBuildTrustedReconciliationAdmissionSupportsSemanticRelease(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOOPS_WORKSPACE_ROOT", root)
	sessionID := "semantic-release"
	workspace := filepath.Join(root, sessionID)
	if err := os.MkdirAll(filepath.Join(workspace, "deploy/workflows/services"), 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	writeCredentialPlanTestFile(t, workspace, "deploy/workflows/release.yaml", `
apiVersion: doops.sh/v2
kind: SemanticRelease
metadata:
  name: application-release
spec:
  parameters:
    reason:
      required: true
  application: application
  release:
    reason: ${inputs.reason}
    selectedServices: all
  environment: production
  configurationSource: deploy/environments.yaml
  serviceCatalog:
    api:
      uses: services/api.yaml
  flow:
    - intent: converge-release
`)
	writeCredentialPlanTestFile(t, workspace, "deploy/workflows/services/api.yaml", `
apiVersion: doops.sh/v2
kind: ServiceRelease
metadata:
  name: application-api
spec:
  service: api
  application: application
  artifact:
    type: oci-image
    declarationSource: environment-service
    identity: exact-sha256-digest
  binding:
    helmValue: api
    workload: deployment/application-api
    container: application-api
  intents: [converge]
  evidence:
    required: [runtime-state]
`)
	writeCredentialPlanTestFile(t, workspace, "deploy/environments.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: application-environments
artifactContract:
  sourceRepository: https://example.test/application.git
  sourceBranch: main
environments:
  production:
    file: deploy/environments/production.yaml
`)
	writeCredentialPlanTestFile(t, workspace, "deploy/environments/production.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentDefinition
metadata:
  name: production
spec:
  execution:
    deploy:
      target: control
      cluster: doops
      instance: runner
`)
	workspaceCommit := strings.Repeat("a", 40)
	instruction := `{
		"task":"execute-doops-cicd-workflow",
		"skill":"$doops-cicd",
		"executionMode":"dry-run",
		"runId":"semantic-release-0123456789abcdef",
		"workflowPath":"deploy/workflows/release.yaml",
		"workspaceCommit":"` + workspaceCommit + `",
		"inputs":{"reason":"scheduled-release"},
		"credentialRun":{
			"apiVersion":"doops.sh/v2",
			"kind":"CredentialRun",
			"id":"credrun_semantic",
			"mode":"dry-run",
			"cluster":"doops",
			"instance":"runner",
			"template":"application-release",
			"project":"application",
			"environment":"production",
			"mutationCount":0,
			"materializations":[],
			"createdAt":"2026-07-16T00:00:00Z"
		},
		"resultContract":{"apiVersion":"doops.sh/v2","kind":"DeploymentRun","requireEvidence":true}
	}`
	admission, err := buildTrustedReconciliationAdmission(sessionID, instruction, workspaceCommit)
	if err != nil {
		t.Fatalf("build SemanticRelease trusted admission: %v", err)
	}
	if admission.Context.Source.Repository != "https://example.test/application.git" ||
		admission.Context.Source.Branch != "main" ||
		admission.Context.Source.Revision != workspaceCommit {
		t.Fatalf("unexpected SemanticRelease source: %#v", admission.Context.Source)
	}
}

func TestCredentialSecretLikeKeyNormalizesSeparators(t *testing.T) {
	for _, key := range []string{
		"client_secret", "registry-password", "private.key", "accessToken",
		"dbPassword", "secretKey", "authorization", "cookie", "githubToken",
		"registryCredential", "githubPAT", "passphrase", "dockerConfigJson", "sshIdentity",
	} {
		if !credentialSecretLikeKey(key) {
			t.Fatalf("credentialSecretLikeKey(%q) = false, want true", key)
		}
	}
	if credentialSecretLikeKey("releaseId") {
		t.Fatal("releaseId must not be classified as credential material")
	}
	for _, key := range []string{"secretName", "imagePullSecrets", "agentTokenSecret"} {
		value := &yaml.Node{Kind: yaml.MappingNode}
		if credentialSecretLikeYAMLField(key, value) {
			t.Fatalf("reference metadata %q must not be classified as inline material", key)
		}
	}
	if !credentialSecretLikeYAMLField("stringData", &yaml.Node{Kind: yaml.MappingNode}) {
		t.Fatal("stringData mapping must be rejected")
	}
	if !credentialSecretLikeYAMLField("githubToken", &yaml.Node{Kind: yaml.MappingNode}) {
		t.Fatal("sensitive parameter declaration mapping must be rejected")
	}
	for _, value := range []string{
		"https://user:token@example.test/repo.git",
		"https://example.test/repo.git?access_token=synthetic",
		"https://example.test/repo.git?X-Goog-Signature=synthetic",
		"scheduled release Bearer synthetic-canary",
		"Bearer synthetic-canary",
		"-----BEGIN PRIVATE KEY-----",
	} {
		if !credentialScalarContainsInlineMaterial(value) {
			t.Fatalf("inline credential scalar %q was accepted", value)
		}
	}
}

func TestBuildDoagentPromptParamsUsesExactPlanToolsAndNoGenericTools(t *testing.T) {
	context := trustedReconciliationContext{
		SchemaVersion:      trustedReconciliationContextSchema,
		OperationID:        "op_0123456789abcdef0123456789abcdef",
		PlanDigest:         "sha256:" + strings.Repeat("1", 64),
		PlanBindingDigest:  "sha256:" + strings.Repeat("2", 64),
		ExecutionMode:      "dry-run",
		MutationAuthorized: false,
		Source: trustedReconciliationSource{
			Repository:      "https://example.test/app.git",
			Revision:        strings.Repeat("b", 40),
			Branch:          "main",
			WorkspaceCommit: strings.Repeat("a", 40),
		},
		Capabilities:  map[string]trustedReconciliationCapability{},
		ContextDigest: "sha256:" + strings.Repeat("3", 64),
	}
	admission := trustedReconciliationAdmission{
		AllowedTools: []string{"mcp_doops_plan_ObserveWorkspaceSource"},
		Context:      context,
	}
	params := buildDoagentPromptParams("doagent-session", "execute", &admission, true)
	if params["sessionId"] != "doagent-session" || params["prompt"] != "execute" {
		t.Fatalf("unexpected base prompt params: %#v", params)
	}
	allowed, ok := params["allowedTools"].([]string)
	if !ok || !reflect.DeepEqual(allowed, admission.AllowedTools) {
		t.Fatalf("allowedTools = %#v, want %#v", params["allowedTools"], admission.AllowedTools)
	}
	if !reflect.DeepEqual(params["trustedReconciliationContext"], context) {
		t.Fatalf("trusted context missing from prompt params: %#v", params)
	}
	for _, forbidden := range []string{"Bash", "Read", "Agent", "Glob"} {
		if containsString(allowed, forbidden) {
			t.Fatalf("generic tool %q entered structured prompt allowlist: %#v", forbidden, allowed)
		}
	}

	plain := buildDoagentPromptParams("plain-json", "return JSON", nil, true)
	plainAllowed, ok := plain["allowedTools"].([]string)
	if !ok || len(plainAllowed) != 0 {
		t.Fatalf("plain JSON prompt must disable all tools: %#v", plain)
	}
	if _, ok := plain["trustedReconciliationContext"]; ok {
		t.Fatalf("plain JSON prompt must not carry trusted context: %#v", plain)
	}
}

const trustedAdmissionInstructionFixture = `{
	"task":"execute-doops-cicd-workflow",
	"skill":"$doops-cicd",
	"executionMode":"${MODE}",
	"runId":"${RUN_ID}",
	"workflowPath":"deploy/release.yaml",
	"workspaceCommit":"${WORKSPACE_COMMIT}",
	"inputs":{"releaseId":"${REVISION}"},
	"credentialRun":{
		"apiVersion":"doops.sh/v2",
		"kind":"CredentialRun",
		"id":"credrun_123",
		"mode":"${MODE}",
		"cluster":"doops",
		"instance":"runner",
		"template":"release",
		"project":"app",
		"environment":"production",
		"mutationCount":0,
		"materializations":[],
		"createdAt":"2026-07-16T00:00:00Z"
	},
	"resultContract":{"apiVersion":"doops.sh/v2","kind":"DeploymentRun","requireEvidence":true}
}`

func writeTrustedAdmissionFixture(t *testing.T, workspace string) {
	t.Helper()
	writeCredentialPlanTestFile(t, workspace, "deploy/release.yaml", `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: release
spec:
  parameters:
    releaseId:
      required: true
  application: app
  release:
    source:
      repository: https://example.test/app.git
      revision: ${inputs.releaseId}
      branch: main
  environment: production
  configurationSource: deploy/environments.yaml
`)
	writeCredentialPlanTestFile(t, workspace, "deploy/environments.yaml", `
artifactContract:
  type: image-set
  sourceRegistry: registry.example.test/source
  sourceRepository: https://example.test/app.git
  sourceBranch: main
  services: [app]
  sourceArtifactNames: {app: app}
  imageTagPattern: "^[0-9a-f]{40}$"
  imageReferenceFormat: repository@digest
verificationProfiles:
  production:
    requiredEvidence:
      - source-identity
      - image-set
      - gitops-render
      - runtime-state
      - authorization-state
      - public-contract
      - post-deploy-log-scan
      - release-manifest
environments:
  production:
    deploymentPlatform: linux/amd64
    target: {name: control, cluster: doops, instance: runner}
    deploymentTarget: {name: workload, cluster: doops, instance: app}
    executor:
      type: helm
      lifecycle: in-process
      config:
        namespace: apps
        release: app
        workload: deployment/app
        container: app
        registry: registry.example.test/target
        releaseManifestRepository: https://example.test/app.git
        chart: deploy/chart
        values: deploy/values.yaml
        imageBindings: {app: ""}
        healthChecks:
          public:
            - {id: health, url: https://app.example.test/health, expectedStatus: 200}
        authz: {appCode: APP, environmentCode: PROD}
    verificationProfile: production
`)
}
