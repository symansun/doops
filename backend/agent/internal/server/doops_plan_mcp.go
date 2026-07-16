package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	doopsPlanCallContextSchema = "doops.reconciliation-call-context/v1"
	doopsPlanAttestationSchema = "doops.tool-attestation/v1"
)

var doopsPlanToolNames = []string{
	"ObserveAuthorizationState",
	"ObserveHTTPContract",
	"ObserveHelmRelease",
	"ObserveKubernetesLogs",
	"ObserveKubernetesWorkload",
	"ObserveSourceRegistryImageSet",
	"ObserveTargetRegistryImageSet",
	"ObserveWorkspaceSource",
	"RenderHelmRelease",
	"SyncImageSetToTarget",
	"ReconcileHelmRelease",
}

var (
	doopsPlanHTTPClient = &http.Client{Timeout: 30 * time.Second}
	doopsPlanCommand    = exec.CommandContext
	doopsPlanCalls      = struct {
		sync.Mutex
		counts map[string]int
	}{counts: make(map[string]int)}
)

type doopsPlanRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
}

type doopsPlanCallContext struct {
	SchemaVersion string                          `json:"schemaVersion"`
	Context       trustedReconciliationContext    `json:"context"`
	CapabilityKey string                          `json:"capabilityKey"`
	Capability    trustedReconciliationCapability `json:"capability"`
}

type doopsPlanRuntime struct {
	Root                string
	WorkflowPath        string
	Version             string
	Plan                CredentialPlan
	SourceRegistry      string
	TargetRegistry      string
	Services            []string
	Namespace           string
	Release             string
	Chart               string
	Values              string
	ImageBindings       map[string]string
	WorkloadNamespaces  map[string]string
	PublicChecks        []credentialConfigurationHTTPCheck
	WorkloadChecks      []credentialConfigurationWorkloadCheck
	SourceRepository    string
	SourceBranch        string
	RegistryCredential  credentialConfigurationSecretRef
	ArtifactSync        credentialConfigurationArtifactSync
	VerificationProfile string
}

type doopsPlanImage struct {
	Service    string `json:"service"`
	Repository string `json:"repository"`
	Digest     string `json:"digest"`
}

func RunDoopsPlanMCP(input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var request doopsPlanRequest
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			if err := encoder.Encode(doopsPlanError(nil, -32700, "invalid JSON-RPC request")); err != nil {
				return err
			}
			continue
		}
		response := handleDoopsPlanRequest(request)
		if response == nil {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func handleDoopsPlanRequest(request doopsPlanRequest) map[string]interface{} {
	switch request.Method {
	case "initialize":
		return doopsPlanResponse(request.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
			"serverInfo":      map[string]interface{}{"name": "doops_plan", "version": "1"},
		})
	case "notifications/initialized":
		return nil
	case "tools/list":
		tools := make([]map[string]interface{}, 0, len(doopsPlanToolNames))
		for _, name := range doopsPlanToolNames {
			tools = append(tools, map[string]interface{}{
				"name":        name,
				"description": "Execute one plan-scoped DoOps observation or reconciliation using only the trusted workflow context.",
				"inputSchema": map[string]interface{}{
					"type":                 "object",
					"properties":           map[string]interface{}{},
					"additionalProperties": false,
				},
			})
		}
		return doopsPlanResponse(request.ID, map[string]interface{}{"tools": tools})
	case "tools/call":
		result, err := callDoopsPlanTool(request.Params)
		if err != nil {
			return doopsPlanError(request.ID, -32602, err.Error())
		}
		return doopsPlanResponse(request.ID, result)
	default:
		return doopsPlanError(request.ID, -32601, "method not found")
	}
}

func callDoopsPlanTool(params map[string]interface{}) (map[string]interface{}, error) {
	name, _ := params["name"].(string)
	if !containsDoopsPlanTool(name) {
		return nil, fmt.Errorf("unsupported doops_plan tool %q", name)
	}
	arguments, ok := params["arguments"].(map[string]interface{})
	if !ok || len(arguments) != 0 {
		return nil, errors.New("doops_plan tool arguments must be an exact empty object")
	}
	meta, ok := params["_meta"].(map[string]interface{})
	if !ok {
		return nil, errors.New("trusted reconciliation metadata is required")
	}
	rawContext, ok := meta["doops.sh/reconciliation-context"]
	if !ok {
		return nil, errors.New("trusted reconciliation call context is required")
	}
	encodedContext, err := json.Marshal(rawContext)
	if err != nil {
		return nil, errors.New("trusted reconciliation call context is invalid")
	}
	var callContext doopsPlanCallContext
	if err := json.Unmarshal(encodedContext, &callContext); err != nil {
		return nil, errors.New("trusted reconciliation call context is invalid")
	}
	if err := validateDoopsPlanCallContext(name, callContext); err != nil {
		return nil, err
	}
	callKey := callContext.Context.OperationID + "\x00" + name
	maxCalls := 1
	if callContext.Context.ExecutionMode == "apply" &&
		(name == "ObserveHelmRelease" || name == "ObserveKubernetesWorkload") {
		maxCalls = 2
	}
	doopsPlanCalls.Lock()
	if doopsPlanCalls.counts[callKey] >= maxCalls {
		doopsPlanCalls.Unlock()
		return nil, fmt.Errorf("doops_plan tool %q was already called for this operation", name)
	}
	doopsPlanCalls.counts[callKey]++
	doopsPlanCalls.Unlock()
	time.AfterFunc(time.Hour, func() {
		doopsPlanCalls.Lock()
		delete(doopsPlanCalls.counts, callKey)
		doopsPlanCalls.Unlock()
	})
	runtime, err := loadDoopsPlanRuntime(callContext)
	if err != nil {
		return nil, err
	}
	payload, toolErr := executeDoopsPlanTool(context.Background(), name, callContext, runtime)
	return buildDoopsPlanAttestedResult(name, callContext, arguments, payload, toolErr)
}

