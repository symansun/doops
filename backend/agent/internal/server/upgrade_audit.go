package server

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/user/doops/agent/api"
)

var upgradeAuditIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var upgradeAuditSessionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var upgradeAuditCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

const upgradeAuditProtocol = "upgradeAudit"

func upgradeAuditCapability() map[string]interface{} {
	return map[string]interface{}{"version": 1, "structuredResult": true}
}

func upgradeAuditCapabilityCompatible(value interface{}) bool {
	capability, ok := value.(map[string]interface{})
	version, valid := numericID(capability["version"])
	return ok && valid && version == 1 && capability["structuredResult"] == true
}

func upgradeAuditPhase(arguments json.RawMessage) string {
	var args struct {
		ObserveJob string `json:"observe_job"`
		Rollback   bool   `json:"rollback"`
		Preflight  bool   `json:"preflight"`
		DryRun     bool   `json:"dry_run"`
	}
	_ = json.Unmarshal(arguments, &args)
	switch {
	case args.ObserveJob != "":
		return "observe"
	case args.DryRun:
		return "dry_run"
	case args.Rollback:
		return "rollback"
	case args.Preflight:
		return "preflight"
	default:
		return "apply"
	}
}

func upgradeAuditMutation(arguments json.RawMessage) bool {
	phase := upgradeAuditPhase(arguments)
	return phase == "apply" || phase == "rollback"
}

// Attribution is set by the authenticated Gateway, never by the caller.
func prepareUpgradeAuditArguments(arguments json.RawMessage, auth TokenAuth, auditID int64) (json.RawMessage, error) {
	var args map[string]interface{}
	if err := json.Unmarshal(arguments, &args); err != nil || args == nil {
		return nil, fmt.Errorf("invalid agent upgrade arguments")
	}
	for _, name := range []string{"upgrade_operation_id", "session_id"} {
		value, exists := args[name]
		if !exists || value == "" {
			continue
		}
		text, ok := value.(string)
		valid := upgradeAuditIDPattern.MatchString(text)
		if name == "session_id" {
			valid = upgradeAuditSessionPattern.MatchString(text) && !strings.Contains(text, "..")
		}
		if !ok || !valid {
			return nil, fmt.Errorf("%s must be a bounded operation identifier", name)
		}
	}
	for _, name := range []string{"expected_release_commit", "source_commit"} {
		value, exists := args[name]
		if !exists || value == "" {
			continue
		}
		text, ok := value.(string)
		if !ok || !upgradeAuditCommitPattern.MatchString(text) {
			return nil, fmt.Errorf("%s must be a full lowercase commit SHA", name)
		}
	}
	if value, exists := args["ci_url"]; exists && value != "" {
		text, ok := value.(string)
		u, err := url.Parse(text)
		if !ok || err != nil || len(text) > 512 || u.Scheme != "https" || u.Host == "" ||
			u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("ci_url must be an HTTPS URL without credentials, query or fragment")
		}
	}
	for _, name := range []string{"image", "namespace", "workload", "release", "observe_job"} {
		value, exists := args[name]
		if !exists {
			continue
		}
		text, ok := value.(string)
		if !ok || len(text) > 512 || strings.IndexFunc(text, unicode.IsControl) >= 0 {
			return nil, fmt.Errorf("invalid %s metadata", name)
		}
	}
	if args["upgrade_operation_id"] == nil || args["upgrade_operation_id"] == "" {
		args["upgrade_operation_id"] = "upg-" + randomHex(16)
	}
	args["requester_id"] = auth.UserID
	args["audit_id"] = auditID
	return json.Marshal(args)
}

