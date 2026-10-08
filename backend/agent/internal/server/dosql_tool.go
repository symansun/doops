package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
)

const (
	doSQLReadMCPToolName = "read"
	doSQLMCPToolName     = "execute"
)

var doSQLReadOperations = []string{"query", "discover"}
var doSQLMutationOperations = []string{"execute"}

type doSQLParams struct {
	SessionID             string          `json:"session_id"`
	Operation             string          `json:"operation"`
	Confirm               bool            `json:"confirm,omitempty"`
	CredentialRef         string          `json:"credential_ref"`
	DatabaseAssetID       string          `json:"database_asset_id"`
	EnvironmentID         string          `json:"environment_id"`
	ProjectID             string          `json:"project_id"`
	Engine                string          `json:"engine"`
	DatabaseName          string          `json:"database_name"`
	Statement             string          `json:"statement,omitempty"`
	MongoOperation        json.RawMessage `json:"mongo_operation,omitempty"`
	VerificationStatement string          `json:"verification_statement,omitempty"`
	VerificationMongo     json.RawMessage `json:"verification_mongo_operation,omitempty"`
	ChangeRequestID       string          `json:"change_request_id,omitempty"`
	ApprovalArtifactPath  string          `json:"approval_artifact_path,omitempty"`
}

type doSQLSecret struct {
	Metadata struct {
		ResourceVersion string            `json:"resourceVersion"`
		Labels          map[string]string `json:"labels"`
		Annotations     map[string]string `json:"annotations"`
	} `json:"metadata"`
	Data map[string]string `json:"data"`
}

func doSQLMCPToolDefinition(name string, operations []string, description string) map[string]interface{} {
	queryRequired := []string{"operation", "credential_ref", "database_asset_id", "environment_id", "project_id", "engine", "database_name"}
	required := queryRequired
	if name == doSQLReadMCPToolName && containsDoSQLString(operations, "discover") {
		required = []string{"operation"}
	}
	if name == doSQLMCPToolName {
		required = append(required, "confirm", "change_request_id", "approval_artifact_path")
	}
	properties := map[string]interface{}{
		"operation":                    map[string]interface{}{"type": "string", "enum": operations},
		"confirm":                      map[string]interface{}{"type": "boolean"},
		"credential_ref":               map[string]interface{}{"type": "string"},
		"database_asset_id":            map[string]interface{}{"type": "string"},
		"environment_id":               map[string]interface{}{"type": "string"},
		"project_id":                   map[string]interface{}{"type": "string"},
		"engine":                       map[string]interface{}{"type": "string", "enum": []string{"mysql", "postgres", "mongodb"}},
		"database_name":                map[string]interface{}{"type": "string"},
		"statement":                    map[string]interface{}{"type": "string"},
		"mongo_operation":              map[string]interface{}{"type": "object"},
		"verification_statement":       map[string]interface{}{"type": "string"},
		"verification_mongo_operation": map[string]interface{}{"type": "object"},
		"change_request_id":            map[string]interface{}{"type": "string"},
		"approval_artifact_path":       map[string]interface{}{"type": "string"},
	}
	schema := map[string]interface{}{
		"type": "object", "additionalProperties": false, "required": required, "properties": properties,
	}
	if name == doSQLReadMCPToolName && containsDoSQLString(operations, "discover") {
		schema["oneOf"] = []interface{}{
			map[string]interface{}{
				"properties": map[string]interface{}{"operation": map[string]interface{}{"const": "discover"}},
				"required":   []string{"operation"},
			},
			map[string]interface{}{
				"properties": map[string]interface{}{"operation": map[string]interface{}{"const": "query"}},
				"required":   queryRequired,
			},
		}
	}
	return map[string]interface{}{
		"name": name, "description": description,
		"inputSchema": schema,
	}
}