func validateDoopsPlanCallContext(name string, callContext doopsPlanCallContext) error {
	if callContext.SchemaVersion != doopsPlanCallContextSchema {
		return errors.New("trusted reconciliation call context schema is unsupported")
	}
	if err := callContext.Context.ValidateDigest(); err != nil {
		return err
	}
	expected, ok := callContext.Context.Capabilities[callContext.CapabilityKey]
	if !ok || expected.Tool != name {
		return errors.New("trusted reconciliation capability does not match the requested tool")
	}
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	actualJSON, err := json.Marshal(callContext.Capability)
	if err != nil || !bytes.Equal(expectedJSON, actualJSON) {
		return errors.New("trusted reconciliation capability payload mismatch")
	}
	return nil
}

func loadDoopsPlanRuntime(callContext doopsPlanCallContext) (doopsPlanRuntime, error) {
	scope := callContext.Capability.CanonicalScope
	root, err := doopsPlanScopeString(scope, "workspacePath")
	if err != nil {
		return doopsPlanRuntime{}, err
	}
	workflowPath, err := doopsPlanScopeString(scope, "workflowPath")
	if err != nil {
		return doopsPlanRuntime{}, err
	}
	inputs, err := doopsPlanScopeInputs(scope)
	if err != nil {
		return doopsPlanRuntime{}, err
	}
	root, err = validateDoopsPlanWorkspace(root, callContext.Context.Source.WorkspaceCommit)
	if err != nil {
		return doopsPlanRuntime{}, err
	}
	runtime, err := loadDoopsPlanRuntimeFromWorkspace(root, workflowPath, inputs)
	if err != nil {
		return doopsPlanRuntime{}, err
	}
	plan := runtime.Plan
	for key, value := range map[string]string{
		"cluster": plan.Cluster, "instance": plan.Instance, "project": plan.Project,
		"environment": plan.Environment, "template": plan.Template,
	} {
		actual, err := doopsPlanScopeString(scope, key)
		if err != nil || actual != value {
			return doopsPlanRuntime{}, fmt.Errorf("trusted reconciliation scope %q mismatch", key)
		}
	}
	declaredReleaseScope, ok := scope["releasePlan"].(map[string]interface{})
	if !ok {
		return doopsPlanRuntime{}, errors.New("trusted reconciliation release plan is missing")
	}
	expectedReleaseScope := doopsPlanReleaseScope(runtime)
	declaredDigest, err := digestJSON(declaredReleaseScope)
	if err != nil {
		return doopsPlanRuntime{}, err
	}
	expectedDigest, err := digestJSON(expectedReleaseScope)
	if err != nil || declaredDigest != expectedDigest {
		return doopsPlanRuntime{}, errors.New("trusted reconciliation release plan mismatch")
	}
	return runtime, nil
}

