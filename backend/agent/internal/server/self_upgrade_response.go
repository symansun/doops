package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
	"github.com/user/doops/agent/pkg/release"
)

var upgradeOperationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var upgradeCategoryPattern = regexp.MustCompile(`(?:^|\s)category=([a-z][a-z0-9_]+)(?:\s|$)`)

func agentUpgradePhase(args agentUpgradeParams) string {
	switch {
	case args.ObserveJob != "":
		return "observe"
	case args.Rollback:
		return "rollback"
	case args.DryRun:
		return "dry_run"
	case args.Preflight:
		return "preflight"
	default:
		return "apply"
	}
}

func validateUpgradeEvidenceParams(args agentUpgradeParams) error {
	if !args.DryRun && !upgradeOperationPattern.MatchString(args.UpgradeOperationID) {
		return fmt.Errorf("category=invalid_upgrade_request upgrade_operation_id is required (1-128 identifier characters)")
	}
	if !args.DryRun && !args.Rollback && args.ObserveJob == "" && !release.ValidCommit(args.ExpectedReleaseCommit) {
		return fmt.Errorf("category=release_commit_unknown expected_release_commit must be a full release commit")
	}
	if args.SourceCommit != "" && !release.ValidCommit(args.SourceCommit) {
		return fmt.Errorf("category=invalid_upgrade_request source_commit must be a full commit")
	}
	if args.CIURL != "" {
		parsed, err := url.Parse(args.CIURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("category=invalid_upgrade_request ci_url must be HTTPS without credentials, query or fragment")
		}
	}
	for _, value := range []string{args.RequesterID, args.SessionID, args.CIURL} {
		if len(value) > 1024 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("category=invalid_upgrade_request invalid evidence metadata")
		}
	}
	if args.AuditID < 0 {
		return fmt.Errorf("category=invalid_upgrade_request audit_id cannot be negative")
	}
	return nil
}

// The string API remains available to existing callers. The websocket handler
// uses this adapter so terminal Job failures cannot become successful tool calls.
func buildAgentUpgradeResponse(reqID interface{}, argBytes json.RawMessage) api.JSONRPCResponse {
	return buildAgentUpgradeResponseWithProcess(reqID, argBytes, currentUpgradeProcess())
}

func buildAgentUpgradeResponseWithProcess(reqID interface{}, argBytes json.RawMessage, proc upgradeProcess) api.JSONRPCResponse {
	var args agentUpgradeParams
	err := json.Unmarshal(argBytes, &args)
	text := ""
	if err == nil {
		text, err = upgradeAgent(context.Background(), args, proc)
	} else {
		err = fmt.Errorf("category=invalid_upgrade_request invalid agent upgrade params")
	}
	fields := map[string]interface{}{
		"phase": agentUpgradePhase(args), "outcome": "failed",
		"audit_id": args.AuditID, "time": time.Now().UTC().Format(time.RFC3339Nano),
	}
	for key, value := range map[string]string{
		"image": args.Image, "job": args.ObserveJob, "namespace": firstNonEmptyString(args.Namespace, proc.namespace),
		"release": args.Release, "expected_release_commit": args.ExpectedReleaseCommit,
		"upgrade_operation_id": args.UpgradeOperationID,
		"requester_id":         args.RequesterID, "session_id": args.SessionID, "source_commit": args.SourceCommit, "ci_url": args.CIURL,
	} {
		if value != "" {
			fields[key] = value
		}
	}
	if _, _, digest, parseErr := parseUpgradeImage(args.Image); parseErr == nil && digest != "" {
		fields["digest"] = digest
	}
	if err != nil {
		text = err.Error()
		fields["category"] = firstNonEmptyString(selfUpgradeFailureCategory(text), "upgrade_"+agentUpgradePhase(args)+"_failed")
		// A create transport failure may have happened after Kubernetes stored
		// the Job. Keep its deterministic name so the caller can observe it.
		if fields["category"] == "upgrade_job_create_unknown" || fields["category"] == "upgrade_job_exists" {
			fields["job"] = selfUpgradeJobName(args.UpgradeOperationID, agentUpgradePhase(args))
		}
		if fields["category"] == "upgrade_job_create_unknown" {
			fields["outcome"] = "unknown"
		}
		if strings.HasPrefix(fmt.Sprint(fields["category"]), "upgrade_receipt_") || fields["category"] == "upgrade_job_not_found" {
			fields["outcome"] = "unknown"
		}
	} else {
		// Only the header is part of the string protocol; never parse Job logs
		// as response metadata (they can contain prior verification attempts).
		header, evidence, _ := strings.Cut(text, "\n")
		tokens := strings.Fields(header)
		if len(tokens) > 0 {
			fields["outcome"] = strings.TrimPrefix(tokens[0], "self-upgrade-")
			if tokens[0] == "dry-run:" {
				fields["outcome"] = "dry_run"
			}
			tokens = tokens[1:]
		}
		for _, token := range tokens {
			key, value, ok := strings.Cut(token, "=")
			if !ok {
				continue
			}
			switch key {
			case "phase", "job_phase", "category", "job", "namespace", "release", "workload", "image", "digest",
				"expected_release_commit", "upgrade_operation_id":
				fields[key] = value
			}
		}
		if args.ObserveJob != "" && evidence != "" {
			var verified map[string]interface{}
			if json.Unmarshal([]byte(evidence), &verified) == nil {
				for key, value := range verified {
					fields[key] = value
				}
			}
		}
	}
	if fields["category"] == "upgrade_busy" {
		fields["outcome"] = "busy"
	}
	outcome := fields["outcome"]
	isError := err != nil || (outcome != "accepted" && outcome != "running" && outcome != "succeeded" && outcome != "dry_run")
	if isError && fields["category"] == nil {
		fields["category"] = "upgrade_job_failed"
	}
	// Invalid caller metadata must not be reflected into process logs or audit.
	if validateUpgradeEvidenceParams(args) != nil {
		fields = map[string]interface{}{"phase": agentUpgradePhase(args), "outcome": "failed", "category": fields["category"]}
	}
	text = redactSecretText(text)
	fields = redactDoagentValue(fields).(map[string]interface{})
	response := buildStructuredSuccessResponse(reqID, text, fields)
	response.Result.(map[string]interface{})["isError"] = isError
	encoded, _ := json.Marshal(fields)
	log.Printf("agent_upgrade %s", encoded)
	return response
}

