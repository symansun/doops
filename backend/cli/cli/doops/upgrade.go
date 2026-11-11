package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type UpgradeOptions struct {
	Image        string
	Mode         string
	Namespace    string
	Workload     string
	Container    string
	Release      string
	DryRun       bool
	Rollback     bool
	Session      string
	SourceCommit string
	CIURL        string
	// AllowMissingRollbackBaseline lets the operator upgrade an Agent whose
	// rollback baseline image is gone from the registry. The default is fail
	// closed: without that image an automatic rollback could not restore the
	// backup, so the third upgrade guarantee could not hold.
	AllowMissingRollbackBaseline bool
}

var (
	upgradePollInterval    = 5 * time.Second
	upgradeWait            = 21 * time.Minute
	cliImageRepository     = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]{1,5})?(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+$`)
	cliImageTag            = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	cliImageDigest         = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	releaseCommitPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	upgradeSessionPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	upgradeMetadataPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,252}$`)
)

type upgradeOperation struct {
	server          Server
	token           string
	session         string
	verbose         bool
	id              string
	expectedRelease string
	sourceCommit    string
	ciURL           string
	output          strings.Builder
	auditErr        error
}

func (op *upgradeOperation) printf(format string, args ...interface{}) {
	text := redactUpgradeText(fmt.Sprintf(format, args...), op.token)
	fmt.Print(text)
	op.output.WriteString(text)
}

func (op *upgradeOperation) client() *MCPClient {
	client := NewMCPClient(op.server, NewSessionStore(), op.session, op.verbose)
	client.Token = op.token
	client.UpgradeOperationID = op.id
	return client
}

func UpgradeAgents(base Server, opts UpgradeOptions, verbose bool) error {
	opts.Image = strings.TrimSpace(opts.Image)
	opts.Session = strings.TrimSpace(opts.Session)
	if err := validateUpgradeMetadata(opts); err != nil {
		return err
	}
	if opts.Rollback {
		if strings.TrimSpace(opts.Image) != "" {
			return fmt.Errorf("rollback does not take an image")
		}
		if opts.DryRun {
			return fmt.Errorf("rollback cannot be combined with --dry-run")
		}
	} else {
		if strings.TrimSpace(opts.Image) == "" {
			return fmt.Errorf("image is required")
		}
		if err := validateUpgradeImage(opts.Image); err != nil {
			return err
		}
	}
	if strings.TrimSpace(base.Gateway) == "" ||
		strings.TrimSpace(base.Cluster) == "" ||
		strings.TrimSpace(base.Instance) == "" {
		return fmt.Errorf("configured Gateway target is required")
	}
	session := resolveSession(opts.Session)
	expectedRelease := ""
	if !opts.DryRun && !opts.Rollback {
		expectedRelease = strings.TrimSpace(cliBuildCommit)
		if !releaseCommitPattern.MatchString(expectedRelease) {
			return fmt.Errorf("published CLI release commit is required for Agent upgrade")
		}
	}
	token, err := ResolveToken(base)
	if err != nil {
		return err
	}
	targets, err := fetchGatewayTargets(base.Gateway, token)
	if err != nil {
		return err
	}
	selected := filterUpgradeTargets(targets, base.Cluster, base.Instance)
	if len(selected) == 0 {
		return fmt.Errorf("configured target is offline: %s/%s", base.Cluster, base.Instance)
	}
	var failures []error
	for _, target := range selected {
		server := base
		server.Name = target.Key
		server.Gateway = base.Gateway
		server.Cluster = target.Cluster
		server.Instance = target.Instance
		var operationID [16]byte
		if _, err := rand.Read(operationID[:]); err != nil {
			return fmt.Errorf("create upgrade operation id: %w", err)
		}
		op := &upgradeOperation{
			server: server, token: token, session: session, verbose: verbose,
			id: fmt.Sprintf("upgrade_%x", operationID), expectedRelease: expectedRelease,
			sourceCommit: opts.SourceCommit, ciURL: opts.CIURL,
		}
		action := "upgrade"
		if opts.Rollback {
			action = "rollback"
		}
		op.printf("==> %s %s/%s operation=%s session=%s image=%s\n", action, target.Cluster, target.Instance, op.id, session, opts.Image)
		runErr := op.run(opts)
		resultErr := errors.Join(runErr, op.auditErr)
		if resultErr != nil {
			resultErr = errors.New(redactUpgradeText(resultErr.Error(), op.token))
		}
		status := "success"
		if resultErr != nil {
			status = "error"
			op.printf("FAIL %s/%s operation=%s session=%s: %v\n", target.Cluster, target.Instance, op.id, session, resultErr)
		}
		result := HistoryResult{UpgradeOperationID: op.id, Status: status, Output: op.output.String()}
		if resultErr != nil {
			result.Error = resultErr.Error()
		}
		if err := RecordHistory(target.Cluster+"/"+target.Instance, session, action, result); err != nil {
			op.printf("FAIL operation=%s session=%s: could not record upgrade history: %v\n", op.id, session, err)
			resultErr = errors.Join(resultErr, fmt.Errorf("record upgrade history: %w", err))
		}
		if resultErr != nil {
			failures = append(failures, fmt.Errorf("%s/%s operation=%s: %w", target.Cluster, target.Instance, op.id, resultErr))
			continue
		}
		if opts.DryRun {
			fmt.Printf("PREVIEW %s/%s operation=%s session=%s\n", target.Cluster, target.Instance, op.id, session)
		} else {
			fmt.Printf("OK %s/%s operation=%s session=%s: %s Job, structuredSSH protocol and release_commit verified\n", target.Cluster, target.Instance, op.id, session, action)
			fmt.Println(withNext("verify the release", "doops targets --target "+base.Name+"   # compare RELEASE_COMMIT with the verified commit", "doops capabilities --target "+base.Name+" --require structuredSSH"))
		}
	}
	return errors.Join(failures...)
}

