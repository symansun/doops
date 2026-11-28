package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	doSQLRunnerTimeout    = 3 * time.Minute
	doSQLRunnerInputLimit = 1 << 20
)

var doSQLRunnerImagePattern = regexp.MustCompile(`^[-./:@A-Za-z0-9_]+$`)
var doSQLRunnerKeyPattern = regexp.MustCompile(`^[-._A-Za-z0-9]+$`)

type doSQLRunnerRequest struct {
	OperationID    string          `json:"operation_id"`
	Phase          string          `json:"phase"`
	Engine         string          `json:"engine"`
	Namespace      string          `json:"namespace"`
	DatabaseName   string          `json:"database_name"`
	SecretName     string          `json:"secret_name"`
	ClientImage    string          `json:"client_image"`
	URIKey         string          `json:"uri_key,omitempty"`
	URLKey         string          `json:"url_key,omitempty"`
	UserKey        string          `json:"user_key,omitempty"`
	PasswordKey    string          `json:"password_key,omitempty"`
	ReadOnly       bool            `json:"read_only"`
	Statement      string          `json:"statement,omitempty"`
	MongoScript    string          `json:"mongo_script,omitempty"`
	MongoOperation json.RawMessage `json:"mongo_operation,omitempty"`
}

// RunDatabaseRunnerContext is the only Go database-client entrypoint used by
// the integrated DoSql runtime. It receives metadata and SQL on stdin, creates
// a short-lived target Job, and writes only the database client output to stdout.
func RunDatabaseRunnerContext(ctx context.Context, input io.Reader, output, errorOutput io.Writer) error {
	request, err := decodeDoSQLRunnerRequest(input)
	if err != nil {
		return err
	}
	if err := validateDoSQLRunnerRequest(request); err != nil {
		return err
	}
	return runDoSQLJob(ctx, request, output, errorOutput)
}

func decodeDoSQLRunnerRequest(input io.Reader) (doSQLRunnerRequest, error) {
	decoder := json.NewDecoder(io.LimitReader(input, doSQLRunnerInputLimit))
	decoder.DisallowUnknownFields()
	var request doSQLRunnerRequest
	if err := decoder.Decode(&request); err != nil {
		return doSQLRunnerRequest{}, fmt.Errorf("database runner request is invalid: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return doSQLRunnerRequest{}, errors.New("database runner request must contain one JSON object")
	}
	return request, nil
}

func validateDoSQLRunnerRequest(request doSQLRunnerRequest) error {
	request.Engine = strings.ToLower(strings.TrimSpace(request.Engine))
	if request.Engine != "postgres" && request.Engine != "mysql" && request.Engine != "mongodb" {
		return errors.New("database runner engine is invalid")
	}
	if !validKubernetesCredentialName(request.Namespace) || !validKubernetesCredentialName(request.SecretName) {
		return errors.New("database runner namespace or Secret name is invalid")
	}
	if !validDatabaseName(request.DatabaseName) || !doSQLRunnerImagePattern.MatchString(request.ClientImage) {
		return errors.New("database runner database or image is invalid")
	}
	if strings.TrimSpace(request.OperationID) == "" || len(request.OperationID) > 128 || strings.TrimSpace(request.Phase) == "" {
		return errors.New("database runner operation id is invalid")
	}
	for _, key := range []string{request.URIKey, request.URLKey, request.UserKey, request.PasswordKey} {
		if key != "" && !doSQLRunnerKeyPattern.MatchString(key) {
			return errors.New("database runner Secret key is invalid")
		}
	}
	if request.Engine == "mongodb" {
		if strings.TrimSpace(request.MongoScript) == "" || len(request.MongoScript) > doSQLRunnerInputLimit {
			return errors.New("database runner MongoDB script is invalid")
		}
		if len(request.MongoOperation) == 0 {
			return errors.New("database runner MongoDB operation is missing")
		}
	} else if strings.TrimSpace(request.Statement) == "" || len(request.Statement) > doSQLRunnerInputLimit {
		return errors.New("database runner SQL statement is invalid")
	}
	return nil
}

func runDoSQLJob(parent context.Context, request doSQLRunnerRequest, output, errorOutput io.Writer) error {
	ctx, cancel := context.WithTimeout(parent, doSQLRunnerTimeout)
	defer cancel()

	digest := sha256.Sum256([]byte(request.OperationID + "\x1f" + request.Phase))
	identity := hex.EncodeToString(digest[:])
	if len(identity) < 12 {
		return errors.New("database runner operation identity failed")
	}
	identity = identity[:12]
	jobName := "dosql-" + request.Engine + "-" + identity
	secretName := jobName + "-input"

	secret := buildDoSQLInputSecret(request, secretName)
	if _, err := runDoSQLKubectl(ctx, []string{"create", "-f", "-"}, secret); err != nil {
		return fmt.Errorf("database runner input Secret create failed: %w", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = runDoSQLKubectl(cleanupCtx, []string{
			"delete", "secret", secretName, "-n", request.Namespace, "--ignore-not-found", "--wait=false",
		}, nil)
	}()

	job := buildDoSQLJob(request, jobName, secretName)
	if _, err := runDoSQLKubectl(ctx, []string{"create", "-f", "-"}, job); err != nil {
		return fmt.Errorf("database runner Job create failed: %w", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = runDoSQLKubectl(cleanupCtx, []string{
			"delete", "job", jobName, "-n", request.Namespace, "--ignore-not-found", "--wait=false",
		}, nil)
	}()
	if err := attachDoSQLSecretOwner(ctx, request.Namespace, secretName, jobName); err != nil {
		return err
	}

	if err := waitDoSQLJob(ctx, request.Namespace, jobName); err != nil {
		logs, _ := runDoSQLKubectl(ctx, []string{"logs", "job/" + jobName, "-n", request.Namespace, "--all-containers=true"}, nil)
		if len(logs) > 0 {
			_, _ = fmt.Fprintln(errorOutput, string(logs))
		}
		return err
	}
	logs, err := runDoSQLKubectl(ctx, []string{"logs", "job/" + jobName, "-n", request.Namespace, "--all-containers=true"}, nil)
	if err != nil {
		return fmt.Errorf("database runner logs failed: %w", err)
	}
	_, _ = output.Write(logs)
	return nil
}

func attachDoSQLSecretOwner(ctx context.Context, namespace, secretName, jobName string) error {
	payload, err := runDoSQLKubectl(ctx, []string{"get", "job", jobName, "-n", namespace, "-o", "json"}, nil)
	if err != nil {
		return fmt.Errorf("database runner Job owner lookup failed: %w", err)
	}
	var job struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(payload, &job); err != nil || strings.TrimSpace(job.Metadata.UID) == "" {
		return errors.New("database runner Job owner identity is invalid")
	}
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"ownerReferences": []interface{}{map[string]interface{}{
				"apiVersion": "batch/v1", "kind": "Job", "name": jobName, "uid": job.Metadata.UID,
				"controller": true, "blockOwnerDeletion": true,
			}},
		},
	}
	encoded, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("database runner owner patch encode failed: %w", err)
	}
	if _, err := runDoSQLKubectl(ctx, []string{"patch", "secret", secretName, "-n", namespace, "--type=merge", "--patch", string(encoded)}, nil); err != nil {
		return fmt.Errorf("database runner Secret owner patch failed: %w", err)
	}
	return nil
}