func callDoSQLTool(ctx context.Context, request k8sMCPRequest, boundSessionID string, mutation bool) api.JSONRPCResponse {
	var call k8sMCPToolCall
	if err := json.Unmarshal(request.Params, &call); err != nil {
		return mcpErrorResponse(request.ID, -32602, "invalid tools/call params")
	}
	if (mutation && !k8sMCPOperationAllowed(call.Arguments, doSQLMutationOperations)) ||
		(!mutation && !k8sMCPOperationAllowed(call.Arguments, doSQLReadOperations)) {
		return doSQLMCPErrorResponse(request.ID, -32602, "unsupported SQL operation")
	}
	bound, err := bindK8SMCPSession(call.Arguments, boundSessionID)
	if err != nil {
		return doSQLMCPErrorResponse(request.ID, -32602, err.Error())
	}
	result, err := handleDoSQLTool(ctx, bound, mutation)
	if err != nil {
		if errors.Is(err, errExecutionOutcomeUnknown) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return mcpErrorResponse(request.ID, -32008, errExecutionOutcomeUnknown.Error())
		}
		return doSQLMCPErrorResponse(request.ID, -32003, sanitizeSecretText(err.Error()))
	}
	return api.JSONRPCResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]interface{}{
		"content": []interface{}{map[string]interface{}{"type": "text", "text": result}},
		"isError": false,
	}}
}

func doSQLMCPErrorResponse(id interface{}, code int, message string) api.JSONRPCResponse {
	return api.JSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &api.RPCError{Code: code, Message: "dosql: " + message}}
}