func (h *ClientService) auditUpgradeVerification(auth TokenAuth, cluster, instance string, req api.JSONRPCRequest, agent *GatewayAgent) *api.JSONRPCResponse {
	var args struct {
		OperationID      string `json:"upgrade_operation_id"`
		SessionID        string `json:"session_id"`
		ExpectedCommit   string `json:"expected_release_commit"`
		RequiredProtocol string `json:"required_protocol"`
	}
	if json.Unmarshal(req.Params, &args) != nil || args.OperationID == "" || args.ExpectedCommit == "" && args.RequiredProtocol == "" {
		return nil
	}
	failure := func(code int, message string) *api.JSONRPCResponse {
		response := buildErrorResponse(req.ID, code, message)
		return &response
	}
	if !upgradeAuditIDPattern.MatchString(args.OperationID) || !upgradeAuditSessionPattern.MatchString(args.SessionID) || strings.Contains(args.SessionID, "..") ||
		args.ExpectedCommit != "" && !upgradeAuditCommitPattern.MatchString(args.ExpectedCommit) ||
		args.RequiredProtocol != "" && args.RequiredProtocol != structuredSSHProtocol {
		return failure(-32602, "invalid upgrade verification metadata")
	}
	if err := h.store.validateUserTokenAuth(auth); err != nil {
		return failure(-32001, "upgrade_verification_unauthorized")
	}
	id, err := h.store.StartAudit(AuditEvent{
		UserID: auth.UserID, TokenID: auth.TokenID, Cluster: cluster, Instance: instance,
		Action: ActionAgentUpgrade, Session: args.SessionID, CorrelationID: args.OperationID,
		CommandSummary: "doops_agent_upgrade phase=verify upgrade_operation_id=" + args.OperationID + " expected_release_commit=" + args.ExpectedCommit + " required_protocol=" + args.RequiredProtocol,
	})
	if err != nil {
		return failure(-32603, "audit_unavailable")
	}
	status, category, actual, protocolReady := "success", "", "", false
	if agent != nil {
		actual, protocolReady = agent.ReleaseCommit, agent.StructuredSSH
	}
	switch {
	case !h.store.UserCanUseTarget(auth.UserID, cluster, instance):
		status, category = "forbidden", "upgrade_verification_forbidden"
	case agent == nil:
		status, category = "offline", "upgrade_verification_target_offline"
	case args.RequiredProtocol != "" && !protocolReady:
		status, category = "error", "upgrade_verification_protocol_missing"
	case args.ExpectedCommit != "" && actual != args.ExpectedCommit:
		status, category = "error", "upgrade_verification_commit_mismatch"
	}
	metadata := map[string]interface{}{
		"phase": "verify", "upgrade_operation_id": args.OperationID, "release_commit": actual,
		"expected_release_commit": args.ExpectedCommit, "required_protocol": args.RequiredProtocol,
		"structured_ssh": protocolReady, "outcome": status, "category": category,
	}
	evidence, _ := json.Marshal(metadata)
	verificationFailure := func(code int, message string) *api.JSONRPCResponse {
		response := failure(code, message)
		response.Error.Data = map[string]interface{}{
			"audit_id": id, "upgrade_operation_id": args.OperationID,
			"result": map[string]interface{}{"isError": category != "", "structuredContent": metadata},
		}
		return response
	}
	finish := AuditFinish{Status: status, Error: category, Tail: string(evidence), EndedAt: time.Now().UTC()}
	if err := h.store.FinishAudit(id, finish); err != nil {
		op := h.registerActiveOperation(GatewayActiveOperation{
			AuditID: id, UserID: auth.UserID, TokenID: auth.TokenID, Cluster: cluster, Instance: instance,
			Action: ActionAgentUpgrade, Session: args.SessionID, Kind: "rpc",
		}, func() {})
		_ = h.finishExecutionAudit(id, finish)
		h.finishActiveOperation(op)
		return verificationFailure(-32603, "audit_completion_failed")
	}
	if category != "" {
		return verificationFailure(-32003, category)
	}
	return nil
}

func summarizeUpgradeAudit(arguments json.RawMessage) string {
	var args map[string]interface{}
	_ = json.Unmarshal(arguments, &args)
	summary := "doops_agent_upgrade phase=" + upgradeAuditPhase(arguments)
	for _, key := range []string{"upgrade_operation_id", "image", "namespace", "release", "workload", "observe_job", "expected_release_commit"} {
		if value, ok := args[key].(string); ok && value != "" {
			summary += " " + key + "=" + sanitizeSecretText(value)
		}
	}
	if allowed, _ := args["allow_missing_rollback_baseline"].(bool); allowed {
		summary += " allow_missing_rollback_baseline=true"
	}
	return summary
}

