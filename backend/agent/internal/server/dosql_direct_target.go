package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/user/doops/agent/api"
)

type doSQLTarget struct {
	Engine   string
	Driver   string
	DSN      string
	Database string
	Address  string
}

// readDoSQLSecretData reads the Gateway-materialized Secret only after its
// credential identity labels prove it is the binding this operation was granted.
func readDoSQLSecretData(ctx context.Context, binding api.AgentCredentialBinding) (map[string]string, error) {
	output, err := exec.CommandContext(ctx, "kubectl", "get", "secret", binding.ResourceName,
		"-n", binding.Namespace, "-o", "json").Output()
	if err != nil {
		return nil, errors.New("dosql_credential_secret_unavailable")
	}
	var secret doSQLSecret
	if err := json.Unmarshal(output, &secret); err != nil {
		return nil, errors.New("dosql_credential_secret_invalid")
	}
	if secret.Metadata.Labels["doops.sh/credential-id"] != binding.CredentialID ||
		secret.Metadata.Labels["doops.sh/credential-grant-id"] != binding.GrantID ||
		secret.Metadata.Annotations["doops.sh/credential-version"] != binding.VersionID {
		return nil, errors.New("dosql_credential_secret_scope_mismatch")
	}
	data := make(map[string]string, len(binding.Keys))
	for _, key := range binding.Keys {
		encoded, ok := secret.Data[key]
		if !ok {
			return nil, errors.New("dosql_credential_secret_key_missing")
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, errors.New("dosql_credential_secret_invalid")
		}
		data[key] = string(decoded)
	}
	return data, nil
}

func doSQLConnectionTarget(engine, databaseName string, data map[string]string) (doSQLTarget, error) {
	switch engine {
	case "postgres":
		return doSQLURITarget(engine, "pgx", databaseName, data["connection_uri"], doSQLSchemesForEngine(engine), "5432")
	case "oracle":
		return doSQLURITarget(engine, "oracle", databaseName, data["connection_uri"], doSQLSchemesForEngine(engine), "1521")
	case "mongodb":
		return doSQLURITarget(engine, "mongo", databaseName, data["connection_uri"], doSQLSchemesForEngine(engine), "27017")
	case "mssql":
		return doSQLMSSQLTarget(databaseName, data["connection_uri"])
	case "mysql":
		if uri := data["connection_uri"]; uri != "" {
			return doSQLMySQLTarget(databaseName, uri, "", "", "")
		}
		return doSQLMySQLTarget(databaseName, data["SPRING_DATASOURCE_URL"], "jdbc:mysql://",
			data["SPRING_DATASOURCE_USERNAME"], data["SPRING_DATASOURCE_PASSWORD"])
	}
	if _, err := doSQLExternalDriverPath(engine); err == nil {
		return doSQLExternalTarget(engine, databaseName, data["connection_uri"])
	}
	return doSQLTarget{}, errors.New("dosql_engine_invalid")
}

// The mssql driver takes its database as a query parameter, so the URL path is
// moved there before the DSN is formed.
func doSQLMSSQLTarget(databaseName, raw string) (doSQLTarget, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "sqlserver" && parsed.Scheme != "mssql") {
		return doSQLTarget{}, errors.New("dosql_connection_uri_invalid: mssql credential needs connection_uri sqlserver://user:password@host:port/database")
	}
	database := strings.Trim(parsed.Path, "/")
	if database != databaseName {
		return doSQLTarget{}, fmt.Errorf("dosql_database_name_mismatch: credential points to %q, request names %q", database, databaseName)
	}
	port := parsed.Port()
	if port == "" {
		port = "1433"
	}
	query := parsed.Query()
	query.Set("database", database)
	dsn := url.URL{Scheme: "sqlserver", User: parsed.User, Host: net.JoinHostPort(parsed.Hostname(), port), RawQuery: query.Encode()}
	return doSQLTarget{Engine: "mssql", Driver: "sqlserver", DSN: dsn.String(), Database: database, Address: dsn.Host}, nil
}

func doSQLURITarget(engine, driver, databaseName, raw string, schemes []string, defaultPort string) (doSQLTarget, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" || !containsDoSQLString(schemes, parsed.Scheme) {
		return doSQLTarget{}, fmt.Errorf("dosql_connection_uri_invalid: %s credential needs connection_uri %s://user:password@host:port/database", engine, schemes[0])
	}
	database := strings.Trim(parsed.Path, "/")
	if database != databaseName {
		return doSQLTarget{}, fmt.Errorf("dosql_database_name_mismatch: credential points to %q, request names %q", database, databaseName)
	}
	port := parsed.Port()
	if port == "" {
		port = defaultPort
	}
	return doSQLTarget{
		Engine: engine, Driver: driver, DSN: parsed.String(), Database: database,
		Address: net.JoinHostPort(parsed.Hostname(), port),
	}, nil
}

func doSQLMySQLTarget(databaseName, raw, prefix, user, password string) (doSQLTarget, error) {
	if prefix != "" && !strings.HasPrefix(raw, prefix) {
		return doSQLTarget{}, errors.New("dosql_connection_uri_invalid: mysql credential needs SPRING_DATASOURCE_URL jdbc:mysql://host:port/database or connection_uri mysql://user:password@host:port/database")
	}
	if prefix == "" {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			return doSQLTarget{}, errors.New("dosql_connection_uri_invalid: mysql host is missing")
		}
		if engine, ok := doSQLEngineForScheme(parsed.Scheme); !ok || engine != "mysql" {
			return doSQLTarget{}, errors.New("dosql_connection_uri_invalid: mysql credential needs connection_uri mysql://user:password@host:port/database")
		}
		raw, prefix = "mysql://"+parsed.Host+parsed.RequestURI(), "mysql://"
		if parsed.User != nil {
			raw = "mysql://" + parsed.User.String() + "@" + parsed.Host + parsed.RequestURI()
		}
	}
	parsed, err := url.Parse("mysql://" + strings.TrimPrefix(raw, prefix))
	if err != nil || parsed.Hostname() == "" {
		return doSQLTarget{}, errors.New("dosql_connection_uri_invalid: mysql host is missing")
	}
	if parsed.User != nil {
		user = parsed.User.Username()
		password, _ = parsed.User.Password()
	}
	database := strings.Trim(parsed.Path, "/")
	if database != databaseName {
		return doSQLTarget{}, fmt.Errorf("dosql_database_name_mismatch: credential points to %q, request names %q", database, databaseName)
	}
	port := parsed.Port()
	if port == "" {
		port = "3306"
	}
	config := mysql.NewConfig()
	config.User, config.Passwd = user, password
	config.Net, config.Addr, config.DBName = "tcp", net.JoinHostPort(parsed.Hostname(), port), database
	config.Timeout, config.MultiStatements = 10*time.Second, false
	return doSQLTarget{Engine: "mysql", Driver: "mysql", DSN: config.FormatDSN(), Database: database, Address: config.Addr}, nil
}
