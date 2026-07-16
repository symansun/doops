package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type trustedReconciliationAdmission struct {
	AllowedTools []string                     `json:"allowedTools"`
	Context      trustedReconciliationContext `json:"context"`
}

const trustedReconciliationContextSchema = "doops.reconciliation-context/v1"

type trustedReconciliationContext struct {
	SchemaVersion      string                                     `json:"schemaVersion"`
	OperationID        string                                     `json:"operationId"`
	ExecutionMode      string                                     `json:"executionMode"`
	MutationAuthorized bool                                       `json:"mutationAuthorized"`
	PlanDigest         string                                     `json:"planDigest"`
	PlanBindingDigest  string                                     `json:"planBindingDigest"`
	ContextDigest      string                                     `json:"contextDigest,omitempty"`
	Source             trustedReconciliationSource                `json:"source"`
	Capabilities       map[string]trustedReconciliationCapability `json:"capabilities"`
}

type trustedReconciliationSource struct {
	Repository      string `json:"repository"`
	Revision        string `json:"revision"`
	Branch          string `json:"branch,omitempty"`
	WorkspaceCommit string `json:"workspaceCommit"`
}

type trustedReconciliationCapability struct {
	Tool            string                 `json:"tool"`
	EvidenceKind    string                 `json:"evidenceKind"`
	EvidenceSubject string                 `json:"evidenceSubject"`
	CanonicalScope  map[string]interface{} `json:"canonicalScope"`
	ScopeDigest     string                 `json:"scopeDigest"`
}

type doopsCICDInstructionEnvelope struct {
	Task            string            `json:"task"`
	Skill           string            `json:"skill"`
	ExecutionMode   string            `json:"executionMode"`
	RunID           string            `json:"runId"`
	WorkflowPath    string            `json:"workflowPath"`
	WorkspaceCommit string            `json:"workspaceCommit"`
	Inputs          map[string]string `json:"inputs"`
	CredentialRun   CredentialRun     `json:"credentialRun"`
	ResultContract  struct {
		APIVersion      string `json:"apiVersion"`
		Kind            string `json:"kind"`
		RequireEvidence bool   `json:"requireEvidence"`
	} `json:"resultContract"`
}

func buildDoagentPromptParams(sessionID, prompt string, admission *trustedReconciliationAdmission, structured bool) map[string]interface{} {
	params := map[string]interface{}{
		"sessionId":    sessionID,
		"prompt":       prompt,
		"allowedTools": []string{},
		"toolsEnabled": false,
	}
	if !structured || admission == nil {
		return params
	}
	params["allowedTools"] = append([]string(nil), admission.AllowedTools...)
	params["trustedReconciliationContext"] = admission.Context
	params["toolsEnabled"] = true
	return params
}

func (c trustedReconciliationContext) ValidateDigest() error {
	digest, err := c.calculatedDigest()
	if err != nil {
		return err
	}
	if c.ContextDigest != digest {
		return fmt.Errorf("trusted reconciliation context digest mismatch")
	}
	for key, capability := range c.Capabilities {
		scopeDigest, err := digestJSON(capability.CanonicalScope)
		if err != nil {
			return fmt.Errorf("digest capability %q scope: %w", key, err)
		}
		if capability.ScopeDigest != scopeDigest {
			return fmt.Errorf("trusted reconciliation capability %q scope digest mismatch", key)
		}
	}
	return nil
}

func (c trustedReconciliationContext) calculatedDigest() (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	var object map[string]interface{}
	if err := json.Unmarshal(data, &object); err != nil {
		return "", err
	}
	delete(object, "contextDigest")
	return digestJSON(object)
}