func handleDoSQLTool(ctx context.Context, raw json.RawMessage, mutation bool) (string, error) {
	var params doSQLParams
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return "", fmt.Errorf("invalid SQL request")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return "", fmt.Errorf("invalid SQL request")
	}
	if params.SessionID == "" || params.Operation == "" {
		return "", errors.New("dosql_request_invalid")
	}
	if !mutation && params.Operation == "discover" {
		return discoverDatabaseCandidates(ctx)
	}
	if params.CredentialRef == "" || params.DatabaseAssetID == "" || params.EnvironmentID == "" || params.ProjectID == "" ||
		params.DatabaseName == "" || !validDatabaseName(params.DatabaseName) {
		return "", errors.New("dosql_request_invalid")
	}
	params.Engine = strings.ToLower(strings.TrimSpace(params.Engine))
	if params.Engine != "mysql" && params.Engine != "postgres" && params.Engine != "mongodb" {
		return "", errors.New("dosql_engine_invalid")
	}
	if err := authorizeDoSQLDatabaseAsset(ctx, params); err != nil {
		return "", err
	}
	if mutation {
		if params.Operation != "execute" || !params.Confirm || params.ChangeRequestID == "" || params.ApprovalArtifactPath == "" {
			return "", errors.New("dosql_mutation_confirmation_required")
		}
	} else if params.Operation != "query" {
		return "", errors.New("dosql_read_operation_invalid")
	}
	binding, err := authorizedDoSQLCredential(params.SessionID, params.CredentialRef)
	if err != nil {
		return "", err
	}
	if !validDatabaseCredentialKeys(params.Engine, binding.Keys) {
		return "", errors.New("dosql_credential_keys_invalid")
	}
	if err := validateDoSQLSecretBinding(ctx, binding); err != nil {
		return "", err
	}
	workspace, err := resolveWebSession(params.SessionID)
	if err != nil {
		return "", err
	}
	operationID := "dbop_" + randomDoSQLID()
	artifactRoot := filepath.Join(workspace, ".dosql")
	operationDir := filepath.Join(artifactRoot, "operations")
	if err := os.MkdirAll(operationDir, 0700); err != nil {
		return "", errors.New("dosql_artifact_root_unavailable")
	}
	journalPath := filepath.Join(operationDir, operationID+".journal.jsonl")
	evidencePath := filepath.Join(operationDir, operationID+".evidence.json")
	tmpDir, err := os.MkdirTemp(filepath.Join(workspace, ".dosql"), "request-")
	if err != nil {
		return "", errors.New("dosql_request_workspace_unavailable")
	}
	defer os.RemoveAll(tmpDir)
	inputPath := filepath.Join(tmpDir, "request.json")
	outputPath := filepath.Join(tmpDir, "response.json")
	request, env, err := buildDoSQLExecutionRequest(params, binding, operationID, journalPath, evidencePath, mutation)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", errors.New("dosql_request_encode_failed")
	}
	if err := os.WriteFile(inputPath, encoded, 0600); err != nil {
		return "", errors.New("dosql_request_write_failed")
	}
	node := os.Getenv("DOSQL_NODE")
	if node == "" {
		node = "node"
	}
	script := os.Getenv("DOSQL_SCRIPT")
	if script == "" {
		script = "/app/skills/dosql/scripts/dosql-agent.mjs"
	}
	command := exec.CommandContext(ctx, node, script, params.Operation, "--input", inputPath, "--output", outputPath)
	command.Dir = workspace
	command.Env = append(os.Environ(), env...)
	if output, err := command.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return "", errExecutionOutcomeUnknown
		}
		_ = output
		return "", errors.New("dosql_execution_failed")
	}
	responseBytes, err := os.ReadFile(outputPath)
	if err != nil {
		return "", errors.New("dosql_response_missing")
	}
	var response struct {
		Status string                 `json:"status"`
		Result map[string]interface{} `json:"result"`
		Error  map[string]interface{} `json:"error"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return "", errors.New("dosql_response_invalid")
	}
	if response.Status != "succeeded" {
		return "", errors.New("dosql_execution_failed")
	}
	if status, _ := response.Result["status"].(string); status != "verified" {
		return "", errors.New("dosql_verification_failed")
	}
	return doSQLResultSummary(response.Result, operationID, journalPath, evidencePath), nil
}

type doSQLDatabaseCandidate struct {
	DatabaseAssetID string `json:"database_asset_id"`
	Cluster         string `json:"cluster,omitempty"`
	Instance        string `json:"instance,omitempty"`
	Namespace       string `json:"namespace"`
	Service         string `json:"service"`
	Engine          string `json:"engine"`
	Host            string `json:"host"`
	Port            int    `json:"port,omitempty"`
	DatabaseName    string `json:"database_name,omitempty"`
	Status          string `json:"status"`
	Source          string `json:"source"`
}

// discoverDatabaseCandidateList inspects only non-secret Kubernetes metadata.
// It never reads Secret values; credentials are required for an actual
// database connection after the user selects a candidate.
func discoverDatabaseCandidateList(ctx context.Context) (string, string, []doSQLDatabaseCandidate, error) {
	command := exec.CommandContext(ctx, "kubectl", "get", "pods,services,deployments,statefulsets", "--all-namespaces", "--output", "json")
	output, err := command.Output()
	if err != nil {
		return "", "", nil, errors.New("dosql_discovery_unavailable")
	}
	var list struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(output, &list); err != nil {
		return "", "", nil, errors.New("dosql_discovery_invalid")
	}
	cluster := strings.TrimSpace(os.Getenv("DOOPS_GATEWAY_CLUSTER"))
	instance := strings.TrimSpace(os.Getenv("DOOPS_GATEWAY_INSTANCE"))
	seen := map[string]doSQLDatabaseCandidate{}
	for _, item := range list.Items {
		metadata, _ := item["metadata"].(map[string]interface{})
		namespace, _ := metadata["namespace"].(string)
		name, _ := metadata["name"].(string)
		if namespace == "" || name == "" {
			continue
		}
		engine := detectDatabaseEngine(item)
		if engine == "" {
			continue
		}
		databaseName := detectDatabaseName(item, engine)
		port := detectDatabasePort(item, engine)
		key := strings.Join([]string{namespace, name, engine}, "/")
		status := "candidate"
		if databaseName == "" {
			status = "needs_database_name_or_credential"
		}
		seen[key] = doSQLDatabaseCandidate{
			DatabaseAssetID: databaseAssetID(cluster, instance, namespace, name, engine),
			Cluster:         cluster, Instance: instance, Namespace: namespace, Service: name,
			Engine: engine, Host: name + "." + namespace + ".svc", Port: port,
			DatabaseName: databaseName, Status: status, Source: stringValue(item["kind"]),
		}
	}
	items := make([]doSQLDatabaseCandidate, 0, len(seen))
	for _, value := range seen {
		items = append(items, value)
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.Join([]string{items[i].Namespace, items[i].Service, items[i].Engine}, "/") < strings.Join([]string{items[j].Namespace, items[j].Service, items[j].Engine}, "/")
	})
	return cluster, instance, items, nil
}

func discoverDatabaseCandidates(ctx context.Context) (string, error) {
	cluster, instance, items, err := discoverDatabaseCandidateList(ctx)
	if err != nil {
		return "", err
	}
	result := map[string]interface{}{
		"status": "discovered", "cluster": cluster, "instance": instance,
		"candidates": items,
		"next_step":  "选择 database_asset_id；如果 database_name 缺失，提供对应 database credential 后再执行数据库目录发现。",
	}
	encoded, _ := json.Marshal(result)
	return string(encoded), nil
}

func authorizeDoSQLDatabaseAsset(ctx context.Context, params doSQLParams) error {
	cluster, instance, candidates, err := discoverDatabaseCandidateList(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if candidate.DatabaseAssetID != params.DatabaseAssetID {
			continue
		}
		if candidate.Cluster != cluster || candidate.Instance != instance || candidate.Engine != params.Engine {
			return errors.New("dosql_database_asset_mismatch")
		}
		if candidate.DatabaseName != "" && candidate.DatabaseName != params.DatabaseName {
			return errors.New("dosql_database_name_mismatch")
		}
		return nil
	}
	return errors.New("dosql_database_asset_not_found")
}

func databaseAssetID(cluster, instance, namespace, name, engine string) string {
	value := strings.ToLower(strings.Join([]string{cluster, instance, namespace, name, engine}, "_"))
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	return "dbscan_" + strings.Trim(builder.String(), "_")
}

func detectDatabaseEngine(item map[string]interface{}) string {
	parts := []string{stringValue(item["kind"])}
	metadata, _ := item["metadata"].(map[string]interface{})
	parts = append(parts, stringValue(metadata["name"]))
	if labels, ok := metadata["labels"].(map[string]interface{}); ok {
		for key, value := range labels {
			parts = append(parts, key, stringValue(value))
		}
	}
	for _, image := range containerImages(item) {
		parts = append(parts, image)
	}
	value := strings.ToLower(strings.Join(parts, " "))
	switch {
	case strings.Contains(value, "mongodb") || strings.Contains(value, "mongo"):
		return "mongodb"
	case strings.Contains(value, "postgres") || strings.Contains(value, "postgresql"):
		return "postgres"
	case strings.Contains(value, "mysql") || strings.Contains(value, "mariadb"):
		return "mysql"
	default:
		return ""
	}
}

func detectDatabaseName(item map[string]interface{}, engine string) string {
	for _, env := range containerEnv(item) {
		name := strings.ToUpper(stringValue(env["name"]))
		value, _ := env["value"].(string)
		if value == "" || env["valueFrom"] != nil {
			continue
		}
		if name == "POSTGRES_DB" || name == "MYSQL_DATABASE" || name == "MONGO_INITDB_DATABASE" || name == "DB_NAME" || name == "DATABASE_NAME" {
			return value
		}
		if (engine == "postgres" || engine == "mysql") && strings.Contains(name, "DATASOURCE_URL") {
			if parsed, err := url.Parse(value); err == nil {
				return strings.Trim(parsed.Path, "/")
			}
		}
	}
	return ""
}

func detectDatabasePort(item map[string]interface{}, engine string) int {
	defaults := map[string]int{"mysql": 3306, "postgres": 5432, "mongodb": 27017}
	spec, _ := item["spec"].(map[string]interface{})
	ports, _ := spec["ports"].([]interface{})
	for _, raw := range ports {
		port, _ := raw.(map[string]interface{})
		if number, ok := port["port"].(float64); ok && number > 0 {
			return int(number)
		}
	}
	return defaults[engine]
}

func containerImages(item map[string]interface{}) []string {
	var result []string
	appendContainers := func(value interface{}) {
		containers, _ := value.([]interface{})
		for _, raw := range containers {
			container, _ := raw.(map[string]interface{})
			if image := stringValue(container["image"]); image != "" {
				result = append(result, image)
			}
			if name := stringValue(container["name"]); name != "" {
				result = append(result, name)
			}
		}
	}
	spec, _ := item["spec"].(map[string]interface{})
	appendContainers(spec["containers"])
	template, _ := spec["template"].(map[string]interface{})
	templateSpec, _ := template["spec"].(map[string]interface{})
	appendContainers(templateSpec["containers"])
	return result
}

func containerEnv(item map[string]interface{}) []map[string]interface{} {
	var result []map[string]interface{}
	spec, _ := item["spec"].(map[string]interface{})
	collect := func(value interface{}) {
		containers, _ := value.([]interface{})
		for _, raw := range containers {
			container, _ := raw.(map[string]interface{})
			env, _ := container["env"].([]interface{})
			for _, envRaw := range env {
				if entry, ok := envRaw.(map[string]interface{}); ok {
					result = append(result, entry)
				}
			}
		}
	}
	collect(spec["containers"])
	template, _ := spec["template"].(map[string]interface{})
	templateSpec, _ := template["spec"].(map[string]interface{})
	collect(templateSpec["containers"])
	return result
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func containsDoSQLString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func validateDoSQLSecretBinding(ctx context.Context, binding api.AgentCredentialBinding) error {
	command := exec.CommandContext(ctx, "kubectl", "get", "secret", binding.ResourceName,
		"-n", binding.Namespace, "-o", "json")
	output, err := command.Output()
	if err != nil {
		return errors.New("dosql_credential_secret_unavailable")
	}
	var secret doSQLSecret
	if err := json.Unmarshal(output, &secret); err != nil {
		return errors.New("dosql_credential_secret_invalid")
	}
	if secret.Metadata.Labels["doops.sh/credential-id"] != binding.CredentialID ||
		secret.Metadata.Labels["doops.sh/credential-grant-id"] != binding.GrantID ||
		secret.Metadata.Annotations["doops.sh/credential-version"] != binding.VersionID ||
		(secret.Metadata.ResourceVersion != "" && binding.ResourceVersion != "" && secret.Metadata.ResourceVersion != binding.ResourceVersion) {
		return errors.New("dosql_credential_secret_scope_mismatch")
	}
	for _, key := range binding.Keys {
		if _, ok := secret.Data[key]; !ok {
			return errors.New("dosql_credential_secret_key_missing")
		}
	}
	return nil
}

func buildDoSQLExecutionRequest(params doSQLParams, binding api.AgentCredentialBinding, operationID, journalPath, evidencePath string, mutation bool) (map[string]interface{}, []string, error) {
	request := map[string]interface{}{
		"operationId": operationID, "projectId": params.ProjectID, "databaseAssetId": params.DatabaseAssetID,
		"environmentId": params.EnvironmentID, "engine": params.Engine, "databaseName": params.DatabaseName,
		"actor": map[string]string{"type": "doops-agent", "id": "dosql"}, "journalPath": journalPath, "evidencePath": evidencePath,
	}
	if params.Engine == "mongodb" {
		if len(params.MongoOperation) == 0 {
			return nil, nil, errors.New("mongo_operation_required")
		}
		var operation interface{}
		if err := json.Unmarshal(params.MongoOperation, &operation); err != nil {
			return nil, nil, errors.New("mongo_operation_invalid")
		}
		request["mongoOperation"] = operation
		if mutation {
			var verification interface{}
			if len(params.VerificationMongo) == 0 || json.Unmarshal(params.VerificationMongo, &verification) != nil {
				return nil, nil, errors.New("verification_mongo_operation_required")
			}
			request["verificationMongoOperation"] = verification
		}
	} else {
		request["statement"] = params.Statement
		if strings.TrimSpace(params.Statement) == "" {
			return nil, nil, errors.New("statement_required")
		}
		if mutation {
			request["verificationStatement"] = params.VerificationStatement
			request["changeRequestId"] = params.ChangeRequestID
			request["approvalArtifactPath"] = params.ApprovalArtifactPath
		}
	}
	env := []string{}
	if params.Engine == "mysql" {
		request["adapter"] = map[string]interface{}{"type": "mysql-cli", "mysqlPath": "/app/skills/dosql/Server/scripts/doops-mysql-client.sh"}
		env = append(env,
			"DOSQL_MYSQL_NAMESPACE="+binding.Namespace,
			"DOSQL_MYSQL_DATABASE="+params.DatabaseName,
			"DOSQL_MYSQL_SECRET_NAME="+binding.ResourceName,
			"DOSQL_MYSQL_CLIENT_IMAGE="+doSQLClientImage(),
			"DOSQL_MYSQL_URL_KEY=SPRING_DATASOURCE_URL",
			"DOSQL_MYSQL_USER_KEY=SPRING_DATASOURCE_USERNAME",
			"DOSQL_MYSQL_PASSWORD_KEY=SPRING_DATASOURCE_PASSWORD",
		)
	} else if params.Engine == "postgres" {
		request["adapter"] = map[string]interface{}{"type": "postgres-psql", "psqlPath": "/app/skills/dosql/Server/scripts/doops-postgres-client.sh", "connectionUriEnv": "DOSQL_POSTGRES_URI"}
		env = append(env,
			"DOSQL_POSTGRES_URI="+"", // populated by the target-side Secret wrapper
			"DOSQL_POSTGRES_NAMESPACE="+binding.Namespace,
			"DOSQL_POSTGRES_DATABASE="+params.DatabaseName,
			"DOSQL_POSTGRES_SECRET_NAME="+binding.ResourceName,
			"DOSQL_POSTGRES_URI_KEY=connection_uri",
		)
	} else {
		request["adapter"] = map[string]interface{}{"type": "mongodb-mongosh", "mongoshPath": "/app/skills/dosql/Server/scripts/doops-mongodb-client.sh", "connectionUriEnv": "DOSQL_MONGODB_URI"}
		env = append(env,
			"DOSQL_MONGODB_URI="+"", // populated by the target-side Secret wrapper
			"DOSQL_MONGODB_NAMESPACE="+binding.Namespace,
			"DOSQL_MONGODB_DATABASE="+params.DatabaseName,
			"DOSQL_MONGODB_SECRET_NAME="+binding.ResourceName,
			"DOSQL_MONGODB_URI_KEY=connection_uri",
		)
	}
	return request, env, nil
}

func doSQLClientImage() string {
	if value := strings.TrimSpace(os.Getenv("DOSQL_CLIENT_IMAGE")); value != "" {
		return value
	}
	return "mysql:8.4"
}

func doSQLResultSummary(result map[string]interface{}, operationID, journalPath, evidencePath string) string {
	summary := map[string]interface{}{"status": "verified", "operation_id": operationID, "journal_path": journalPath, "evidence_path": evidencePath}
	if execution, ok := result["executionResult"].(map[string]interface{}); ok {
		for _, key := range []string{"affectedRows", "transactionId"} {
			if value, exists := execution[key]; exists {
				summary[key] = value
			}
		}
	}
	encoded, _ := json.Marshal(summary)
	return string(encoded)
}

func randomDoSQLID() string {
	return fmt.Sprintf("%d-%06d", time.Now().UnixNano(), rand.Intn(1000000))
}
