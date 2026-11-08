package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// An external driver is an executable the operator drops into the driver
// directory. The Agent runs it once per batch; it owns the connection, the
// transaction and the rollback, so the Agent stays free of its client library.
const (
	doSQLExternalDriver = "external"
	doSQLExternalDirEnv = "DOOPS_DOSQL_DRIVER_DIR"
)

// The default directory sits beside the version journal, so drivers survive a
// Pod replacement exactly when the journal does.
func doSQLExternalDriverDir() string {
	if dir := os.Getenv(doSQLExternalDirEnv); dir != "" {
		return dir
	}
	return filepath.Join(filepath.Dir(workspaceRoot()), ".doops-dosql-drivers")
}

var doSQLExternalSchemePattern = regexp.MustCompile(`^[a-z][a-z0-9]{1,31}$`)

type doSQLExternalRequest struct {
	DSN          string   `json:"dsn"`
	Statements   []string `json:"statements"`
	Verification string   `json:"verification,omitempty"`
	ReadOnly     bool     `json:"read_only"`
	DryRun       bool     `json:"dry_run"`
}

type doSQLExternalResponse struct {
	Result doSQLDirectResult `json:"result"`
	Error  string            `json:"error,omitempty"`
}

func doSQLExternalDriverPath(scheme string) (string, error) {
	if !doSQLExternalSchemePattern.MatchString(scheme) {
		return "", errors.New("dosql_engine_unsupported")
	}
	path := filepath.Join(doSQLExternalDriverDir(), "dosql-driver-"+scheme)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o002 != 0 {
		return "", errors.New("dosql_engine_unsupported")
	}
	return path, nil
}

func doSQLExternalTarget(engine, databaseName, raw string) (doSQLTarget, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || strings.ToLower(parsed.Scheme) != engine {
		return doSQLTarget{}, fmt.Errorf("dosql_connection_uri_invalid: %s credential needs connection_uri %s://user:password@host:port/database", engine, engine)
	}
	if database := strings.Trim(parsed.Path, "/"); database != databaseName {
		return doSQLTarget{}, fmt.Errorf("dosql_database_name_mismatch: credential points to %q, request names %q", database, databaseName)
	}
	return doSQLTarget{Engine: engine, Driver: doSQLExternalDriver, DSN: parsed.String(), Database: databaseName, Address: parsed.Host}, nil
}

// The DSN carries the password, so it travels on stdin only: never argv or env.
func executeDoSQLExternal(ctx context.Context, request doSQLDirectRequest) (doSQLDirectResult, error) {
	result := doSQLDirectResult{Status: "failed"}
	path, err := doSQLExternalDriverPath(request.Target.Engine)
	if err != nil {
		return result, err
	}
	input, _ := json.Marshal(doSQLExternalRequest{
		DSN: request.Target.DSN, Statements: request.Statements, Verification: request.Verification,
		ReadOnly: request.ReadOnly, DryRun: request.DryRun,
	})
	var stdout bytes.Buffer
	command := exec.CommandContext(ctx, path)
	command.Stdin, command.Stdout, command.Env = bytes.NewReader(input), &stdout, []string{}
	runErr := command.Run()
	var response doSQLExternalResponse
	if json.Unmarshal(stdout.Bytes(), &response) != nil {
		return result, doSQLFailure("dosql_driver_failed", fmt.Sprintf("%s did not return a result (%v)", filepath.Base(path), runErr))
	}
	if response.Error != "" {
		return response.Result, errors.New(strings.Join(strings.Fields(sanitizeSecretText(response.Error)), " "))
	}
	return response.Result, nil
}

// DoSQLMySQLDSN turns the connection URL the Agent sends into go-sql-driver's DSN.
func DoSQLMySQLDSN(rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	parsed.Scheme = "mysql"
	target, err := doSQLMySQLTarget(strings.Trim(parsed.Path, "/"), parsed.String(), "mysql://", "", "")
	return target.DSN, err
}

// RunDoSQLDriver is the whole body of an external driver: read one request,
// run it with the named database/sql driver (already imported by the caller),
// write one response. toDSN converts the connection URL into the driver's own
// DSN form; nil passes the URL through.
func RunDoSQLDriver(engine, driver string, toDSN func(string) (string, error), in io.Reader, out io.Writer) error {
	var request doSQLExternalRequest
	if err := json.NewDecoder(in).Decode(&request); err != nil {
		return err
	}
	if !slices.Contains(sql.Drivers(), driver) {
		return fmt.Errorf("driver %q is not linked into this binary", driver)
	}
	dsn := request.DSN
	if toDSN != nil {
		var err error
		if dsn, err = toDSN(dsn); err != nil {
			return err
		}
	}
	result, err := executeDoSQLDirect(context.Background(), doSQLDirectRequest{
		Target:     doSQLTarget{Engine: engine, Driver: driver, DSN: dsn},
		Statements: request.Statements, Verification: request.Verification,
		ReadOnly: request.ReadOnly, DryRun: request.DryRun,
	})
	response := doSQLExternalResponse{Result: result}
	if err != nil {
		response.Error = err.Error()
	}
	return json.NewEncoder(out).Encode(response)
}