func (op *upgradeOperation) run(opts UpgradeOptions) error {
	if !opts.DryRun && !opts.Rollback {
		if err := op.runUpgradePreflight(opts); err != nil {
			return fmt.Errorf("preflight failed, nothing was changed: %w", err)
		}
		// Finish observing the preflight, but do not start a new mutation when
		// its audit could not be persisted.
		if op.auditErr != nil {
			return fmt.Errorf("preflight completed but its audit failed; upgrade was not started")
		}
	}
	out, err := op.callUpgradeTool(upgradeArguments(opts))
	if err != nil {
		return err
	}
	out = strings.TrimSpace(out)
	if opts.DryRun {
		if !strings.HasPrefix(out, "dry-run: ") {
			return fmt.Errorf("invalid dry-run result: %s", out)
		}
		op.printf("%s\n", out)
		return nil
	}
	if _, _, _, err := parseUpgradeAcceptance(out); err != nil {
		return err
	}
	op.printf("ACCEPTED %s/%s: %s\n", op.server.Cluster, op.server.Instance, out)
	return op.waitForSelfUpgrade(out, !opts.Rollback, op.expectedRelease)
}

func upgradeArguments(opts UpgradeOptions) map[string]interface{} {
	if opts.Rollback {
		args := map[string]interface{}{
			"rollback":  true,
			"mode":      opts.Mode,
			"namespace": opts.Namespace,
			"workload":  opts.Workload,
			"release":   opts.Release,
		}
		return args
	}
	return map[string]interface{}{
		"image":                           opts.Image,
		"mode":                            opts.Mode,
		"namespace":                       opts.Namespace,
		"workload":                        opts.Workload,
		"container":                       opts.Container,
		"release":                         opts.Release,
		"dry_run":                         opts.DryRun,
		"allow_missing_rollback_baseline": opts.AllowMissingRollbackBaseline,
	}
}