type selfUpgradeVerification struct {
	OperationID    string `json:"upgrade_operation_id"`
	Job            string `json:"job"`
	Namespace      string `json:"namespace"`
	Release        string `json:"release"`
	Workload       string `json:"workload"`
	Image          string `json:"image"`
	ReleaseCommit  string `json:"release_commit"`
	Outcome        string `json:"outcome"`
	Revision       int    `json:"revision"`
	VerifiedAt     string `json:"verified_at"`
	PodUID         string `json:"podUID"`
	Node           string `json:"node"`
	RuntimeDigest  string `json:"runtimeDigest"`
	Stage          string `json:"verification_stage"`
	ExpectedCommit string `json:"expected_release_commit"`
}

type selfUpgradeReceipt struct {
	SchemaVersion   int                      `json:"schemaVersion"`
	OperationID     string                   `json:"upgrade_operation_id"`
	Job             string                   `json:"job"`
	Phase           string                   `json:"phase"`
	Namespace       string                   `json:"namespace"`
	Release         string                   `json:"release"`
	Workload        string                   `json:"workload"`
	Image           string                   `json:"image"`
	ExpectedCommit  string                   `json:"expected_release_commit"`
	Outcome         string                   `json:"outcome"`
	Category        string                   `json:"category"`
	RollbackOutcome string                   `json:"rollback_outcome"`
	RequesterID     string                   `json:"requester_id"`
	SessionID       string                   `json:"session_id"`
	AuditID         string                   `json:"audit_id"`
	SourceCommit    string                   `json:"source_commit"`
	CIURL           string                   `json:"ci_url"`
	Verification    *selfUpgradeVerification `json:"verification"`
}