func buildTrustedReconciliationAdmission(sessionID, instruction, workspaceCommit string) (trustedReconciliationAdmission, error) {
	sessionID = strings.TrimSpace(sessionID)
	workspaceCommit = strings.TrimSpace(workspaceCommit)
	if sessionID == "" || !validCredentialWorkspaceCommit(workspaceCommit) {
		return trustedReconciliationAdmission{}, fmt.Errorf("trusted reconciliation identity is invalid")
	}

	envelope, err := decodeDoopsCICDInstruction(instruction)
	if err != nil {
		return trustedReconciliationAdmission{}, err
	}
	if envelope.WorkspaceCommit != workspaceCommit {
		return trustedReconciliationAdmission{}, fmt.Errorf("reconciliation instruction binding is invalid")
	}
	for key, value := range envelope.Inputs {
		if credentialSecretLikeKey(key) {
			return trustedReconciliationAdmission{}, fmt.Errorf("sensitive workflow input %q is forbidden", key)
		}
		if credentialScalarContainsInlineMaterial(value) {
			return trustedReconciliationAdmission{}, fmt.Errorf("workflow input %q contains inline credential material", key)
		}
	}

	root, err := workspacePath(sessionID)
	if err != nil {
		return trustedReconciliationAdmission{}, err
	}
	parsedWorkflow, err := parseCredentialWorkflow(root, envelope.WorkflowPath)
	if err != nil {
		return trustedReconciliationAdmission{}, err
	}
	workflow := parsedWorkflow.Template
	if envelope.ResultContract.APIVersion != workflow.APIVersion {
		return trustedReconciliationAdmission{}, fmt.Errorf("reconciliation result contract version does not match workflow")
	}
	if err := validateTrustedWorkflowInputs(workflow.Spec.Parameters, envelope.Inputs); err != nil {
		return trustedReconciliationAdmission{}, err
	}
	var environment credentialEnvironmentContext
	if parsedWorkflow.Environment != nil {
		environment = *parsedWorkflow.Environment
	} else {
		environment, err = parseCredentialEnvironment(
			root,
			workflow.Spec.ConfigurationSource,
			workflow.Spec.Environment,
		)
		if err != nil {
			return trustedReconciliationAdmission{}, err
		}
	}
	revisionDeclaration := workflow.Spec.Release.Source.Revision
	if strings.TrimSpace(revisionDeclaration) == "" {
		revisionDeclaration = parsedWorkflow.SemanticRevision
	}
	if strings.TrimSpace(revisionDeclaration) == "" {
		revisionDeclaration = workspaceCommit
	}
	revision, err := resolveTrustedRevision(revisionDeclaration, envelope.Inputs)
	if err != nil {
		return trustedReconciliationAdmission{}, err
	}
	plan, err := parseCredentialPlan(root, envelope.WorkflowPath)
	if err != nil {
		return trustedReconciliationAdmission{}, err
	}
	source := trustedReconciliationSource{
		Repository:      selectTrustedSource(workflow.Spec.Release.Source.Repository, environment.SourceRepository),
		Revision:        revision,
		Branch:          selectTrustedSource(workflow.Spec.Release.Source.Branch, environment.SourceBranch),
		WorkspaceCommit: workspaceCommit,
	}
	if source.Repository == "" || source.Branch == "" || !validLowerHex(source.Revision, 40) {
		return trustedReconciliationAdmission{}, fmt.Errorf("release source identity is incomplete")
	}
	if envelope.CredentialRun.Cluster != plan.Cluster ||
		envelope.CredentialRun.Instance != plan.Instance ||
		envelope.CredentialRun.Template != plan.Template ||
		envelope.CredentialRun.Project != plan.Project ||
		envelope.CredentialRun.Environment != plan.Environment {
		return trustedReconciliationAdmission{}, fmt.Errorf("credential run target binding is invalid")
	}
	credentialRunDigest, err := digestJSON(envelope.CredentialRun)
	if err != nil {
		return trustedReconciliationAdmission{}, err
	}

	planBinding := map[string]interface{}{
		"template": plan.Template, "project": plan.Project, "environment": plan.Environment,
		"target": plan.Target, "cluster": plan.Cluster, "instance": plan.Instance,
		"workflowPath": envelope.WorkflowPath, "inputs": envelope.Inputs,
		"credentialRunDigest": credentialRunDigest,
	}
	planDigest, err := digestJSON(plan)
	if err != nil {
		return trustedReconciliationAdmission{}, err
	}
	planBinding["planDigest"] = planDigest
	planBindingDigest, err := digestJSON(planBinding)
	if err != nil {
		return trustedReconciliationAdmission{}, err
	}

	tools := []string{
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
	if envelope.ExecutionMode == "apply" {
		tools = append(tools,
			"mcp_doops_plan_SyncImageSetToTarget",
			"mcp_doops_plan_ReconcileHelmRelease",
		)
	}
	releaseScope, err := trustedDoopsPlanReleaseScope(root, envelope.WorkflowPath, envelope.Inputs)
	if err != nil {
		return trustedReconciliationAdmission{}, err
	}
	if releaseScope == nil {
		return trustedReconciliationAdmission{}, errors.New("trusted native reconciliation currently requires a v2 DeploymentTemplate")
	}
	capabilities := make(map[string]trustedReconciliationCapability, len(tools))
	for _, tool := range tools {
		key := strings.TrimPrefix(tool, "mcp_doops_plan_")
		scope := map[string]interface{}{
			"cluster": plan.Cluster, "instance": plan.Instance, "project": plan.Project,
			"environment": plan.Environment, "template": plan.Template, "tool": tool,
			"workspacePath": root, "workflowPath": envelope.WorkflowPath,
			"inputs": envelope.Inputs,
		}
		scope["releasePlan"] = releaseScope
		scopeDigest, err := digestJSON(scope)
		if err != nil {
			return trustedReconciliationAdmission{}, err
		}
		evidenceKind, evidenceSubject := trustedToolEvidence(key)
		capabilities[key] = trustedReconciliationCapability{
			Tool:            key,
			EvidenceKind:    evidenceKind,
			EvidenceSubject: evidenceSubject,
			CanonicalScope:  scope,
			ScopeDigest:     scopeDigest,
		}
	}

	context := trustedReconciliationContext{
		SchemaVersion:      trustedReconciliationContextSchema,
		OperationID:        trustedOperationID(envelope.RunID, workspaceCommit),
		ExecutionMode:      envelope.ExecutionMode,
		MutationAuthorized: envelope.ExecutionMode == "apply",
		PlanDigest:         planDigest,
		PlanBindingDigest:  planBindingDigest,
		Source:             source,
		Capabilities:       capabilities,
	}
	context.ContextDigest, err = context.calculatedDigest()
	if err != nil {
		return trustedReconciliationAdmission{}, err
	}
	return trustedReconciliationAdmission{AllowedTools: tools, Context: context}, nil
}

func decodeDoopsCICDInstruction(instruction string) (doopsCICDInstructionEnvelope, error) {
	var envelope doopsCICDInstructionEnvelope
	decoder := json.NewDecoder(strings.NewReader(instruction))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return doopsCICDInstructionEnvelope{}, fmt.Errorf("invalid reconciliation instruction")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return doopsCICDInstructionEnvelope{}, fmt.Errorf("invalid reconciliation instruction")
	}
	if envelope.Task != "execute-doops-cicd-workflow" || envelope.Skill != "$doops-cicd" ||
		(envelope.ExecutionMode != "dry-run" && envelope.ExecutionMode != "apply") ||
		strings.TrimSpace(envelope.RunID) == "" || strings.TrimSpace(envelope.WorkflowPath) == "" ||
		(envelope.ResultContract.APIVersion != "doops.sh/v2" &&
			envelope.ResultContract.APIVersion != "doops.sh/v3") ||
		envelope.ResultContract.Kind != "DeploymentRun" ||
		!envelope.ResultContract.RequireEvidence ||
		envelope.CredentialRun.APIVersion != "doops.sh/v2" ||
		envelope.CredentialRun.Kind != "CredentialRun" ||
		strings.TrimSpace(envelope.CredentialRun.ID) == "" ||
		envelope.CredentialRun.Materializations == nil ||
		envelope.CredentialRun.Mode != envelope.ExecutionMode {
		return doopsCICDInstructionEnvelope{}, fmt.Errorf("reconciliation instruction binding is invalid")
	}
	return envelope, nil
}