// runUpgradePreflight pulls the target image on the Agent's node and runs every
// pre-change check without modifying the release. It returns only after the Job
// reached a terminal state.
func (op *upgradeOperation) runUpgradePreflight(opts UpgradeOptions) error {
	args := upgradeArguments(opts)
	args["preflight"] = true
	op.printf("==> preflight: pulling %s and checking the release\n", opts.Image)
	out, err := op.callUpgradeTool(args)
	if err != nil {
		return err
	}
	job, namespace, _, err := parseUpgradeAcceptance(strings.TrimSpace(out))
	if err != nil {
		return err
	}
	deadline := time.Now().Add(upgradeWait)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("preflight job %s did not finish (the image may not be pullable)", job)
		}
		observed, err := op.callUpgradeTool(map[string]interface{}{
			"observe_job":             job,
			"namespace":               namespace,
			"expected_release_commit": op.expectedRelease,
		})
		if err == nil || strings.HasPrefix(observed, "self-upgrade-failed ") {
			_, fields, parseErr := parseUpgradeResponse(observed)
			if parseErr != nil {
				return parseErr
			}
			if fields["job"] != job || fields["namespace"] != namespace {
				return fmt.Errorf("preflight observation does not match job %s in namespace %s", job, namespace)
			}
		}
		switch classifyUpgradeObservation(observed, err) {
		case "retry", "running":
			time.Sleep(upgradePollInterval)
			continue
		case "failed":
			return fmt.Errorf("%s", strings.TrimSpace(observed))
		case "succeeded":
			op.printf("preflight passed for %s\n", opts.Image)
			return nil
		default:
			if err != nil {
				return err
			}
			return fmt.Errorf("unrecognized preflight status: %s", strings.TrimSpace(observed))
		}
	}
}

func (op *upgradeOperation) callUpgradeTool(args map[string]interface{}) (string, error) {
	args["upgrade_operation_id"] = op.id
	if op.expectedRelease != "" && args["rollback"] != true && args["observe_job"] == nil {
		args["expected_release_commit"] = op.expectedRelease
	}
	if op.sourceCommit != "" {
		args["source_commit"] = op.sourceCommit
	}
	if op.ciURL != "" {
		args["ci_url"] = op.ciURL
	}
	client := op.client()
	defer client.Close()
	out, result, err := client.callAndCaptureResult("doops_agent_upgrade", args)
	step := map[string]interface{}{"arguments": args, "result": result, "output": out}
	if err != nil {
		step["error"] = err.Error()
	}
	var rpcErr *gatewayRPCError
	if errors.As(err, &rpcErr) {
		step["rpc_error"] = map[string]interface{}{"Code": rpcErr.Code, "Message": rpcErr.Message, "Data": rpcErr.Data}
	}
	data, marshalErr := json.Marshal(redactUpgradeValue(step, op.token))
	if marshalErr != nil {
		return out, errors.Join(err, fmt.Errorf("record upgrade result: %w", marshalErr))
	}
	op.output.Write(data)
	op.output.WriteByte('\n')
	if rpcErr != nil && rpcErr.Code == -32603 && rpcErr.Message == "audit_completion_failed" {
		auditID, validAuditID := rpcErr.Data["audit_id"].(float64)
		operationID, _ := rpcErr.Data["upgrade_operation_id"].(string)
		auditErr := fmt.Errorf("audit_completion_failed audit_id=%.0f operation=%s: %w", auditID, operationID, rpcErr)
		if op.auditErr == nil {
			op.auditErr = auditErr
		}
		op.printf("WARNING: %v; operation remains unsuccessful; verifying returned Job evidence\n", auditErr)
		if !validAuditID || auditID <= 0 || operationID != op.id || result == nil || strings.TrimSpace(out) == "" {
			return out, fmt.Errorf("audit_completion_failed did not include matching operation and result evidence: %w", rpcErr)
		}
		// Only this explicit Gateway error carries a completed execution result.
		// Retain the audit failure while allowing its Job to reach verification.
		if toolResultIsError(result) {
			return out, fmt.Errorf("%s", toolResultText(result, "tool returned an error"))
		}
		return out, nil
	}
	return out, err
}