func loadDoopsPlanRuntimeFromWorkspace(
	root string,
	workflowPath string,
	inputs map[string]string,
) (doopsPlanRuntime, error) {
	plan, err := parseCredentialPlan(root, workflowPath)
	if err != nil {
		return doopsPlanRuntime{}, err
	}
	workflowData, err := readCredentialDeclarationFile(root, workflowPath)
	if err != nil {
		return doopsPlanRuntime{}, err
	}
	var identity credentialWorkflowIdentity
	if err := yaml.Unmarshal(workflowData, &identity); err != nil {
		return doopsPlanRuntime{}, invalidCredentialDeclaration(err)
	}
	if identity.APIVersion != "doops.sh/v2" || identity.Kind != "DeploymentTemplate" {
		return doopsPlanRuntime{}, errors.New("doops_plan currently requires a v2 DeploymentTemplate")
	}
	var workflow credentialDeploymentTemplate
	if err := strictCredentialYAMLDecode(workflowData, &workflow); err != nil {
		return doopsPlanRuntime{}, err
	}
	version, err := resolveDoopsPlanInput(workflow.Spec.Release.Version, inputs)
	if err != nil {
		return doopsPlanRuntime{}, err
	}
	configData, err := readCredentialDeclarationFile(root, workflow.Spec.ConfigurationSource)
	if err != nil {
		return doopsPlanRuntime{}, err
	}
	var configuration credentialConfigurationSource
	if err := strictCredentialYAMLDecode(configData, &configuration); err != nil {
		return doopsPlanRuntime{}, err
	}
	environment, ok := configuration.Environments[workflow.Spec.Environment]
	if !ok || environment.Executor.Type != "helm" {
		return doopsPlanRuntime{}, errors.New("declared environment does not provide a Helm executor")
	}
	if strings.TrimSpace(environment.DeploymentTarget.Name) != "" {
		return doopsPlanRuntime{}, errors.New("detached deployment targets require an isolated executor")
	}
	executor := environment.Executor.Config
	sourceRegistry := strings.TrimSuffix(configuration.ArtifactContract.SourceRegistry, "/")
	targetRegistry := strings.TrimSuffix(executor.Registry, "/")
	if targetRegistry == "" {
		targetRegistry = sourceRegistry
	}
	if sourceRegistry == "" || targetRegistry == "" || len(configuration.ArtifactContract.Services) == 0 {
		return doopsPlanRuntime{}, errors.New("image-set registry declaration is incomplete")
	}
	if executor.Namespace == "" || executor.Release == "" || executor.Chart == "" || executor.Values == "" {
		return doopsPlanRuntime{}, errors.New("Helm executor declaration is incomplete")
	}
	return doopsPlanRuntime{
		Root:                root,
		WorkflowPath:        workflowPath,
		Version:             version,
		Plan:                plan,
		SourceRegistry:      sourceRegistry,
		TargetRegistry:      targetRegistry,
		Services:            append([]string(nil), configuration.ArtifactContract.Services...),
		Namespace:           executor.Namespace,
		Release:             executor.Release,
		Chart:               executor.Chart,
		Values:              executor.Values,
		ImageBindings:       executor.ImageBindings,
		WorkloadNamespaces:  executor.WorkloadNamespaces,
		PublicChecks:        executor.HealthChecks.Public,
		WorkloadChecks:      executor.HealthChecks.Workloads,
		SourceRepository:    configuration.ArtifactContract.SourceRepository,
		SourceBranch:        configuration.ArtifactContract.SourceBranch,
		RegistryCredential:  executor.RegistryCredential,
		ArtifactSync:        executor.ArtifactSync,
		VerificationProfile: environment.VerificationProfile,
	}, nil
}

func trustedDoopsPlanReleaseScope(
	root string,
	workflowPath string,
	inputs map[string]string,
) (map[string]interface{}, error) {
	workflowData, err := readCredentialDeclarationFile(root, workflowPath)
	if err != nil {
		return nil, err
	}
	var identity credentialWorkflowIdentity
	if err := yaml.Unmarshal(workflowData, &identity); err != nil {
		return nil, invalidCredentialDeclaration(err)
	}
	if identity.APIVersion != "doops.sh/v2" || identity.Kind != "DeploymentTemplate" {
		return nil, nil
	}
	var workflow credentialDeploymentTemplate
	if err := strictCredentialYAMLDecode(workflowData, &workflow); err != nil {
		return nil, err
	}
	if strings.TrimSpace(workflow.Spec.Release.Version) == "" {
		return nil, nil
	}
	runtime, err := loadDoopsPlanRuntimeFromWorkspace(root, workflowPath, inputs)
	if err != nil {
		return nil, err
	}
	return doopsPlanReleaseScope(runtime), nil
}

func doopsPlanReleaseScope(runtime doopsPlanRuntime) map[string]interface{} {
	publicChecks := make([]map[string]interface{}, 0, len(runtime.PublicChecks))
	for _, check := range runtime.PublicChecks {
		publicChecks = append(publicChecks, map[string]interface{}{
			"id": check.ID, "url": check.URL, "expectedStatus": check.ExpectedStatus,
			"bodyContains": check.BodyContains,
		})
	}
	workloadChecks := make([]map[string]interface{}, 0, len(runtime.WorkloadChecks))
	for _, check := range runtime.WorkloadChecks {
		workloadChecks = append(workloadChecks, map[string]interface{}{
			"service": check.Service, "minReadyReplicas": check.MinReadyReplicas,
			"requireEndpoints": check.RequireEndpoints,
		})
	}
	return map[string]interface{}{
		"version": runtime.Version,
		"source": map[string]interface{}{
			"registry": runtime.SourceRegistry, "repository": runtime.SourceRepository,
			"branch": runtime.SourceBranch, "services": runtime.Services,
		},
		"targetRegistry":     runtime.TargetRegistry,
		"registryCredential": doopsPlanSecretRefScope(runtime.RegistryCredential),
		"artifactSync": map[string]interface{}{
			"sourceCredentialRef":   doopsPlanSecretRefScope(runtime.ArtifactSync.SourceCredentialRef),
			"registryCredentialRef": doopsPlanSecretRefScope(runtime.ArtifactSync.RegistryCredentialRef),
		},
		"helm": map[string]interface{}{
			"namespace": runtime.Namespace, "release": runtime.Release,
			"chart": runtime.Chart, "values": runtime.Values,
			"imageBindings":      runtime.ImageBindings,
			"workloadNamespaces": runtime.WorkloadNamespaces,
		},
		"healthChecks": map[string]interface{}{
			"public": publicChecks, "workloads": workloadChecks,
		},
		"verificationProfile": runtime.VerificationProfile,
	}
}

