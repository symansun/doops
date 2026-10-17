package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
)

type doSQLDirectReport struct {
	OperationID          string            `json:"operation_id"`
	Mode                 string            `json:"mode"`
	Engine               string            `json:"engine"`
	Database             string            `json:"database"`
	Credential           string            `json:"credential"`
	StatementFingerprint string            `json:"statement_fingerprint"`
	EvidencePath         string            `json:"evidence_path,omitempty"`
	Result               doSQLDirectResult `json:"result"`
}

func doSQLDirectRequested(args api.AgentPromptParams) bool {
	return strings.TrimSpace(args.Skill) == "dosql" && strings.TrimSpace(args.SQL) != ""
}

func handleDirectDoSQLPrompt(ctx context.Context, sessionID string, args api.AgentPromptParams) (string, error) {
	binding, err := selectDirectDoSQLBinding(args.CredentialBindings)
	if err != nil {
		return "", err
	}
	data, err := readDoSQLSecretData(ctx, binding)
	if err != nil {
		return "", err
	}
	engine, database, err := inferDoSQLEngine(data)
	if err != nil {
		return "", err
	}
	if expected := strings.TrimSpace(args.SQLDatabase); expected != "" && expected != database {
		return "", fmt.Errorf("dosql_database_name_mismatch: credential %s points to %q, --database names %q", binding.Name, database, expected)
	}
	target, err := doSQLConnectionTarget(engine, database, data)
	if err != nil {
		return "", err
	}
	request, err := planDirectDoSQL(engine, args)
	if err != nil {
		return "", err
	}
	request.Target = target
	report := doSQLDirectReport{
		OperationID: "dbop_" + randomHex(8), Mode: directDoSQLMode(request), Engine: engine,
		Database: database, Credential: binding.Name,
		StatementFingerprint: "sha256:" + doSQLStatementFingerprint(request.Statements),
	}
	result, execErr := executeDoSQLDirect(ctx, request)
	report.Result = result
	report.EvidencePath = writeDirectDoSQLEvidence(sessionID, report, execErr)
	if execErr != nil {
		return "", fmt.Errorf("%w (operation_id=%s)", execErr, report.OperationID)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", errors.New("dosql_result_encode_failed")
	}
	return string(encoded), nil
}

// planDirectDoSQL turns the verbatim SQL text into an executable request; a
// query prompt only accepts read statements so ask authority never mutates.
func planDirectDoSQL(engine string, args api.AgentPromptParams) (doSQLDirectRequest, error) {
	apply := strings.ToLower(strings.TrimSpace(args.Operation)) == "apply"
	statements, err := splitDoSQLStatements(engine, args.SQL)
	if err != nil {
		return doSQLDirectRequest{}, err
	}
	request := doSQLDirectRequest{Statements: statements, ReadOnly: !apply, DryRun: apply && args.SQLDryRun}
	if !apply {
		if args.SQLDryRun || strings.TrimSpace(args.SQLVerification) != "" {
			return doSQLDirectRequest{}, errors.New("dosql_query_options_invalid: --verify and --dry-run require --mode apply")
		}
		for index, statement := range statements {
			if !doSQLReadOnlyStatement(statement) {
				return doSQLDirectRequest{}, fmt.Errorf("dosql_query_requires_read_only: statement %d is not a read statement; use --mode apply", index+1)
			}
		}
		return request, nil
	}
	if strings.TrimSpace(args.SQLVerification) != "" {
		verification, err := splitDoSQLStatements(engine, args.SQLVerification)
		if err != nil {
			return doSQLDirectRequest{}, err
		}
		if len(verification) != 1 || !doSQLReadOnlyStatement(verification[0]) {
			return doSQLDirectRequest{}, errors.New("dosql_verification_invalid: verification must be exactly one read statement")
		}
		request.Verification = verification[0]
	}
	return request, nil
}

func directDoSQLMode(request doSQLDirectRequest) string {
	switch {
	case request.ReadOnly:
		return "query"
	case request.DryRun:
		return "dry_run"
	default:
		return "apply"
	}
}

func selectDirectDoSQLBinding(bindings []api.AgentCredentialBinding) (api.AgentCredentialBinding, error) {
	var matches []api.AgentCredentialBinding
	for _, binding := range bindings {
		if binding.Use == string(CredentialUseDatabase) {
			matches = append(matches, binding)
		}
	}
	switch len(matches) {
	case 0:
		return api.AgentCredentialBinding{}, errors.New("dosql_credential_missing: bind a database credential to this target or pass --credential <name>")
	case 1:
		return matches[0], nil
	}
	names := make([]string, 0, len(matches))
	for _, binding := range matches {
		names = append(names, binding.Name)
	}
	sort.Strings(names)
	return api.AgentCredentialBinding{}, fmt.Errorf("dosql_credential_ambiguous: pass --credential, bound: %s", strings.Join(names, ","))
}

func inferDoSQLEngine(data map[string]string) (string, string, error) {
	if raw := strings.TrimSpace(data["SPRING_DATASOURCE_URL"]); raw != "" {
		parsed, err := url.Parse("mysql://" + strings.TrimPrefix(raw, "jdbc:mysql://"))
		if err != nil || !strings.HasPrefix(raw, "jdbc:mysql://") {
			return "", "", errors.New("dosql_connection_uri_invalid: SPRING_DATASOURCE_URL must be jdbc:mysql://host:port/database")
		}
		return "mysql", strings.Trim(parsed.Path, "/"), nil
	}
	parsed, err := url.Parse(strings.TrimSpace(data["connection_uri"]))
	if err != nil || parsed.Scheme == "" {
		return "", "", errors.New("dosql_connection_uri_invalid: credential needs connection_uri or SPRING_DATASOURCE_URL")
	}
	database := strings.Trim(parsed.Path, "/")
	switch parsed.Scheme {
	case "postgres", "postgresql":
		return "postgres", database, nil
	case "mysql":
		return "mysql", database, nil
	case "oracle":
		return "oracle", database, nil
	}
	return "", "", fmt.Errorf("dosql_engine_unsupported: %s is not supported for direct SQL", parsed.Scheme)
}

// Evidence is best effort: a full disk must not turn a committed change into an error.
func writeDirectDoSQLEvidence(sessionID string, report doSQLDirectReport, execErr error) string {
	workspace, err := workspacePath(sessionID)
	if err != nil {
		return ""
	}
	directory := filepath.Join(workspace, ".dosql", "operations")
	if os.MkdirAll(directory, 0700) != nil {
		return ""
	}
	evidence := map[string]interface{}{
		"schema": "dosql.direct-evidence.v1", "recorded_at": time.Now().UTC().Format(time.RFC3339Nano), "report": report,
	}
	if execErr != nil {
		evidence["error"] = sanitizeSecretText(execErr.Error())
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return ""
	}
	path := filepath.Join(directory, report.OperationID+".evidence.json")
	if os.WriteFile(path, encoded, 0600) != nil {
		return ""
	}
	return path
}