func selectTrustedSource(declared, registry string) string {
	if declared = strings.TrimSpace(declared); declared != "" {
		return declared
	}
	return strings.TrimSpace(registry)
}

func digestJSON(value interface{}) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var normalized interface{}
	if err := decoder.Decode(&normalized); err != nil {
		return "", err
	}
	var canonical bytes.Buffer
	if err := writeCanonicalJSON(&canonical, normalized); err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical.Bytes())
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func writeCanonicalJSON(output *bytes.Buffer, value interface{}) error {
	switch typed := value.(type) {
	case nil:
		output.WriteString("null")
	case bool:
		if typed {
			output.WriteString("true")
		} else {
			output.WriteString("false")
		}
	case string:
		if err := writeCanonicalJSONString(output, typed); err != nil {
			return err
		}
	case json.Number:
		number, err := canonicalJSONNumber(typed.String())
		if err != nil {
			return err
		}
		output.WriteString(number)
	case []interface{}:
		output.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeCanonicalJSON(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case map[string]interface{}:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			if !utf8.ValidString(key) {
				return fmt.Errorf("canonical JSON object key is not valid UTF-8")
			}
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			return compareUTF16(keys[i], keys[j]) < 0
		})
		output.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				output.WriteByte(',')
			}
			if err := writeCanonicalJSONString(output, key); err != nil {
				return err
			}
			output.WriteByte(':')
			if err := writeCanonicalJSON(output, typed[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON type %T", value)
	}
	return nil
}