func doopsPlanSecretRefScope(reference credentialConfigurationSecretRef) map[string]interface{} {
	return map[string]interface{}{
		"kind": reference.Kind, "name": reference.Name, "namespace": reference.Namespace,
		"secretName": reference.SecretName, "key": reference.Key,
	}
}

func executeDoopsPlanTool(
	ctx context.Context,
	name string,
	callContext doopsPlanCallContext,
	runtime doopsPlanRuntime,
) (map[string]interface{}, error) {
	switch name {
	case "ObserveWorkspaceSource":
		return observeDoopsPlanWorkspace(ctx, callContext, runtime)
	case "ObserveSourceRegistryImageSet":
		images, err := resolveDoopsPlanImageSet(ctx, runtime.SourceRegistry, runtime.Services, runtime.Version)
		return map[string]interface{}{"registry": runtime.SourceRegistry, "version": runtime.Version, "images": images}, err
	case "ObserveTargetRegistryImageSet":
		images, err := resolveDoopsPlanImageSet(ctx, runtime.TargetRegistry, runtime.Services, runtime.Version)
		return map[string]interface{}{"registry": runtime.TargetRegistry, "version": runtime.Version, "images": images}, err
	case "RenderHelmRelease":
		return renderDoopsPlanHelm(ctx, runtime)
	case "ObserveHelmRelease":
		return observeDoopsPlanHelm(ctx, runtime)
	case "ObserveKubernetesWorkload":
		return observeDoopsPlanWorkloads(ctx, runtime)
	case "ObserveAuthorizationState":
		sameRegistry := runtime.SourceRegistry == runtime.TargetRegistry
		registryMode := "same-registry"
		if !sameRegistry {
			registryMode = "unsupported-cross-registry"
		}
		return map[string]interface{}{
			"authorized":   sameRegistry && (callContext.Context.ExecutionMode == "dry-run" || callContext.Context.MutationAuthorized),
			"mode":         callContext.Context.ExecutionMode,
			"target":       runtime.Plan.Target,
			"registryMode": registryMode,
		}, nil
	case "ObserveHTTPContract":
		return observeDoopsPlanHTTP(ctx, runtime)
	case "ObserveKubernetesLogs":
		return observeDoopsPlanLogs(ctx, runtime)
	case "SyncImageSetToTarget":
		if !callContext.Context.MutationAuthorized {
			return nil, errors.New("image synchronization is not authorized in dry-run mode")
		}
		if runtime.SourceRegistry != runtime.TargetRegistry {
			return nil, errors.New("cross-registry synchronization requires a declared detached copy executor")
		}
		images, err := resolveDoopsPlanImageSet(ctx, runtime.TargetRegistry, runtime.Services, runtime.Version)
		return map[string]interface{}{"changed": false, "registry": runtime.TargetRegistry, "images": images}, err
	case "ReconcileHelmRelease":
		if !callContext.Context.MutationAuthorized {
			return nil, errors.New("Helm reconciliation is not authorized in dry-run mode")
		}
		if runtime.SourceRegistry != runtime.TargetRegistry {
			return nil, errors.New("Helm reconciliation requires a completed detached cross-registry copy")
		}
		return reconcileDoopsPlanHelm(ctx, runtime)
	default:
		return nil, fmt.Errorf("unsupported doops_plan tool %q", name)
	}
}

func observeDoopsPlanWorkspace(
	ctx context.Context,
	callContext doopsPlanCallContext,
	runtime doopsPlanRuntime,
) (map[string]interface{}, error) {
	_ = ctx
	return map[string]interface{}{
		"repository":      callContext.Context.Source.Repository,
		"revision":        callContext.Context.Source.Revision,
		"branch":          callContext.Context.Source.Branch,
		"workspaceCommit": callContext.Context.Source.WorkspaceCommit,
		"workflowPath":    runtime.WorkflowPath,
	}, nil
}

func renderDoopsPlanHelm(ctx context.Context, runtime doopsPlanRuntime) (map[string]interface{}, error) {
	images, err := resolveDoopsPlanImageSet(ctx, runtime.TargetRegistry, runtime.Services, runtime.Version)
	if err != nil {
		return nil, err
	}
	args, err := doopsPlanHelmArgs(runtime, images, false)
	if err != nil {
		return nil, err
	}
	rendered, err := runDoopsPlanCommand(ctx, runtime.Root, "helm", args...)
	if err != nil {
		return nil, err
	}
	digest, err := digestJSON(string(rendered))
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"release": runtime.Release, "namespace": runtime.Namespace,
		"renderDigest": digest, "documentCount": strings.Count(rendered, "\n---") + 1,
		"images": images,
	}, nil
}

