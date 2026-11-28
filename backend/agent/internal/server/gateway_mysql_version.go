package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
)

// gatewayMySQLServerVersion records the connected MySQL server version parsed
// from SELECT VERSION() (for example "5.7.44-log", "8.0.36", or "8.4.13").
// Gateway requires at least MySQL 5.7.22 (JSON_ARRAYAGG and InnoDB secondary
// indexes on VIRTUAL GENERATED columns).
type gatewayMySQLServerVersion struct {
	Major int
	Minor int
	Patch int
	Raw   string
}

func parseGatewayMySQLVersion(raw string) (gatewayMySQLServerVersion, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return gatewayMySQLServerVersion{}, errors.New("gateway_mysql_version_invalid: empty server version")
	}
	head := trimmed
	if idx := strings.IndexAny(head, "-+ "); idx >= 0 {
		head = head[:idx]
	}
	parts := strings.Split(head, ".")
	if len(parts) < 2 {
		return gatewayMySQLServerVersion{}, fmt.Errorf("gateway_mysql_version_invalid: unparsable server version %q", trimmed)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return gatewayMySQLServerVersion{}, fmt.Errorf("gateway_mysql_version_invalid: unparsable server version %q", trimmed)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return gatewayMySQLServerVersion{}, fmt.Errorf("gateway_mysql_version_invalid: unparsable server version %q", trimmed)
	}
	patch := 0
	if len(parts) >= 3 && parts[2] != "" {
		patch, err = strconv.Atoi(parts[2])
		if err != nil || patch < 0 {
			return gatewayMySQLServerVersion{}, fmt.Errorf("gateway_mysql_version_invalid: unparsable server version %q", trimmed)
		}
	}
	return gatewayMySQLServerVersion{Major: major, Minor: minor, Patch: patch, Raw: trimmed}, nil
}

// supportsGateway reports whether the MySQL server meets the minimum version
// required by the Gateway store (5.7.22+, 8.0+, or 8.4+).
func (v gatewayMySQLServerVersion) supportsGateway() bool {
	if v.Major > 5 {
		return true
	}
	return v.Major == 5 && (v.Minor > 7 || (v.Minor == 7 && v.Patch >= 22))
}

// isMySQL57 reports whether the server is in the MySQL 5.7 series, which lacks
// MySQL 8 expression indexes, WITH CTEs, JSON_TABLE, utf8mb4_0900_* collations,
// and information_schema_stats_expiry.
func (v gatewayMySQLServerVersion) isMySQL57() bool {
	return v.Major < 8
}

