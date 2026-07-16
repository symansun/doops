package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
		"credentialRun":{"id":"credrun_123","materializations":[]},
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

func TestTrustedReconciliationContextDigestMatchesDoagentCanonicalJSON(t *testing.T) {
	context := trustedReconciliationContext{
		SchemaVersion:      trustedReconciliationContextSchema,
		OperationID:        "op_" + strings.Repeat("0", 32),
		ExecutionMode:      "dry-run",
		MutationAuthorized: false,
		PlanDigest:         "sha256:" + strings.Repeat("1", 64),
		PlanBindingDigest:  "sha256:" + strings.Repeat("2", 64),
		Source: trustedReconciliationSource{
			Repository:      "https://example.test/app.git",
			Revision:        strings.Repeat("b", 40),
			Branch:          "main",
			WorkspaceCommit: strings.Repeat("a", 40),
		},
		Capabilities: map[string]trustedReconciliationCapability{
			"ObserveWorkspaceSource": {
				Tool:            "ObserveWorkspaceSource",
				EvidenceKind:    "source-identity",
				EvidenceSubject: "source-identity",
				CanonicalScope: map[string]interface{}{
					"environment": "test",
					"project":     "ecp",
					"tool":        "mcp_doops_plan_ObserveWorkspaceSource",
				},
				ScopeDigest: "sha256:" + strings.Repeat("3", 64),
			},
		},
	}

	digest, err := context.calculatedDigest()
	if err != nil {
		t.Fatalf("calculate context digest: %v", err)
	}
	const want = "sha256:511ccf1c6389a50280913da368d30dffa425e9291ec403c43ca55d0a63edda0d"
	if digest != want {
		t.Fatalf("context digest = %q, want DoAgent JCS digest %q", digest, want)
	}
}

func TestDigestJSONMatchesJCSStringNumberAndUTF16Ordering(t *testing.T) {
	value := map[string]interface{}{
		"html":    "a&b<c>d",
		"unicode": "line\u2028sep\u2029end",
		"numbers": []interface{}{
			333333333.33333329,
			1e30,
			4.50,
			2e-3,
			1e-27,
			json.Number("-0"),
			json.Number("9007199254740993"),
		},
		"nested": map[string]interface{}{
			"\U0001f600": "supplementary",
			"\u20ac":     "bmp",
		},
	}

	digest, err := digestJSON(value)
	if err != nil {
		t.Fatalf("digest canonical JSON: %v", err)
	}
	const want = "sha256:138bc68e5d8a50403aa62eb6ce9332e6e835d8b465a7aee7e2f115908a8ea9a2"
	if digest != want {
		t.Fatalf("canonical digest = %q, want DoAgent JCS digest %q", digest, want)
	}
}

func TestBuildTrustedReconciliationAdmissionUsesRegistrySourceForCurrentReleaseShapes(t *testing.T) {
	for _, test := range []struct {
		name            string
		workflow        string
		inputs          string
		wantRevision    string
		workspaceCommit string
	}{
		{
			name: "version release binds workspace commit",
			workflow: `
apiVersion: doops.sh/v2
kind: DeploymentTemplate
metadata:
  name: ecp-test-deploy
spec:
  parameters:
    version:
      required: true
  application: ecp
  release:
    version: ${inputs.version}
  environment: test
  configurationSource: deploy/environments.yaml
`,
			inputs:          `"version":"release-20260716"`,
			wantRevision:    strings.Repeat("c", 40),
			workspaceCommit: strings.Repeat("c", 40),
		},
		{
			name: "semantic release binds declared revision",
			workflow: `
apiVersion: doops.sh/v2
kind: SemanticRelease
metadata:
  name: zhiyong-test-release
spec:
  parameters:
    revision:
      required: true
  application: zhiyong
  release:
    revision: ${inputs.revision}
  environment: test
  configurationSource: deploy/environments.yaml
  serviceCatalog:
    frontend:
      uses: services/frontend.yaml
`,
			inputs:          `"revision":"` + strings.Repeat("d", 40) + `"`,
			wantRevision:    strings.Repeat("d", 40),
			workspaceCommit: strings.Repeat("c", 40),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("DOOPS_WORKSPACE_ROOT", root)
			sessionID := "current-release-" + strings.ReplaceAll(test.name, " ", "-")
			workspace := filepath.Join(root, sessionID)
			if err := os.MkdirAll(filepath.Join(workspace, "deploy"), 0o700); err != nil {
				t.Fatalf("create workspace: %v", err)
			}
			writeCredentialPlanTestFile(t, workspace, "deploy/release.yaml", test.workflow)
			writeCredentialPlanTestFile(t, workspace, "deploy/environments.yaml", `
apiVersion: doops.sh/v2
kind: EnvironmentRegistry
metadata:
  name: current-environments
artifactContract:
  type: image-set
  sourceRepository: https://example.test/current.git
  sourceBranch: main
  sourceRegistry: registry.example.test/source
  services: [frontend]
  imageTagPattern: "^release-[0-9]{8}$"
verificationProfiles:
  application:
    requiredEvidence: [runtime-state]
environments:
  test:
    target: {name: gw-oilan-node, cluster: doops-oilan, instance: oilan-node}
    executor:
      type: helm
      config:
        namespace: test
        release: app
        registry: registry.example.test/target
        chart: deploy/chart
        values: deploy/values.yaml
    verificationProfile: application
`)
			instruction := `{
				"task":"execute-doops-cicd-workflow",
				"skill":"$doops-cicd",
				"executionMode":"dry-run",
				"runId":"current-release-0123456789abcdef",
				"workflowPath":"deploy/release.yaml",
				"workspaceCommit":"` + test.workspaceCommit + `",
				"inputs":{` + test.inputs + `},
				"credentialRun":{"id":"credrun_123","materializations":[]},
				"resultContract":{"apiVersion":"doops.sh/v2","kind":"DeploymentRun","requireEvidence":true}
			}`
			admission, err := buildTrustedReconciliationAdmission(sessionID, instruction, test.workspaceCommit)
			if err != nil {
				t.Fatalf("build trusted admission: %v", err)
			}
			if admission.Context.Source.Repository != "https://example.test/current.git" ||
				admission.Context.Source.Branch != "main" ||
				admission.Context.Source.Revision != test.wantRevision {
				t.Fatalf("unexpected source: %#v", admission.Context.Source)
			}
		})
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
	"credentialRun":{"id":"credrun_123","materializations":[]},
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