func buildDoSQLInputSecret(request doSQLRunnerRequest, secretName string) map[string]interface{} {
	data := map[string]string{}
	if request.Engine == "mongodb" {
		data["script.js"] = request.MongoScript
		data["operation.json"] = string(request.MongoOperation)
	} else {
		data["statement.sql"] = formatDoSQLRunnerStatement(request.Statement, request.Engine, request.ReadOnly)
	}
	return map[string]interface{}{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]interface{}{
			"name": secretName, "namespace": request.Namespace,
			"labels": map[string]string{"app.kubernetes.io/managed-by": "doops", "doops.sh/database-operation": request.OperationID},
		},
		"type": "Opaque", "stringData": data,
	}
}

func buildDoSQLJob(request doSQLRunnerRequest, jobName, inputSecret string) map[string]interface{} {
	envs := []interface{}{}
	envs = append(envs, map[string]interface{}{"name": "DOSQL_DATABASE", "value": request.DatabaseName})
	addSecretEnv := func(name, key string) {
		envs = append(envs, map[string]interface{}{
			"name":      name,
			"valueFrom": map[string]interface{}{"secretKeyRef": map[string]interface{}{"name": request.SecretName, "key": key}},
		})
	}
	if request.Engine == "postgres" {
		addSecretEnv("DOSQL_CONNECTION_URI", request.URIKey)
	} else if request.Engine == "mysql" {
		addSecretEnv("SPRING_DATASOURCE_URL", request.URLKey)
		addSecretEnv("SPRING_DATASOURCE_USERNAME", request.UserKey)
		addSecretEnv("SPRING_DATASOURCE_PASSWORD", request.PasswordKey)
	} else {
		addSecretEnv("DOSQL_MONGODB_URI", request.URIKey)
		envs = append(envs,
			map[string]interface{}{"name": "DOSQL_MONGODB_DATABASE", "value": request.DatabaseName},
			map[string]interface{}{"name": "DOSQL_MONGODB_REQUIRE_READ_ONLY_ROLE", "value": boolString(request.ReadOnly)},
		)
	}

	command := []string{"sh", "-ec", databaseRunnerContainerScript(request)}
	container := map[string]interface{}{
		"name": "database-client", "image": request.ClientImage, "imagePullPolicy": "IfNotPresent",
		"env": envs, "command": command,
		"volumeMounts": []interface{}{map[string]interface{}{"name": "dosql-input", "mountPath": "/run/dosql", "readOnly": true}},
	}
	podTemplate := map[string]interface{}{
		"metadata": map[string]interface{}{"labels": map[string]string{
			"app.kubernetes.io/managed-by": "doops", "doops.sh/database-operation": request.OperationID,
		}},
		"spec": map[string]interface{}{
			"restartPolicy": "Never", "terminationGracePeriodSeconds": 5,
			"containers": []interface{}{container},
			"volumes":    []interface{}{map[string]interface{}{"name": "dosql-input", "secret": map[string]interface{}{"secretName": inputSecret, "defaultMode": 0400}}},
		},
	}
	return map[string]interface{}{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]interface{}{
			"name": jobName, "namespace": request.Namespace,
			"labels": map[string]string{"app.kubernetes.io/managed-by": "doops", "doops.sh/database-operation": request.OperationID},
		},
		"spec": map[string]interface{}{
			"backoffLimit": int32(0), "activeDeadlineSeconds": int64(180), "ttlSecondsAfterFinished": int32(300),
			"template": podTemplate,
		},
	}
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func databaseRunnerContainerScript(request doSQLRunnerRequest) string {
	switch request.Engine {
	case "postgres":
		return `db="${DOSQL_CONNECTION_URI##*/}"; db="${db%%\?*}"; test "$db" = "$DOSQL_DATABASE" || { echo "configured database does not match requested database" >&2; exit 2; }; exec psql "$DOSQL_CONNECTION_URI" --no-psqlrc --set ON_ERROR_STOP=1 --no-align --tuples-only --file /run/dosql/statement.sql`
	case "mysql":
		return `case "$SPRING_DATASOURCE_URL" in jdbc:mysql://*) ;; *) echo "unsupported MySQL datasource URL" >&2; exit 2;; esac; endpoint="${SPRING_DATASOURCE_URL#jdbc:mysql://}"; authority="${endpoint%%/*}"; configured_database="${endpoint#*/}"; configured_database="${configured_database%%\?*}"; test "$configured_database" = "$DOSQL_DATABASE" || { echo "configured database does not match requested database" >&2; exit 2; }; host="${authority%%:*}"; port="${authority##*:}"; test "$host" != "$authority" || port=3306; MYSQL_PWD="$SPRING_DATASOURCE_PASSWORD" exec mysql --batch --raw --skip-column-names --host "$host" --port "$port" --user "$SPRING_DATASOURCE_USERNAME" "$DOSQL_DATABASE" < /run/dosql/statement.sql`
	default:
		return `export DOSQL_MONGODB_OPERATION="$(cat /run/dosql/operation.json)"; eval_script="$(cat /run/dosql/script.js)"; exec mongosh --nodb --quiet --eval "$eval_script"`
	}
}