func (p upgradeProcess) readUpgradeReceipt(ctx context.Context, namespace, jobName, operationID string) (map[string]interface{}, error) {
	name := strings.Replace(jobName, "doops-self-upgrade-", "doops-upgrade-receipt-", 1)
	out, err := p.command(ctx, "", "kubectl", "-n", namespace, "get", "configmap", name, "--ignore-not-found", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("category=upgrade_receipt_unavailable operation receipt could not be read")
	}
	if strings.TrimSpace(string(out)) == "" {
		return nil, fmt.Errorf("category=upgrade_receipt_missing operation receipt is missing")
	}
	var stored struct {
		Data map[string]string `json:"data"`
	}
	var receipt selfUpgradeReceipt
	if len(out) > 256*1024 || json.Unmarshal(out, &stored) != nil ||
		json.Unmarshal([]byte(stored.Data["evidence.json"]), &receipt) != nil ||
		receipt.SchemaVersion != 1 || receipt.OperationID != operationID || receipt.Job != jobName ||
		(receipt.Phase != "apply" && receipt.Phase != "preflight" && receipt.Phase != "rollback") ||
		selfUpgradeJobName(operationID, receipt.Phase) != jobName || receipt.Namespace != namespace ||
		(receipt.Outcome != "succeeded" && receipt.Outcome != "failed") ||
		(receipt.Outcome == "failed" && receipt.Category == "") ||
		(receipt.Outcome == "succeeded" && (receipt.Category != "" || receipt.RollbackOutcome != "")) ||
		(receipt.Category != "" && selfUpgradeFailureCategory("category="+receipt.Category) != receipt.Category) ||
		(receipt.RollbackOutcome != "" && receipt.RollbackOutcome != "succeeded" && receipt.RollbackOutcome != "failed") {
		return nil, fmt.Errorf("category=upgrade_receipt_invalid operation receipt scope or outcome is invalid")
	}
	if validateK8sName("release", receipt.Release, 63) != nil || validateK8sName("workload", receipt.Workload, 63) != nil {
		return nil, fmt.Errorf("category=upgrade_receipt_invalid operation receipt workload is invalid")
	}
	_, _, digest, err := parseUpgradeImage(receipt.Image)
	if err != nil || !release.ValidCommit(receipt.ExpectedCommit) {
		return nil, fmt.Errorf("category=upgrade_receipt_invalid operation receipt image or commit is invalid")
	}
	fields := map[string]interface{}{
		"outcome": receipt.Outcome, "job_phase": receipt.Phase, "release": receipt.Release,
		"workload": receipt.Workload, "image": receipt.Image, "digest": digest,
		"expected_release_commit": receipt.ExpectedCommit, "receipt_name": name,
	}
	auditID, auditErr := strconv.ParseInt(receipt.AuditID, 10, 64)
	if auditErr != nil || validateUpgradeEvidenceParams(agentUpgradeParams{
		UpgradeOperationID: operationID, ExpectedReleaseCommit: receipt.ExpectedCommit,
		RequesterID: receipt.RequesterID, SessionID: receipt.SessionID, AuditID: auditID,
		SourceCommit: receipt.SourceCommit, CIURL: receipt.CIURL,
	}) != nil {
		return nil, fmt.Errorf("category=upgrade_receipt_invalid operation attribution is invalid")
	}
	fields["requester_id"], fields["session_id"], fields["audit_id"] = receipt.RequesterID, receipt.SessionID, auditID
	fields["source_commit"], fields["ci_url"] = receipt.SourceCommit, receipt.CIURL
	if receipt.Category != "" {
		fields["category"] = receipt.Category
	}
	if receipt.RollbackOutcome != "" {
		fields["rollback_outcome"] = receipt.RollbackOutcome
	}
	v := receipt.Verification
	required := receipt.Outcome == "succeeded" && receipt.Phase != "preflight" || receipt.RollbackOutcome == "succeeded"
	if required && v == nil {
		return nil, fmt.Errorf("category=upgrade_receipt_invalid runtime verification is missing")
	}
	if v != nil {
		if v.OperationID != operationID || v.Job != jobName || v.Namespace != namespace ||
			v.Release != receipt.Release || v.Workload != receipt.Workload || v.Outcome != "succeeded" ||
			!release.ValidCommit(v.ReleaseCommit) || !imageDigestPattern.MatchString(v.RuntimeDigest) ||
			validateK8sName("pod UID", v.PodUID, 128) != nil || validateK8sName("node", v.Node, 253) != nil {
			return nil, fmt.Errorf("category=upgrade_receipt_invalid runtime verification identity is invalid")
		}
		if _, err := time.Parse(time.RFC3339Nano, v.VerifiedAt); err != nil {
			return nil, fmt.Errorf("category=upgrade_receipt_invalid verification timestamp is invalid")
		}
		if receipt.Outcome == "succeeded" && (v.Image != receipt.Image || v.ReleaseCommit != receipt.ExpectedCommit) {
			return nil, fmt.Errorf("category=upgrade_receipt_invalid verified release differs from the requested release")
		}
		if receipt.RollbackOutcome == "succeeded" && v.Stage != "rollback" {
			return nil, fmt.Errorf("category=upgrade_receipt_invalid restored release verification is missing")
		}
		if receipt.RollbackOutcome == "succeeded" && v.ReleaseCommit != v.ExpectedCommit {
			return nil, fmt.Errorf("category=upgrade_receipt_invalid restored commit differs from the verified baseline")
		}
		if _, _, _, err := parseUpgradeImage(v.Image); err != nil || v.Revision < 0 ||
			(receipt.Outcome == "succeeded" && receipt.Phase != "preflight" && v.Revision == 0) {
			return nil, fmt.Errorf("category=upgrade_receipt_invalid verified image or revision is invalid")
		}
		if required {
			fields["release_commit"], fields["revision"], fields["verified_at"] = v.ReleaseCommit, v.Revision, v.VerifiedAt
			fields["pod_uid"], fields["node"], fields["runtime_digest"] = v.PodUID, v.Node, v.RuntimeDigest
			fields["verified_image"], fields["verification_stage"] = v.Image, v.Stage
			if receipt.RollbackOutcome == "succeeded" {
				fields["rollback_revision"] = v.Revision
			}
		}
	}
	return fields, nil
}