func gatewayMySQLQueryVersion(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (gatewayMySQLServerVersion, error) {
	var raw string
	if err := q.QueryRowContext(ctx, "SELECT VERSION()").Scan(&raw); err != nil {
		return gatewayMySQLServerVersion{}, gatewayMySQLConnectionError(err)
	}
	v, err := parseGatewayMySQLVersion(raw)
	if err != nil {
		return gatewayMySQLServerVersion{}, err
	}
	if !v.supportsGateway() {
		return v, fmt.Errorf("gateway_mysql_version_unsupported: MySQL server version %s is not supported (requires MySQL 5.7.22+ or 8.0/8.4)", v.Raw)
	}
	return v, nil
}

// gatewayMySQL57Mode tracks whether the live process MySQL store is talking to
// MySQL 5.7. It follows the same scope and Close() restore rules as
// gatewayDialect.
var gatewayMySQL57Mode atomic.Bool

func gatewayIsMySQL57() bool {
	return gatewayMySQL57Mode.Load()
}

func gatewaySetMySQL57(v bool) {
	gatewayMySQL57Mode.Store(v)
}

// gatewayMySQL57VirtualKeyColumns is the exact frozen set of VIRTUAL GENERATED
// columns added ONLY when installing the Gateway schema onto MySQL 5.7, to back
// the seven functional UNIQUE KEYs (MySQL 8.0.13+ inline expressions) and the
// two VARCHAR(253) UNIQUE KEYs (proxy_routes.host and edge_routes.hostname)
// that exceed InnoDB's 767-byte single-column key limit when
// innodb_large_prefix=OFF. Both the installer and the offline copier consult
// this exact map so no arbitrary generated column can slip past schema
// validation.
var gatewayMySQL57VirtualKeyColumns = map[string]map[string]bool{
	"asset_monitor_alerts":        {"_k_one_open": true},
	"credential_versions":         {"_k_one_active": true},
	"credential_grants":           {"_k_scope_sha256": true},
	"credential_materializations": {"_k_context_sha256": true},
	"credential_verifications":    {"_k_context_sha256": true},
	"edge_routes":                 {"_k_hostname_sha256": true},
	"inventory_resource_catalog":  {"_k_bound_sha256": true, "_k_pending_sha256": true},
	"proxy_routes":                {"_k_host_sha256": true},
}

func gatewayMySQL57VirtualKeyColumn(table, column string) bool {
	cols := gatewayMySQL57VirtualKeyColumns[table]
	return cols != nil && cols[column]
}

type gatewayMySQL57DDLReplacement struct {
	name    string
	target  string
	mysql57 string
}

// gatewayMySQL57SchemaReplacements maps each MySQL 8 functional UNIQUE KEY in
// gateway_mysql_schema.sql to an equivalent MySQL 5.7.8+ VIRTUAL GENERATED
// column + UNIQUE KEY. Every entry must match exactly once so schema drift
// fails closed at install time and in unit tests.
var gatewayMySQL57SchemaReplacements = []gatewayMySQL57DDLReplacement{
	{
		name:    "asset_monitor_one_open",
		target:  "UNIQUE KEY asset_monitor_one_open ((IF(state='open', series_id, NULL))),",
		mysql57: "_k_one_open BIGINT GENERATED ALWAYS AS (IF(state='open', series_id, NULL)) VIRTUAL,\n    UNIQUE KEY asset_monitor_one_open (_k_one_open),",
	},
	{
		name:    "idx_credential_versions_one_active",
		target:  "UNIQUE KEY idx_credential_versions_one_active ((IF(state='active', credential_id, NULL)))",
		mysql57: "_k_one_active VARCHAR(64) GENERATED ALWAYS AS (IF(state='active', credential_id, NULL)) VIRTUAL,\n    UNIQUE KEY idx_credential_versions_one_active (_k_one_active)",
	},
	{
		name:    "credential_grants_identity",
		target:  "UNIQUE KEY credential_grants_identity (credential_id, grantee_id, (SHA2(scope_json, 256)))",
		mysql57: "_k_scope_sha256 CHAR(64) GENERATED ALWAYS AS (SHA2(scope_json, 256)) VIRTUAL,\n    UNIQUE KEY credential_grants_identity (credential_id, grantee_id, _k_scope_sha256)",
	},
	{
		name:    "credential_materializations_identity",
		target:  "UNIQUE KEY credential_materializations_identity (version_id, grant_id, `use`, (SHA2(context_key, 256)))",
		mysql57: "_k_context_sha256 CHAR(64) GENERATED ALWAYS AS (SHA2(context_key, 256)) VIRTUAL,\n    UNIQUE KEY credential_materializations_identity (version_id, grant_id, `use`, _k_context_sha256)",
	},
	{
		name:    "credential_verifications_identity",
		target:  "UNIQUE KEY credential_verifications_identity (version_id, grant_id, `use`, (SHA2(context_key, 256)))",
		mysql57: "_k_context_sha256 CHAR(64) GENERATED ALWAYS AS (SHA2(context_key, 256)) VIRTUAL,\n    UNIQUE KEY credential_verifications_identity (version_id, grant_id, `use`, _k_context_sha256)",
	},
	{
		name:    "catalog_bound_identity",
		target:  "UNIQUE KEY catalog_bound_identity ((IF(cluster<>'', CONCAT(LENGTH(kind), ':', kind, LENGTH(cluster), ':', cluster, LENGTH(instance), ':', instance, identity_key), NULL))),",
		mysql57: "_k_bound_sha256 CHAR(64) GENERATED ALWAYS AS (IF(cluster<>'', SHA2(CONCAT(LENGTH(kind), ':', kind, LENGTH(cluster), ':', cluster, LENGTH(instance), ':', instance, identity_key), 256), NULL)) VIRTUAL,\n    UNIQUE KEY catalog_bound_identity (_k_bound_sha256),",
	},
	{
		name:    "catalog_pending_identity",
		target:  "UNIQUE KEY catalog_pending_identity ((IF(cluster='', CONCAT(LENGTH(kind), ':', kind, LENGTH(created_by), ':', created_by, identity_key), NULL)))",
		mysql57: "_k_pending_sha256 CHAR(64) GENERATED ALWAYS AS (IF(cluster='', SHA2(CONCAT(LENGTH(kind), ':', kind, LENGTH(created_by), ':', created_by, identity_key), 256), NULL)) VIRTUAL,\n    UNIQUE KEY catalog_pending_identity (_k_pending_sha256)",
	},
	// MySQL 5.7 instances with innodb_large_prefix=OFF (common on legacy cloud
	// RDS 5.7 parameter templates) cap each single indexed column at 767 bytes
	// (191 utf8mb4 chars). The three VARCHAR(253) indexed columns below are
	// rewritten deterministically so Gateway installs and copies cleanly without
	// requiring global RDS parameter changes.
	{
		name:    "proxy_routes_host",
		target:  "host VARCHAR(253) NOT NULL UNIQUE,",
		mysql57: "host VARCHAR(253) NOT NULL,\n    _k_host_sha256 CHAR(64) GENERATED ALWAYS AS (SHA2(host, 256)) VIRTUAL,\n    UNIQUE KEY proxy_routes_host (_k_host_sha256),",
	},
	{
		name:    "idx_edge_routes_hostname",
		target:  "UNIQUE KEY idx_edge_routes_hostname (hostname)",
		mysql57: "_k_hostname_sha256 CHAR(64) GENERATED ALWAYS AS (SHA2(hostname, 256)) VIRTUAL,\n    UNIQUE KEY idx_edge_routes_hostname (_k_hostname_sha256)",
	},
	{
		name:    "inventory_node_links_primary",
		target:  "PRIMARY KEY (cluster, instance, node_name),",
		mysql57: "PRIMARY KEY (cluster, instance, node_name(191)),",
	},
}

// gatewayMySQLSchemaSQLForVersion returns the DDL appropriate for the server
// version. MySQL 8.0/8.4 receives gateway_mysql_schema.sql byte-for-byte
// unchanged; MySQL 5.7 receives the deterministic virtual-column translation
// for the seven functional unique keys.
func gatewayMySQLSchemaSQLForVersion(v gatewayMySQLServerVersion) (string, error) {
	if !v.isMySQL57() {
		return gatewayMySQLSchemaSQL, nil
	}
	sqlText := gatewayMySQLSchemaSQL
	for _, rep := range gatewayMySQL57SchemaReplacements {
		if strings.Count(sqlText, rep.target) != 1 {
			return "", fmt.Errorf("%w: mysql57 rewrite %s did not match exactly once", errGatewayMySQLSchemaInvalid, rep.name)
		}
		sqlText = strings.Replace(sqlText, rep.target, rep.mysql57, 1)
	}
	return sqlText, nil
}