func formatDoSQLRunnerStatement(statement, engine string, readOnly bool) string {
	statement = strings.TrimSpace(statement)
	if !readOnly {
		return statement + ";\n"
	}
	if engine == "postgres" {
		return "BEGIN READ ONLY;\n" + statement + ";\nCOMMIT;\n"
	}
	return "START TRANSACTION READ ONLY;\n" + statement + ";\nROLLBACK;\n"
}

func waitDoSQLJob(ctx context.Context, namespace, name string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		payload, err := runDoSQLKubectl(ctx, []string{"get", "job", name, "-n", namespace, "-o", "json"}, nil)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("database runner Job wait canceled: %w", ctx.Err())
			}
			return fmt.Errorf("database runner Job status failed: %w", err)
		}
		var job struct {
			Status struct {
				Succeeded  int32 `json:"succeeded"`
				Failed     int32 `json:"failed"`
				Conditions []struct {
					Type   string `json:"type"`
					Reason string `json:"reason"`
				} `json:"conditions"`
			} `json:"status"`
		}
		if err := json.Unmarshal(payload, &job); err != nil {
			return fmt.Errorf("database runner Job status invalid: %w", err)
		}
		if job.Status.Succeeded > 0 {
			return nil
		}
		if job.Status.Failed > 0 {
			return errors.New("database runner Job failed")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("database runner Job wait canceled: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func runDoSQLKubectl(ctx context.Context, args []string, input interface{}) ([]byte, error) {
	kubectl := strings.TrimSpace(os.Getenv("KUBECTL"))
	if kubectl == "" {
		kubectl = "kubectl"
	}
	command := exec.CommandContext(ctx, kubectl, args...)
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		command.Stdin = strings.NewReader(string(encoded))
	}
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message != "" {
			return nil, fmt.Errorf("kubectl: %s", message)
		}
		return nil, err
	}
	return []byte(stdout.String()), nil
}