func (op *upgradeOperation) waitForSelfUpgrade(acceptance string, rollbackOnProbeFailure bool, expectedRelease string) error {
	job, namespace, release, err := parseUpgradeAcceptance(acceptance)
	if err != nil {
		return err
	}
	_, acceptanceFields, _ := parseUpgradeResponse(acceptance)
	if !rollbackOnProbeFailure {
		expectedRelease = acceptanceFields["expected_release_commit"]
	}
	// Even a malformed rollback receipt may describe an already running Job.
	// Observe it to completion before rejecting missing verification evidence.
	op.printf("==> waiting for self-upgrade job %s\n", job)
	deadline := time.Now().Add(upgradeWait)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("self-upgrade job %s did not finish", job)
		}
		args := map[string]interface{}{
			"observe_job": job,
			"namespace":   namespace,
		}
		if expectedRelease != "" {
			args["expected_release_commit"] = expectedRelease
		}
		out, err := op.callUpgradeTool(args)
		if err == nil || strings.HasPrefix(out, "self-upgrade-failed ") {
			_, fields, parseErr := parseUpgradeResponse(out)
			if parseErr != nil {
				return parseErr
			}
			if fields["job"] != job || fields["namespace"] != namespace {
				return fmt.Errorf("self-upgrade observation does not match job %s in namespace %s", job, namespace)
			}
		}
		switch classifyUpgradeObservation(out, err) {
		case "retry", "running":
			time.Sleep(upgradePollInterval)
			continue
		case "failed":
			return fmt.Errorf("%s", strings.TrimSpace(out))
		case "succeeded":
			op.printf("%s\n", strings.TrimSpace(out))
			if !releaseCommitPattern.MatchString(expectedRelease) {
				return fmt.Errorf("rollback acceptance is missing a valid expected_release_commit")
			}
			if err := op.probeStructuredSSH(); err != nil {
				if !rollbackOnProbeFailure {
					return fmt.Errorf("rollback Job completed but Gateway protocol verification failed: %w", err)
				}
				return op.rollbackAfterProbeFailure(namespace, release, err)
			}
			op.printf("protocol=structuredSSH status=ready\n")
			if err := op.probeAgentRelease(expectedRelease); err != nil {
				if !rollbackOnProbeFailure {
					return fmt.Errorf("rollback Job completed but Agent release verification failed: %w", err)
				}
				return op.rollbackAfterProbeFailure(namespace, release, err)
			}
			op.printf("release_commit=%s status=ready\n", expectedRelease)
			return nil
		default:
			if err != nil {
				return err
			}
			return fmt.Errorf("unrecognized self-upgrade status: %s", strings.TrimSpace(out))
		}
	}
}

func (op *upgradeOperation) rollbackAfterProbeFailure(namespace, release string, probeErr error) error {
	var verificationErr *upgradeVerificationError
	if !errors.As(probeErr, &verificationErr) ||
		verificationErr.category != "upgrade_verification_protocol_missing" && verificationErr.category != "upgrade_verification_commit_mismatch" {
		return fmt.Errorf("upgrade verification inconclusive; no rollback started: %w", probeErr)
	}
	op.printf("verification probe failed: %v\n==> starting rollback job\n", probeErr)
	out, err := op.callUpgradeTool(map[string]interface{}{
		"rollback":  true,
		"namespace": namespace,
		"release":   release,
	})
	if err != nil {
		return fmt.Errorf("verification probe failed: %v; rollback was not accepted: %w", probeErr, err)
	}
	op.printf("%s\n", strings.TrimSpace(out))
	if err := op.waitForSelfUpgrade(out, false, ""); err != nil {
		return fmt.Errorf("verification probe failed: %v; rollback failed: %w", probeErr, err)
	}
	return fmt.Errorf("verification probe failed and the release was rolled back: %v", probeErr)
}

func (op *upgradeOperation) probeAgentRelease(expected string) error {
	client := op.client()
	defer client.Close()
	err := client.RequireAgentRelease(expected)
	op.printf("release probe expected_release_commit=%s actual=%v error=%v\n", expected, client.capabilities["releaseCommit"], err)
	return op.verificationResult(err, expected, "")
}

func (op *upgradeOperation) probeStructuredSSH() error {
	client := op.client()
	defer client.Close()
	err := client.RequireProtocol("structuredSSH")
	op.printf("protocol probe structuredSSH error=%v\n", err)
	return op.verificationResult(err, "", "structuredSSH")
}

type upgradeVerificationError struct {
	error
	category string
}