func writeCanonicalJSONString(output *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("canonical JSON string is not valid UTF-8")
	}
	output.WriteByte('"')
	for _, char := range value {
		switch char {
		case '\b':
			output.WriteString(`\b`)
		case '\t':
			output.WriteString(`\t`)
		case '\n':
			output.WriteString(`\n`)
		case '\f':
			output.WriteString(`\f`)
		case '\r':
			output.WriteString(`\r`)
		case '"':
			output.WriteString(`\"`)
		case '\\':
			output.WriteString(`\\`)
		default:
			if char < 0x20 {
				_, _ = fmt.Fprintf(output, `\u%04x`, char)
			} else {
				output.WriteRune(char)
			}
		}
	}
	output.WriteByte('"')
	return nil
}

func canonicalJSONNumber(value string) (string, error) {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return "", fmt.Errorf("invalid canonical JSON number %q: %w", value, err)
	}
	if number == 0 {
		return "0", nil
	}
	data, err := json.Marshal(number)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func compareUTF16(left, right string) int {
	leftUnits := utf16.Encode([]rune(left))
	rightUnits := utf16.Encode([]rune(right))
	for index := 0; index < len(leftUnits) && index < len(rightUnits); index++ {
		if leftUnits[index] < rightUnits[index] {
			return -1
		}
		if leftUnits[index] > rightUnits[index] {
			return 1
		}
	}
	switch {
	case len(leftUnits) < len(rightUnits):
		return -1
	case len(leftUnits) > len(rightUnits):
		return 1
	default:
		return 0
	}
}

func validateTrustedWorkflowInputs(parameters map[string]credentialTemplateParameter, inputs map[string]string) error {
	for name, parameter := range parameters {
		value, ok := inputs[name]
		if parameter.Required != nil && *parameter.Required && (!ok || strings.TrimSpace(value) == "") {
			return fmt.Errorf("required workflow input %q is missing", name)
		}
	}
	for name := range inputs {
		if _, ok := parameters[name]; !ok {
			return fmt.Errorf("workflow input %q is not declared", name)
		}
	}
	return nil
}

func resolveTrustedRevision(declared string, inputs map[string]string) (string, error) {
	declared = strings.TrimSpace(declared)
	const prefix = "${inputs."
	if strings.HasPrefix(declared, prefix) && strings.HasSuffix(declared, "}") {
		name := strings.TrimSuffix(strings.TrimPrefix(declared, prefix), "}")
		value, ok := inputs[name]
		if !ok || strings.TrimSpace(value) != value || value == "" {
			return "", fmt.Errorf("workflow revision input %q is missing or invalid", name)
		}
		return value, nil
	}
	if strings.Contains(declared, "${") {
		return "", fmt.Errorf("workflow revision must be one exact input reference or immutable revision")
	}
	return declared, nil
}

func trustedToolEvidence(tool string) (string, string) {
	switch tool {
	case "ObserveWorkspaceSource":
		return "source-identity", "source-identity"
	case "ObserveSourceRegistryImageSet":
		return "image-set", "source-image-set"
	case "ObserveTargetRegistryImageSet":
		return "release-manifest", "release-manifest"
	case "RenderHelmRelease":
		return "gitops-render", "gitops-render"
	case "ObserveHelmRelease":
		return "runtime-state", "helm-release"
	case "ObserveKubernetesWorkload":
		return "runtime-state", "kubernetes-workload"
	case "ObserveAuthorizationState":
		return "authorization-state", "authorization-state"
	case "ObserveHTTPContract":
		return "public-contract", "public-contract"
	case "ObserveKubernetesLogs":
		return "post-deploy-log-scan", "post-deploy-log-scan"
	case "SyncImageSetToTarget":
		return "image-set", "target-image-set"
	case "ReconcileHelmRelease":
		return "runtime-state", "helm-release"
	default:
		return "", ""
	}
}

func trustedOperationID(runID, workspaceCommit string) string {
	sum := sha256.Sum256([]byte(runID + "\x00" + workspaceCommit))
	return "op_" + hex.EncodeToString(sum[:16])
}

func validLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validSHA256Digest(value string) bool {
	const prefix = "sha256:"
	return strings.HasPrefix(value, prefix) &&
		validLowerHex(strings.TrimPrefix(value, prefix), sha256.Size*2)
}