// The fixed metadata projection prevents arbitrary result fields or caller
// extensions from entering the audit. JSON is bounded before encoding, so it
// remains readable as structured evidence after persistence.
func upgradeAuditMetadata(source map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for _, key := range []string{
		"upgrade_operation_id", "phase", "outcome", "category", "job", "observe_job",
		"namespace", "release", "workload", "image", "digest", "expected_release_commit",
		"release_commit", "source_commit", "ci_url", "requester_id", "session_id",
		"verified_at", "pod_uid", "node", "runtime_digest", "rollback_outcome",
		"receipt_name", "job_phase", "verified_image", "verification_stage", "receipt_error",
	} {
		if value, ok := source[key].(string); ok && value != "" {
			result[key] = utf8Tail(redactSecretText(value), 512)
		}
	}
	for _, key := range []string{"audit_id", "revision", "rollback_revision"} {
		if value, ok := numericID(source[key]); ok {
			result[key] = value
		}
	}
	if value, ok := source["allow_missing_rollback_baseline"].(bool); ok {
		result["allow_missing_rollback_baseline"] = value
	}
	return result
}

func upgradeAuditResult(result map[string]interface{}, arguments json.RawMessage) (status, errorText, evidence string) {
	structured, ok := result["structuredContent"].(map[string]interface{})
	isError, _ := result["isError"].(bool)
	outcome, _ := structured["outcome"].(string)
	switch outcome {
	case "accepted":
		status = "accepted"
	case "running":
		status = "observing"
	case "succeeded", "dry_run":
		status = "success"
	case "failed":
		status = "error"
	case "busy":
		status = "busy"
	case "unknown":
		status = "outcome_unknown"
	default:
		status, errorText = "error", "upgrade_result_invalid: structured upgrade outcome is required"
	}
	if !ok || isError && status != "busy" && status != "outcome_unknown" {
		status = "error"
		if isError {
			errorText = sanitizeSecretText(resultContentText(result))
		}
	}
	if operationID, _ := structured["upgrade_operation_id"].(string); ok &&
		operationID != extractStringArg(arguments, "upgrade_operation_id") {
		status, errorText = "error", "upgrade_result_invalid: operation identifier does not match"
	}
	if status == "error" || status == "busy" || status == "outcome_unknown" {
		if category, _ := structured["category"].(string); category != "" {
			errorText = sanitizeSecretText(category)
		}
		if errorText == "" {
			errorText = "upgrade_" + outcome
		}
		result["isError"] = true
	}
	var request map[string]interface{}
	_ = json.Unmarshal(arguments, &request)
	request["phase"] = upgradeAuditPhase(arguments)
	record := map[string]interface{}{
		"request": upgradeAuditMetadata(request),
		"result":  upgradeAuditMetadata(structured),
		"output":  utf8Tail(redactSecretText(resultContentText(result)), 4000),
	}
	encoded, _ := json.Marshal(record)
	if len(encoded) > 8192 {
		record["output"] = "[output omitted: structured evidence exceeds audit output budget]"
		encoded, _ = json.Marshal(record)
	}
	if len(encoded) > 8192 {
		// Reject oversized evidence instead of storing truncated, invalid JSON.
		status, errorText = "error", "upgrade_evidence_too_large"
		result["isError"] = true
		record = map[string]interface{}{"upgrade_operation_id": request["upgrade_operation_id"], "category": errorText}
		encoded, _ = json.Marshal(record)
	}
	return status, errorText, string(encoded)
}

func upgradeAuditCompletionError(id interface{}, auditID int64, arguments json.RawMessage, finalResponse []byte) api.JSONRPCResponse {
	response := buildErrorResponse(id, -32603, "audit_completion_failed")
	var terminal map[string]interface{}
	_ = json.Unmarshal(finalResponse, &terminal)
	response.Error.Data = map[string]interface{}{
		"audit_id":             auditID,
		"upgrade_operation_id": extractStringArg(arguments, "upgrade_operation_id"),
		"result":               redactDoagentValue(terminal["result"]),
	}
	return response
}