func (e *upgradeVerificationError) Unwrap() error { return e.error }

// An initialize audit failure is not a failed target probe. Only a correlated,
// internally consistent verification receipt can establish either outcome.
func (op *upgradeOperation) verificationResult(err error, expectedRelease, requiredProtocol string) error {
	var verificationErr *upgradeVerificationError
	if err == nil || errors.As(err, &verificationErr) {
		return err
	}
	var rpcErr *gatewayRPCError
	if !errors.As(err, &rpcErr) {
		return &upgradeVerificationError{error: err}
	}
	auditFailed := rpcErr.Code == -32603 && rpcErr.Message == "audit_completion_failed"
	if !auditFailed && rpcErr.Code != -32003 {
		return &upgradeVerificationError{error: err}
	}
	if auditFailed {
		auditErr := fmt.Errorf("audit_completion_failed audit_id=%v operation=%s: %w", rpcErr.Data["audit_id"], op.id, err)
		if op.auditErr == nil {
			op.auditErr = auditErr
		}
		op.printf("WARNING: %v; operation remains unsuccessful; checking returned verification evidence\n", auditErr)
	}
	data, marshalErr := json.Marshal(redactUpgradeValue(map[string]interface{}{
		"expected_release_commit": expectedRelease, "required_protocol": requiredProtocol,
		"rpc_error": map[string]interface{}{"Code": rpcErr.Code, "Message": rpcErr.Message, "Data": rpcErr.Data},
	}, op.token))
	if marshalErr != nil {
		return &upgradeVerificationError{error: errors.Join(err, fmt.Errorf("record verification result: %w", marshalErr))}
	}
	op.output.Write(data)
	op.output.WriteByte('\n')
	inconclusive := &upgradeVerificationError{error: fmt.Errorf("missing or malformed matching verification evidence: %w", err)}
	auditID, validAuditID := rpcErr.Data["audit_id"].(float64)
	result, _ := rpcErr.Data["result"].(map[string]interface{})
	fields, _ := result["structuredContent"].(map[string]interface{})
	isError, validIsError := result["isError"].(bool)
	actual, validActual := fields["release_commit"].(string)
	protocolReady, validProtocol := fields["structured_ssh"].(bool)
	category, validCategory := fields["category"].(string)
	if !validAuditID || auditID <= 0 || math.Trunc(auditID) != auditID ||
		rpcErr.Data["upgrade_operation_id"] != op.id || fields["upgrade_operation_id"] != op.id ||
		fields["phase"] != "verify" || fields["expected_release_commit"] != expectedRelease ||
		fields["required_protocol"] != requiredProtocol || !validIsError || !validActual || !validProtocol || !validCategory ||
		isError != (category != "") || !auditFailed && rpcErr.Message != category {
		return inconclusive
	}
	if category == "" {
		if fields["outcome"] != "success" || requiredProtocol != "" && !protocolReady || expectedRelease != "" && actual != expectedRelease {
			return inconclusive
		}
		return nil
	}
	validFailure := false
	switch category {
	case "upgrade_verification_protocol_missing":
		validFailure = fields["outcome"] == "error" && requiredProtocol != "" && !protocolReady
	case "upgrade_verification_commit_mismatch":
		validFailure = fields["outcome"] == "error" && expectedRelease != "" && actual != expectedRelease
	case "upgrade_verification_target_offline":
		validFailure = fields["outcome"] == "offline" && actual == "" && !protocolReady
	case "upgrade_verification_forbidden":
		validFailure = fields["outcome"] == "forbidden"
	}
	if !validFailure {
		return inconclusive
	}
	return &upgradeVerificationError{error: fmt.Errorf("%s: %w", category, err), category: category}
}

func parseUpgradeAcceptance(out string) (string, string, string, error) {
	status, fields, err := parseUpgradeResponse(out)
	if err != nil {
		return "", "", "", err
	}
	if status != "self-upgrade-accepted" || fields["job"] == "" || fields["namespace"] == "" || fields["release"] == "" {
		return "", "", "", fmt.Errorf("self-upgrade acceptance was not returned")
	}
	return fields["job"], fields["namespace"], fields["release"], nil
}