func observeDoopsPlanHelm(ctx context.Context, runtime doopsPlanRuntime) (map[string]interface{}, error) {
	output, err := runDoopsPlanCommand(ctx, runtime.Root, "helm", "status", runtime.Release, "-n", runtime.Namespace, "-o", "json")
	if err != nil {
		return nil, err
	}
	var status map[string]interface{}
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		return nil, errors.New("Helm status output is invalid")
	}
	info, _ := status["info"].(map[string]interface{})
	return map[string]interface{}{
		"release": runtime.Release, "namespace": runtime.Namespace,
		"revision": status["version"], "status": info["status"],
	}, nil
}

func observeDoopsPlanWorkloads(ctx context.Context, runtime doopsPlanRuntime) (map[string]interface{}, error) {
	services := doopsPlanWorkloadServices(runtime)
	workloads := make([]map[string]interface{}, 0, len(services))
	for _, service := range services {
		namespace := runtime.WorkloadNamespaces[service]
		if namespace == "" {
			namespace = runtime.Namespace
		}
		output, err := runDoopsPlanCommand(ctx, runtime.Root, "kubectl", "-n", namespace, "get", "deployment", service, "-o", "json")
		if err != nil {
			return nil, err
		}
		var deployment struct {
			Spec struct {
				Replicas *int `json:"replicas"`
				Template struct {
					Spec struct {
						Containers []struct {
							Name  string `json:"name"`
							Image string `json:"image"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas     int `json:"readyReplicas"`
				AvailableReplicas int `json:"availableReplicas"`
				UpdatedReplicas   int `json:"updatedReplicas"`
			} `json:"status"`
		}
		if err := json.Unmarshal([]byte(output), &deployment); err != nil {
			return nil, errors.New("Kubernetes workload output is invalid")
		}
		workloads = append(workloads, map[string]interface{}{
			"service": service, "namespace": namespace, "desiredReplicas": deployment.Spec.Replicas,
			"readyReplicas":     deployment.Status.ReadyReplicas,
			"availableReplicas": deployment.Status.AvailableReplicas,
			"updatedReplicas":   deployment.Status.UpdatedReplicas,
			"containers":        deployment.Spec.Template.Spec.Containers,
		})
	}
	return map[string]interface{}{"workloads": workloads}, nil
}

func doopsPlanWorkloadServices(runtime doopsPlanRuntime) []string {
	if len(runtime.WorkloadChecks) == 0 {
		return append([]string(nil), runtime.Services...)
	}
	services := make([]string, 0, len(runtime.WorkloadChecks))
	for _, check := range runtime.WorkloadChecks {
		services = append(services, check.Service)
	}
	return services
}

func observeDoopsPlanHTTP(ctx context.Context, runtime doopsPlanRuntime) (map[string]interface{}, error) {
	checks := make([]map[string]interface{}, 0, len(runtime.PublicChecks))
	for _, check := range runtime.PublicChecks {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, check.URL, nil)
		if err != nil {
			return nil, errors.New("public check URL is invalid")
		}
		response, err := doopsPlanHTTPClient.Do(request)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
		response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		bodyMatched := check.BodyContains == "" || bytes.Contains(body, []byte(check.BodyContains))
		passed := response.StatusCode == check.ExpectedStatus && bodyMatched
		checks = append(checks, map[string]interface{}{
			"id": check.ID, "url": check.URL, "status": response.StatusCode,
			"bodyMatched": bodyMatched, "passed": passed,
		})
		if !passed {
			return map[string]interface{}{"checks": checks}, fmt.Errorf("public check %q failed", check.ID)
		}
	}
	return map[string]interface{}{"checks": checks}, nil
}

func observeDoopsPlanLogs(ctx context.Context, runtime doopsPlanRuntime) (map[string]interface{}, error) {
	results := make([]map[string]interface{}, 0, len(runtime.Services))
	pattern := regexp.MustCompile(`(?i)(panic|fatal|exception|error)`)
	for _, service := range runtime.Services {
		namespace := runtime.WorkloadNamespaces[service]
		if namespace == "" {
			namespace = runtime.Namespace
		}
		output, err := runDoopsPlanCommand(
			ctx, runtime.Root, "kubectl", "-n", namespace, "logs",
			"deployment/"+service, "--tail=200", "--all-containers=true",
		)
		if err != nil {
			return nil, err
		}
		matches := 0
		for _, line := range strings.Split(output, "\n") {
			if pattern.MatchString(line) {
				matches++
			}
		}
		results = append(results, map[string]interface{}{
			"service": service, "namespace": namespace, "scannedLines": len(strings.Split(output, "\n")),
			"errorLineCount": matches,
		})
	}
	return map[string]interface{}{"logs": results}, nil
}

func reconcileDoopsPlanHelm(ctx context.Context, runtime doopsPlanRuntime) (map[string]interface{}, error) {
	images, err := resolveDoopsPlanImageSet(ctx, runtime.TargetRegistry, runtime.Services, runtime.Version)
	if err != nil {
		return nil, err
	}
	args, err := doopsPlanHelmArgs(runtime, images, true)
	if err != nil {
		return nil, err
	}
	if _, err := runDoopsPlanCommand(ctx, runtime.Root, "helm", args...); err != nil {
		return nil, err
	}
	status, err := observeDoopsPlanHelm(ctx, runtime)
	if err != nil {
		return nil, err
	}
	status["images"] = images
	status["converged"] = true
	return status, nil
}

func doopsPlanHelmArgs(runtime doopsPlanRuntime, images []doopsPlanImage, apply bool) ([]string, error) {
	chart, err := resolveDoopsPlanWorkspacePath(runtime.Root, runtime.Chart)
	if err != nil {
		return nil, err
	}
	values, err := resolveDoopsPlanWorkspacePath(runtime.Root, runtime.Values)
	if err != nil {
		return nil, err
	}
	args := []string{"template", runtime.Release, chart, "-n", runtime.Namespace, "-f", values}
	if apply {
		args = []string{
			"upgrade", "--install", runtime.Release, chart, "-n", runtime.Namespace,
			"-f", values, "--atomic", "--wait", "--timeout", "10m",
		}
	}
	for _, image := range images {
		binding := runtime.ImageBindings[image.Service]
		imagePath := "image"
		if binding != "" {
			imagePath = binding + ".image"
		}
		args = append(args,
			"--set-string", imagePath+".repository="+image.Repository,
			"--set-string", imagePath+".digest="+image.Digest,
		)
	}
	return args, nil
}

func resolveDoopsPlanImageSet(
	ctx context.Context,
	registry string,
	services []string,
	version string,
) ([]doopsPlanImage, error) {
	images := make([]doopsPlanImage, 0, len(services))
	for _, service := range services {
		repository := strings.TrimSuffix(registry, "/") + "/" + service
		digest, err := resolveDoopsPlanRegistryDigest(ctx, repository, version)
		if err != nil {
			return nil, fmt.Errorf("resolve image %s:%s: %w", repository, version, err)
		}
		images = append(images, doopsPlanImage{
			Service: service, Repository: repository, Digest: digest,
		})
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Service < images[j].Service })
	return images, nil
}

func resolveDoopsPlanRegistryDigest(ctx context.Context, repository, reference string) (string, error) {
	host, path, ok := strings.Cut(repository, "/")
	if !ok || host == "" || path == "" {
		return "", errors.New("registry repository is invalid")
	}
	endpoint := "https://" + host + "/v2/" + path + "/manifests/" + url.PathEscape(reference)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	response, err := doopsPlanHTTPClient.Do(request)
	if err != nil {
		return "", err
	}
	if response.StatusCode == http.StatusUnauthorized {
		challenge := response.Header.Get("Www-Authenticate")
		response.Body.Close()
		bearer, err := doopsPlanRegistryBearer(ctx, challenge, path)
		if err != nil {
			return "", err
		}
		request, _ = http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		request.Header.Set("Accept", strings.Join([]string{
			"application/vnd.oci.image.index.v1+json",
			"application/vnd.docker.distribution.manifest.list.v2+json",
			"application/vnd.oci.image.manifest.v1+json",
			"application/vnd.docker.distribution.manifest.v2+json",
		}, ", "))
		request.Header.Set("Authorization", "Bearer "+bearer)
		response, err = doopsPlanHTTPClient.Do(request)
		if err != nil {
			return "", err
		}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("registry manifest returned HTTP %d", response.StatusCode)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4*1024*1024))
	digest := strings.TrimSpace(response.Header.Get("Docker-Content-Digest"))
	if !validSHA256Digest(digest) {
		return "", errors.New("registry manifest digest is missing")
	}
	return digest, nil
}

func doopsPlanRegistryBearer(ctx context.Context, challenge, repository string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", errors.New("registry authentication challenge is unsupported")
	}
	parameters := map[string]string{}
	for _, match := range regexp.MustCompile(`([A-Za-z]+)="([^"]*)"`).FindAllStringSubmatch(challenge, -1) {
		parameters[strings.ToLower(match[1])] = match[2]
	}
	realm := parameters["realm"]
	if realm == "" {
		return "", errors.New("registry authentication realm is missing")
	}
	tokenURL, err := url.Parse(realm)
	if err != nil || tokenURL.Scheme != "https" {
		return "", errors.New("registry authentication realm is invalid")
	}
	query := tokenURL.Query()
	if service := parameters["service"]; service != "" {
		query.Set("service", service)
	}
	scope := parameters["scope"]
	if scope == "" {
		scope = "repository:" + repository + ":pull"
	}
	query.Set("scope", scope)
	tokenURL.RawQuery = query.Encode()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL.String(), nil)
	response, err := doopsPlanHTTPClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("registry authentication returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&payload); err != nil {
		return "", errors.New("registry authentication response is invalid")
	}
	if payload.Token == "" {
		payload.Token = payload.AccessToken
	}
	if payload.Token == "" {
		return "", errors.New("registry authentication response is empty")
	}
	return payload.Token, nil
}

func buildDoopsPlanAttestedResult(
	name string,
	callContext doopsPlanCallContext,
	arguments map[string]interface{},
	payload map[string]interface{},
	toolErr error,
) (map[string]interface{}, error) {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	if toolErr != nil {
		payload["ok"] = false
		payload["failure"] = toolErr.Error()
	} else {
		payload["ok"] = true
	}
	text, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{
		"content":           []interface{}{map[string]interface{}{"type": "text", "text": string(text)}},
		"structuredContent": payload,
		"isError":           toolErr != nil,
	}
	resultDigest, err := digestJSON(result)
	if err != nil {
		return nil, err
	}
	inputDigest, err := digestJSON(map[string]interface{}{
		"arguments": arguments, "capabilityKey": callContext.CapabilityKey,
		"contextDigest": callContext.Context.ContextDigest, "tool": name,
	})
	if err != nil {
		return nil, err
	}
	result["_meta"] = map[string]interface{}{
		"doops.sh/tool-attestation": map[string]interface{}{
			"schemaVersion":        doopsPlanAttestationSchema,
			"contextSchemaVersion": callContext.Context.SchemaVersion,
			"operationId":          callContext.Context.OperationID,
			"contextDigest":        callContext.Context.ContextDigest,
			"planDigest":           callContext.Context.PlanDigest,
			"planBindingDigest":    callContext.Context.PlanBindingDigest,
			"executionMode":        callContext.Context.ExecutionMode,
			"mutationAuthorized":   callContext.Context.MutationAuthorized,
			"capabilityKey":        callContext.CapabilityKey,
			"tool":                 name,
			"evidenceKind":         callContext.Capability.EvidenceKind,
			"evidenceSubject":      callContext.Capability.EvidenceSubject,
			"canonicalScope":       callContext.Capability.CanonicalScope,
			"scopeDigest":          callContext.Capability.ScopeDigest,
			"inputDigest":          inputDigest,
			"resultDigest":         resultDigest,
		},
	}
	return result, nil
}

func validateDoopsPlanWorkspace(root, workspaceCommit string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", errors.New("trusted workspace path is unavailable")
	}
	base := strings.TrimSpace(os.Getenv("DOOPS_PLAN_WORKSPACE_BASE"))
	if base == "" {
		base = "/root/ws"
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		return "", errors.New("trusted workspace base is unavailable")
	}
	relative, err := filepath.Rel(base, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("trusted workspace escapes the configured workspace base")
	}
	ready, err := os.ReadFile(filepath.Join(resolved, ".doops-ready"))
	if err != nil || strings.TrimSpace(string(ready)) != workspaceCommit {
		return "", errors.New("trusted workspace commit marker mismatch")
	}
	session := filepath.Base(resolved)
	if session == "." || session == ".." || strings.ContainsAny(session, `/\`) {
		return "", errors.New("trusted workspace session identity is invalid")
	}
	bareBase := strings.TrimSpace(os.Getenv("DOOPS_PLAN_BARE_REPOSITORY_BASE"))
	if bareBase == "" {
		bareBase = "/tmp/repos"
	}
	bareRepository := filepath.Join(bareBase, session+".git")
	if _, err := os.Stat(bareRepository); err != nil {
		return "", errors.New("trusted workspace source repository is unavailable")
	}
	verifyCommit := doopsPlanCommand(
		context.Background(),
		"git",
		"--git-dir="+bareRepository,
		"cat-file",
		"-e",
		workspaceCommit+"^{commit}",
	)
	if output, err := verifyCommit.CombinedOutput(); err != nil {
		return "", fmt.Errorf("trusted workspace commit is unavailable: %s", strings.TrimSpace(string(output)))
	}
	indexPath, err := temporaryDoopsPlanIndexPath()
	if err != nil {
		return "", err
	}
	defer os.Remove(indexPath)
	readTree := doopsPlanCommand(
		context.Background(),
		"git",
		"--git-dir="+bareRepository,
		"read-tree",
		workspaceCommit,
	)
	readTree.Env = append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
	if output, err := readTree.CombinedOutput(); err != nil {
		return "", fmt.Errorf("trusted workspace index initialization failed: %s", strings.TrimSpace(string(output)))
	}
	refreshIndex := doopsPlanCommand(
		context.Background(),
		"git",
		"--git-dir="+bareRepository,
		"--work-tree="+resolved,
		"update-index",
		"--refresh",
	)
	refreshIndex.Env = append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
	_, _ = refreshIndex.CombinedOutput()
	verifyTree := doopsPlanCommand(
		context.Background(),
		"git",
		"--git-dir="+bareRepository,
		"--work-tree="+resolved,
		"diff-files",
		"--quiet",
		"--",
		".",
	)
	verifyTree.Env = append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
	if err := verifyTree.Run(); err != nil {
		return "", errors.New("trusted workspace differs from the pushed source snapshot")
	}
	if err := verifyDoopsPlanWorkspaceFileSet(resolved, bareRepository, workspaceCommit); err != nil {
		return "", err
	}
	return resolved, nil
}

func verifyDoopsPlanWorkspaceFileSet(root, bareRepository, workspaceCommit string) error {
	command := doopsPlanCommand(
		context.Background(),
		"git",
		"--git-dir="+bareRepository,
		"ls-tree",
		"-r",
		"-z",
		"--name-only",
		workspaceCommit,
	)
	output, err := command.Output()
	if err != nil {
		return errors.New("trusted workspace file manifest is unavailable")
	}
	expected := make(map[string]struct{})
	for _, raw := range bytes.Split(output, []byte{0}) {
		if len(raw) > 0 {
			expected[filepath.ToSlash(string(raw))] = struct{}{}
		}
	}
	actual := make(map[string]struct{})
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			return nil
		}
		if relative == ".git" || strings.HasPrefix(relative, ".git/") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if relative == ".doops-ready" || relative == ".doops-audit-log" {
			return nil
		}
		if !entry.IsDir() {
			actual[relative] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return errors.New("trusted workspace file manifest is unavailable")
	}
	if !reflectStringSet(expected, actual) {
		return errors.New("trusted workspace contains files outside the pushed source snapshot")
	}
	return nil
}

func reflectStringSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for key := range left {
		if _, ok := right[key]; !ok {
			return false
		}
	}
	return true
}

func temporaryDoopsPlanIndexPath() (string, error) {
	file, err := os.CreateTemp("", "doops-plan-index-*")
	if err != nil {
		return "", errors.New("trusted workspace index path is unavailable")
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", errors.New("trusted workspace index path is unavailable")
	}
	if err := os.Remove(path); err != nil {
		return "", errors.New("trusted workspace index path is unavailable")
	}
	return path, nil
}

func resolveDoopsPlanWorkspacePath(root, relative string) (string, error) {
	clean, err := credentialDeclarationRelativePath(relative)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, clean)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	within, err := filepath.Rel(root, resolved)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", errors.New("declared path escapes the trusted workspace")
	}
	return resolved, nil
}

func resolveDoopsPlanInput(declaration string, inputs map[string]string) (string, error) {
	const prefix = "${inputs."
	declaration = strings.TrimSpace(declaration)
	if strings.HasPrefix(declaration, prefix) && strings.HasSuffix(declaration, "}") {
		name := strings.TrimSuffix(strings.TrimPrefix(declaration, prefix), "}")
		value := strings.TrimSpace(inputs[name])
		if value == "" {
			return "", fmt.Errorf("workflow input %q is missing", name)
		}
		return value, nil
	}
	if declaration == "" || strings.Contains(declaration, "${") {
		return "", errors.New("release version declaration is invalid")
	}
	return declaration, nil
}

func doopsPlanScopeString(scope map[string]interface{}, key string) (string, error) {
	value, _ := scope[key].(string)
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("trusted reconciliation scope %q is missing", key)
	}
	return value, nil
}

func doopsPlanScopeInputs(scope map[string]interface{}) (map[string]string, error) {
	raw, ok := scope["inputs"].(map[string]interface{})
	if !ok {
		return nil, errors.New("trusted reconciliation workflow inputs are missing")
	}
	inputs := make(map[string]string, len(raw))
	for key, value := range raw {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(key) == "" || strings.TrimSpace(text) != text {
			return nil, errors.New("trusted reconciliation workflow inputs are invalid")
		}
		inputs[key] = text
	}
	return inputs, nil
}

func runDoopsPlanCommand(
	ctx context.Context,
	workingDirectory string,
	name string,
	arguments ...string,
) (string, error) {
	command := doopsPlanCommand(ctx, name, arguments...)
	command.Dir = workingDirectory
	output, err := command.CombinedOutput()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return "", fmt.Errorf("%s failed with exit status %d", name, exitError.ExitCode())
		}
		return "", fmt.Errorf("%s failed", name)
	}
	return strings.TrimSpace(string(output)), nil
}

func containsDoopsPlanTool(name string) bool {
	for _, candidate := range doopsPlanToolNames {
		if name == candidate {
			return true
		}
	}
	return false
}

func doopsPlanResponse(id interface{}, result interface{}) map[string]interface{} {
	return map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result}
}

func doopsPlanError(id interface{}, code int, message string) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]interface{}{"code": code, "message": message},
	}
}