func parseUpgradeResponse(out string) (string, map[string]string, error) {
	line := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", nil, fmt.Errorf("invalid self-upgrade response header: %s", line)
	}
	fields := map[string]string{}
	for _, field := range parts[1:] {
		key, value, ok := strings.Cut(field, "=")
		_, duplicate := fields[key]
		if !ok || key == "" || duplicate {
			return "", nil, fmt.Errorf("invalid self-upgrade response header: %s", line)
		}
		fields[key] = value
	}
	return parts[0], fields, nil
}

func classifyUpgradeObservation(out string, err error) string {
	status, _, parseErr := parseUpgradeResponse(out)
	if parseErr == nil && status == "self-upgrade-failed" {
		return "failed"
	}
	if err != nil {
		if upgradeRetryable(err) {
			return "retry"
		}
		return "error"
	}
	if parseErr != nil {
		return "error"
	}
	switch status {
	case "self-upgrade-succeeded":
		return "succeeded"
	case "self-upgrade-failed":
		return "failed"
	case "self-upgrade-running":
		return "running"
	default:
		return "error"
	}
}

func upgradeRetryable(err error) bool {
	text := strings.ToLower(err.Error())
	for _, part := range []string{
		"offline",
		"connection",
		"eof",
		"websocket",
		"timeout",
		"temporarily",
		"refused",
	} {
		if strings.Contains(text, part) {
			return true
		}
	}
	return false
}

func validateUpgradeImage(image string) error {
	image = strings.TrimSpace(image)
	if len(image) > 512 {
		return fmt.Errorf("agent upgrade image must not exceed 512 bytes")
	}
	at := strings.LastIndex(image, "@sha256:")
	if at > 0 {
		repository := image[:at]
		digest := image[at+1:]
		if !cliImageDigest.MatchString(digest) || !cliImageRepository.MatchString(repository) {
			return fmt.Errorf("agent upgrade image must be repository:tag or repository@sha256:<64 hex digits>")
		}
		return nil
	}
	colon := strings.LastIndex(image, ":")
	slash := strings.LastIndex(image, "/")
	if colon <= slash || !cliImageRepository.MatchString(image[:colon]) || !cliImageTag.MatchString(image[colon+1:]) {
		return fmt.Errorf("agent upgrade image must be repository:tag or repository@sha256:<64 hex digits>")
	}
	return nil
}

// Validate before authentication, network access, printing or history capture.
// Errors identify the field without echoing the rejected value.
func validateUpgradeMetadata(opts UpgradeOptions) error {
	if opts.SourceCommit != "" && !releaseCommitPattern.MatchString(opts.SourceCommit) {
		return fmt.Errorf("source-commit must be a full lowercase commit SHA")
	}
	if opts.CIURL != "" {
		u, err := url.Parse(opts.CIURL)
		if err != nil || len(opts.CIURL) > 512 || u.Scheme != "https" || u.Hostname() == "" ||
			u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(opts.CIURL, "#") {
			return fmt.Errorf("ci-url must be an HTTPS URL without credentials, query or fragment")
		}
	}
	if opts.Session != "" && (!upgradeSessionPattern.MatchString(opts.Session) || strings.Contains(opts.Session, "..")) {
		return fmt.Errorf("session must be a bounded operation identifier")
	}
	for _, field := range []struct{ name, value string }{
		{"mode", opts.Mode}, {"namespace", opts.Namespace}, {"workload", opts.Workload},
		{"container", opts.Container}, {"release", opts.Release},
	} {
		if field.value != "" && !upgradeMetadataPattern.MatchString(field.value) {
			return fmt.Errorf("invalid %s metadata", field.name)
		}
	}
	return nil
}

func filterUpgradeTargets(targets []GatewayTarget, cluster, instance string) []GatewayTarget {
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	out := make([]GatewayTarget, 0, len(targets))
	for _, target := range targets {
		if cluster != "" && cluster != "*" && target.Cluster != cluster {
			continue
		}
		if instance != "" && instance != "*" && target.Instance != instance {
			continue
		}
		out = append(out, target)
	}
	return out
}
