package server

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"time"
)

//go:embed gateway_sqlite_asset_schema.sql
var gatewaySQLiteAssetSchemaSQL string

//go:embed gateway_sqlite_asset_monitor_upgrade.sql
var gatewaySQLiteAssetMonitorUpgradeSQL string

// gatewaySchemaBaselineVersion is the single ledger row a database carries
// until incremental migrations exist. Every store that migrate() opens is at
// this revision, whichever dialect it runs on.
const gatewaySchemaBaselineVersion = "0001_baseline"

func (s *GatewayStore) migrate() error {
	// A legacy agent_status must be upgraded by the operator before any startup DDL.
	var hasAgentStatus bool
	if err := s.db.QueryRow(gatewayTableExistsSQL(gatewayDialectSQLite), "agent_status").Scan(&hasAgentStatus); err != nil {
		return err
	}
	if hasAgentStatus {
		rows, err := s.db.Query(`SELECT mode, kubernetes_available FROM agent_status LIMIT 0`)
		if err != nil {
			return fmt.Errorf("agent_status capability preflight failed (see backend/docs/migrations/agent_status_capabilities.sqlite.sql): %w", err)
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	stmts := []string{
		`PRAGMA busy_timeout=5000`,
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE IF NOT EXISTS gateway_schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL DEFAULT '',
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS tokens (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			user_id TEXT,
			name TEXT NOT NULL,
			prefix TEXT NOT NULL,
			hash TEXT NOT NULL,
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			expires_at TEXT NOT NULL DEFAULT '',
			revoked_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			issued_via TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(user_id) REFERENCES users(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tokens_kind ON tokens(kind)`,
		`CREATE TABLE IF NOT EXISTS grants (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(user_id, cluster, instance),
			FOREIGN KEY(user_id) REFERENCES users(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_grants_user ON grants(user_id)`,
		`CREATE TABLE IF NOT EXISTS agent_status (
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			token_id TEXT NOT NULL DEFAULT '',
			remote TEXT NOT NULL DEFAULT '',
			release_commit TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'offline',
			generation INTEGER NOT NULL DEFAULT 0,
			connected_at TEXT NOT NULL DEFAULT '',
			last_seen TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			archived_at TEXT NOT NULL DEFAULT '',
			archived_by TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT '',
			kubernetes_available INTEGER,
			PRIMARY KEY(cluster, instance)
		)`,
		`CREATE TABLE IF NOT EXISTS agent_connection_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			generation INTEGER NOT NULL DEFAULT 0,
			event TEXT NOT NULL,
			release_commit TEXT NOT NULL DEFAULT '',
			remote TEXT NOT NULL DEFAULT '',
			enrolled INTEGER NOT NULL DEFAULT 0,
			occurred_at_ms INTEGER NOT NULL,
			occurred_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_conn_events_time ON agent_connection_events(occurred_at_ms DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_conn_events_target ON agent_connection_events(cluster, instance, occurred_at_ms DESC)`,
		`CREATE TABLE IF NOT EXISTS platform_load_samples (
			at_ms INTEGER PRIMARY KEY,
			online_agents INTEGER NOT NULL DEFAULT 0,
			busy_agents INTEGER NOT NULL DEFAULT 0,
			active_ops INTEGER NOT NULL DEFAULT 0,
			queued_ops INTEGER NOT NULL DEFAULT 0,
			active_sessions INTEGER NOT NULL DEFAULT 0,
			resource_locks INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS detached_tasks (
			audit_id INTEGER PRIMARY KEY,
			operation_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			session TEXT NOT NULL,
			command_summary TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT NOT NULL DEFAULT '',
			finished_at_ms INTEGER NOT NULL DEFAULT 0,
			task_id TEXT NOT NULL DEFAULT '',
			cancel_requested INTEGER NOT NULL DEFAULT 0,
			output TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_detached_tasks_user ON detached_tasks(user_id, audit_id DESC)`,
		// INT-82a: addressable executions survive a Gateway restart, so GET
		// /v1/executions/<id> answers after the process comes back. terminal is
		// the stored JSON-RPC terminal envelope, never a command line.
		`CREATE TABLE IF NOT EXISTS execution_records (
			execution_id TEXT PRIMARY KEY,
			tool TEXT NOT NULL DEFAULT '',
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			owner_id TEXT NOT NULL DEFAULT '',
			agent_boot_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'running',
			error_text TEXT NOT NULL DEFAULT '',
			terminal TEXT NOT NULL DEFAULT '',
			terminal_truncated INTEGER NOT NULL DEFAULT 0,
			terminal_bytes INTEGER NOT NULL DEFAULT 0,
			created_at_ms INTEGER NOT NULL DEFAULT 0,
			updated_at_ms INTEGER NOT NULL DEFAULT 0,
			finished_at_ms INTEGER NOT NULL DEFAULT 0,
			expires_at_ms INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_execution_records_expiry ON execution_records(expires_at_ms)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL DEFAULT '',
				token_id TEXT NOT NULL DEFAULT '',
				cluster TEXT NOT NULL DEFAULT '',
				instance TEXT NOT NULL DEFAULT '',
				credential_id TEXT NOT NULL DEFAULT '',
				credential_version_id TEXT NOT NULL DEFAULT '',
				credential_grant_id TEXT NOT NULL DEFAULT '',
				scope_json TEXT NOT NULL DEFAULT '',
				resource_version TEXT NOT NULL DEFAULT '',
				digest TEXT NOT NULL DEFAULT '',
				correlation_id TEXT NOT NULL DEFAULT '',
				action TEXT NOT NULL DEFAULT '',
			session TEXT NOT NULL DEFAULT '',
			command_summary TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'running',
			error TEXT NOT NULL DEFAULT '',
			tail TEXT NOT NULL DEFAULT '',
			bytes_in INTEGER NOT NULL DEFAULT 0,
			bytes_out INTEGER NOT NULL DEFAULT 0,
			model TEXT NOT NULL DEFAULT '',
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			started_at_ms INTEGER NOT NULL DEFAULT 0,
			started_at TEXT NOT NULL,
			ended_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS proxy_routes (
			id TEXT PRIMARY KEY,
			host TEXT NOT NULL UNIQUE,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			upstream TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_proxy_routes_target ON proxy_routes(cluster, instance, enabled)`,
		`CREATE TABLE IF NOT EXISTS credentials (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			scope TEXT NOT NULL,
			owner_id TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'active',
			created_by TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			revoked_at TEXT NOT NULL DEFAULT '',
			UNIQUE(scope, owner_id, name),
			FOREIGN KEY(owner_id) REFERENCES users(id),
			FOREIGN KEY(created_by) REFERENCES users(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_credentials_owner ON credentials(owner_id, state, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_credentials_name ON credentials(name, state)`,
		`CREATE TABLE IF NOT EXISTS credential_versions (
			id TEXT PRIMARY KEY,
			credential_id TEXT NOT NULL,
			payload_ciphertext TEXT NOT NULL,
			payload_digest TEXT NOT NULL,
			state TEXT NOT NULL,
			created_by TEXT NOT NULL,
			created_at TEXT NOT NULL,
			promoted_at TEXT NOT NULL DEFAULT '',
			revoked_at TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(credential_id) REFERENCES credentials(id),
			FOREIGN KEY(created_by) REFERENCES users(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_credential_versions_resource ON credential_versions(credential_id, created_at DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_credential_versions_one_active
			ON credential_versions(credential_id) WHERE state = 'active'`,
		`CREATE TABLE IF NOT EXISTS gateway_secret_key_check (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			key_mac TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS credential_grants (
			id TEXT PRIMARY KEY,
			credential_id TEXT NOT NULL,
			grantee_id TEXT NOT NULL,
			scope_json TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'active',
			created_by TEXT NOT NULL,
			created_at TEXT NOT NULL,
			revoked_at TEXT NOT NULL DEFAULT '',
			UNIQUE(credential_id, grantee_id, scope_json),
			FOREIGN KEY(credential_id) REFERENCES credentials(id),
			FOREIGN KEY(grantee_id) REFERENCES users(id),
			FOREIGN KEY(created_by) REFERENCES users(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_credential_grants_authorize
			ON credential_grants(grantee_id, state, credential_id)`,
		`CREATE TABLE IF NOT EXISTS credential_verifications (
				id TEXT PRIMARY KEY,
				credential_id TEXT NOT NULL,
				version_id TEXT NOT NULL,
				grant_id TEXT NOT NULL,
				use TEXT NOT NULL,
				context_key TEXT NOT NULL,
				request_json TEXT NOT NULL,
				evidence_json TEXT NOT NULL,
				status TEXT NOT NULL,
				verified_at TEXT NOT NULL,
				UNIQUE(version_id, grant_id, use, context_key),
				FOREIGN KEY(credential_id) REFERENCES credentials(id),
				FOREIGN KEY(version_id) REFERENCES credential_versions(id),
				FOREIGN KEY(grant_id) REFERENCES credential_grants(id)
			)`,
		`CREATE INDEX IF NOT EXISTS idx_credential_verifications_resource
				ON credential_verifications(credential_id, version_id, status)`,
		`CREATE TABLE IF NOT EXISTS credential_materializations (
				id TEXT PRIMARY KEY,
				credential_id TEXT NOT NULL,
				version_id TEXT NOT NULL,
				grant_id TEXT NOT NULL,
				use TEXT NOT NULL,
				context_key TEXT NOT NULL,
				request_json TEXT NOT NULL,
				evidence_json TEXT NOT NULL DEFAULT '{}',
				status TEXT NOT NULL,
				error_category TEXT NOT NULL DEFAULT '',
				created_at TEXT NOT NULL,
				updated_at TEXT NOT NULL,
				UNIQUE(version_id, grant_id, use, context_key),
				FOREIGN KEY(credential_id) REFERENCES credentials(id),
				FOREIGN KEY(version_id) REFERENCES credential_versions(id),
				FOREIGN KEY(grant_id) REFERENCES credential_grants(id)
			)`,
		`CREATE INDEX IF NOT EXISTS idx_credential_materializations_resource
				ON credential_materializations(credential_id, version_id, grant_id, status)`,
		`DROP TABLE IF EXISTS credential_run_authorizations`,
		`CREATE TABLE IF NOT EXISTS credential_bundles (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			scope TEXT NOT NULL DEFAULT 'personal',
			owner_id TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL DEFAULT 'active',
			created_by TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(owner_id, name),
			FOREIGN KEY(owner_id) REFERENCES users(id),
			FOREIGN KEY(created_by) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS credential_bundle_items (
			bundle_id TEXT NOT NULL,
			credential_id TEXT NOT NULL,
			PRIMARY KEY(bundle_id, credential_id),
			FOREIGN KEY(bundle_id) REFERENCES credential_bundles(id),
			FOREIGN KEY(credential_id) REFERENCES credentials(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_credential_bundle_items_resource ON credential_bundle_items(credential_id)`,
		// monitor_schedules.strategies_json 已退役：SF-09 起不再读写，保留以兼容既有库（老库升级/冻结导出不删列）。
		`CREATE TABLE IF NOT EXISTS monitor_schedules (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			scope_mode TEXT NOT NULL DEFAULT 'all_online',
			targets_json TEXT NOT NULL DEFAULT '[]',
			tags_json TEXT NOT NULL DEFAULT '[]',
			strategies_json TEXT NOT NULL DEFAULT '["vm_host","k8s_cluster","container_runtime","app_service","database_service"]',
			thresholds_json TEXT NOT NULL DEFAULT '{}',
			interval_seconds INTEGER NOT NULL DEFAULT 300,
			next_run_at_ms INTEGER NOT NULL DEFAULT 0,
			last_run_at_ms INTEGER NOT NULL DEFAULT 0,
			last_run_id TEXT NOT NULL DEFAULT '',
			last_status TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_monitor_schedules_due ON monitor_schedules(enabled, next_run_at_ms)`,
		`CREATE TABLE IF NOT EXISTS monitor_runs (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL DEFAULT '',
			schedule_name TEXT NOT NULL DEFAULT '',
			trigger_kind TEXT NOT NULL,
			triggered_by TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			total_targets INTEGER NOT NULL DEFAULT 0,
			total_assets INTEGER NOT NULL DEFAULT 0,
			passed_checks INTEGER NOT NULL DEFAULT 0,
			warning_checks INTEGER NOT NULL DEFAULT 0,
			failed_checks INTEGER NOT NULL DEFAULT 0,
			skipped_checks INTEGER NOT NULL DEFAULT 0,
			offline_targets INTEGER NOT NULL DEFAULT 0,
			metrics_written INTEGER NOT NULL DEFAULT 0,
			started_at_ms INTEGER NOT NULL,
			finished_at_ms INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			summary TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_monitor_runs_started ON monitor_runs(started_at_ms DESC, id DESC)`,
		`CREATE TABLE IF NOT EXISTS monitor_run_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			asset_id TEXT NOT NULL,
			asset_name TEXT NOT NULL DEFAULT '',
			asset_kind TEXT NOT NULL,
			strategy TEXT NOT NULL,
			collector_method TEXT NOT NULL,
			status TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			started_at_ms INTEGER NOT NULL,
			finished_at_ms INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0,
			metrics_json TEXT NOT NULL DEFAULT '{}',
			checks_json TEXT NOT NULL DEFAULT '[]',
			evidence_json TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_monitor_run_items_run ON monitor_run_items(run_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_monitor_run_items_target ON monitor_run_items(cluster, instance, started_at_ms DESC)`,
		`CREATE TABLE IF NOT EXISTS monitor_resource_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			asset_id TEXT NOT NULL,
			asset_name TEXT NOT NULL DEFAULT '',
			asset_kind TEXT NOT NULL,
			strategy TEXT NOT NULL,
			collector_method TEXT NOT NULL,
			collected_at_ms INTEGER NOT NULL,
			run_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'ok',
			cpu_cores REAL,
			cpu_total_cores REAL,
			cpu_percent REAL,
			memory_used_bytes REAL,
			memory_total_bytes REAL,
			memory_percent REAL,
			disk_used_bytes REAL,
			disk_total_bytes REAL,
			disk_percent REAL,
			net_bytes_per_sec REAL,
			disk_iops REAL,
			disk_latency_ms REAL,
			gpu_percent REAL,
			response_time_ms REAL,
			error_rate REAL,
			active_connections REAL,
			requests_per_sec REAL,
			uptime_seconds REAL,
			load_1m REAL,
			os_info TEXT NOT NULL DEFAULT '',
			kernel_info TEXT NOT NULL DEFAULT '',
			tags_json TEXT NOT NULL DEFAULT '[]',
			details_json TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_monitor_snapshots_latest ON monitor_resource_snapshots(cluster, instance, asset_kind, asset_id, collected_at_ms DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_monitor_snapshots_time ON monitor_resource_snapshots(collected_at_ms DESC)`,
		`CREATE TABLE IF NOT EXISTS platform_skills (
			slug TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			scope TEXT NOT NULL DEFAULT 'custom',
			category TEXT NOT NULL DEFAULT 'inspection',
			driver_mode TEXT NOT NULL DEFAULT 'agent_prompt',
			strategy_id TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			skill_md TEXT NOT NULL,
			files_json TEXT NOT NULL DEFAULT '[]',
			sha256 TEXT NOT NULL DEFAULT '',
			owner_id TEXT NOT NULL DEFAULT '',
			owner_name TEXT NOT NULL DEFAULT '',
			created_at_ms INTEGER NOT NULL DEFAULT 0,
			updated_at_ms INTEGER NOT NULL DEFAULT 0,
			last_run_at_ms INTEGER NOT NULL DEFAULT 0,
			run_count INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_skills_scope ON platform_skills(scope, category, enabled)`,
		`CREATE TABLE IF NOT EXISTS platform_mcp_servers (
			slug TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			scope TEXT NOT NULL DEFAULT 'custom',
			transport TEXT NOT NULL DEFAULT 'stdio',
			command TEXT NOT NULL DEFAULT '',
			args_json TEXT NOT NULL DEFAULT '[]',
			url TEXT NOT NULL DEFAULT '',
			env_json TEXT NOT NULL DEFAULT '{}',
			headers_json TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			auto_start INTEGER NOT NULL DEFAULT 1,
			tool_timeout_secs INTEGER NOT NULL DEFAULT 120,
			last_probe_status TEXT NOT NULL DEFAULT '',
			last_probe_error TEXT NOT NULL DEFAULT '',
			last_probe_at_ms INTEGER NOT NULL DEFAULT 0,
			last_latency_ms INTEGER NOT NULL DEFAULT 0,
			tools_json TEXT NOT NULL DEFAULT '[]',
			owner_id TEXT NOT NULL DEFAULT '',
			owner_name TEXT NOT NULL DEFAULT '',
			created_at_ms INTEGER NOT NULL DEFAULT 0,
			updated_at_ms INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_mcp_scope ON platform_mcp_servers(scope, enabled)`,
		`CREATE TABLE IF NOT EXISTS platform_plugin_config (
			id TEXT PRIMARY KEY CHECK (id = 'default'),
			scenario_id TEXT NOT NULL DEFAULT 'all-in-one',
			revision INTEGER NOT NULL DEFAULT 0,
			updated_by TEXT NOT NULL DEFAULT '',
			updated_at_ms INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS platform_plugin_overrides (
			plugin_id TEXT PRIMARY KEY,
			enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
			updated_by TEXT NOT NULL DEFAULT '',
			updated_at_ms INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS inspection_metric_points (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			collected_at_ms INTEGER NOT NULL DEFAULT 0,
			run_id TEXT NOT NULL DEFAULT '',
			schedule_id TEXT NOT NULL DEFAULT '',
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			skill_slug TEXT NOT NULL DEFAULT '',
			metric_key TEXT NOT NULL,
			metric_label TEXT NOT NULL DEFAULT '',
			value REAL NOT NULL DEFAULT 0,
			unit TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_inspection_metric_points_series ON inspection_metric_points(skill_slug, metric_key, collected_at_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_inspection_metric_points_run ON inspection_metric_points(run_id)`,
		`CREATE INDEX IF NOT EXISTS idx_inspection_metric_points_target ON inspection_metric_points(cluster, instance, collected_at_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_monitor_run_items_series ON monitor_run_items(cluster, instance, strategy)`,
		`CREATE TABLE IF NOT EXISTS platform_events (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			occurred_at_ms INTEGER NOT NULL,
			occurred_at TEXT NOT NULL,
			actor_user_id TEXT NOT NULL DEFAULT '',
			cluster TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			correlation_id TEXT NOT NULL DEFAULT '',
			body_json TEXT NOT NULL,
			dedupe_key TEXT,
			created_at TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_events_dedupe ON platform_events(dedupe_key)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_events_time ON platform_events(occurred_at_ms DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_events_type ON platform_events(type, occurred_at_ms DESC)`,
		`CREATE TABLE IF NOT EXISTS webhook_endpoints (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			url_ciphertext TEXT NOT NULL,
			url_host TEXT NOT NULL,
			url_sha256 TEXT NOT NULL,
			secret_ciphertext TEXT NOT NULL,
			previous_secret_ciphertext TEXT NOT NULL DEFAULT '',
			previous_secret_expires_at_ms INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			scope_cluster TEXT NOT NULL DEFAULT '',
			scope_instance TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_success_at_ms INTEGER NOT NULL DEFAULT 0,
			last_failure_at_ms INTEGER NOT NULL DEFAULT 0,
			last_error_category TEXT NOT NULL DEFAULT '',
			deleted_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_endpoints_active ON webhook_endpoints(enabled, deleted_at, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS webhook_subscriptions (
			endpoint_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (endpoint_id, event_type)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_subscriptions_event ON webhook_subscriptions(event_type, endpoint_id)`,
		`CREATE TABLE IF NOT EXISTS webhook_deliveries (
			id TEXT PRIMARY KEY,
			endpoint_id TEXT NOT NULL,
			event_id TEXT NOT NULL,
			status TEXT NOT NULL,
			attempt INTEGER NOT NULL DEFAULT 0,
			next_attempt_at_ms INTEGER NOT NULL,
			lease_token TEXT NOT NULL DEFAULT '',
			lease_until_ms INTEGER NOT NULL DEFAULT 0,
			last_response_status INTEGER NOT NULL DEFAULT 0,
			error_category TEXT NOT NULL DEFAULT '',
			created_at_ms INTEGER NOT NULL,
			updated_at_ms INTEGER NOT NULL,
			delivered_at_ms INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_deliveries_event ON webhook_deliveries(endpoint_id, event_id)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_due ON webhook_deliveries(status, next_attempt_at_ms)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_endpoint ON webhook_deliveries(endpoint_id, created_at_ms DESC)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	// The bootstrap rows (the migration ledger baseline and the edge route
	// revision) are seeded once, at the end of this function, from
	// gatewayStoreBootstrapRows. They used to be written here and in migrateEdge,
	// which the MySQL install path never ran; see gateway_store_bootstrap.go.
	if err := s.migrateCredentialVerifications(); err != nil {
		return err
	}
	if err := s.migrateCredentialGrantScopes(); err != nil {
		return err
	}
	if err := s.migrateCredentialBundleItems(); err != nil {
		return err
	}
	if err := s.migrateEdge(); err != nil {
		return err
	}
	if err := s.migrateAgentEnrollments(); err != nil {
		return err
	}
	// INT-87: SQLite files created before the truncation marker keep the flag at
	// 0 and the original size at 0, which reads back as "not truncated" exactly
	// like the rows written before the columns existed.
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"terminal_truncated", "INTEGER NOT NULL DEFAULT 0"},
		{"terminal_bytes", "INTEGER NOT NULL DEFAULT 0"},
		// INT-82b: a row written before this column existed reads back with an
		// empty owner, i.e. "unbound", never "some other process".
		{"agent_boot_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn("execution_records", column.name, column.definition); err != nil {
			return err
		}
	}
	if err := s.ensureColumn("users", "password_hash", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("users", "is_admin", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.migrateGrantActions(); err != nil {
		return err
	}
	if err := s.ensureColumn("tokens", "issued_via", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.backfillPasswordLoginTokens(); err != nil {
		return err
	}
	// INT-108: legacy agent tokens written before user_id became NULLable could
	// store '' instead of NULL, which violates FOREIGN KEY(user_id) REFERENCES
	// users(id) in both SQLite pragma_foreign_key_check and MySQL strict FK.
	if _, err := s.db.Exec(`UPDATE tokens SET user_id = NULL WHERE kind = 'agent' AND user_id = ''`); err != nil {
		return err
	}
	if err := s.ensureColumn("agent_status", "generation", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn("agent_status", "archived_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("agent_status", "archived_by", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("agent_status", "release_commit", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{"credential_id", "TEXT NOT NULL DEFAULT ''"},
		{"credential_version_id", "TEXT NOT NULL DEFAULT ''"},
		{"credential_grant_id", "TEXT NOT NULL DEFAULT ''"},
		{"scope_json", "TEXT NOT NULL DEFAULT ''"},
		{"resource_version", "TEXT NOT NULL DEFAULT ''"},
		{"digest", "TEXT NOT NULL DEFAULT ''"},
		{"correlation_id", "TEXT NOT NULL DEFAULT ''"},
		{"model", "TEXT NOT NULL DEFAULT ''"},
		{"input_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"output_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"total_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"started_at_ms", "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := s.ensureColumn("audit_events", column.name, column.definition); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`UPDATE audit_events
		SET started_at_ms = CAST(ROUND((julianday(started_at) - 2440587.5) * 86400000.0) AS INTEGER)
		WHERE started_at_ms = 0 AND started_at != ''`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_started_ms ON audit_events(started_at_ms DESC, id DESC)`); err != nil {
		return err
	}
	for _, column := range []struct {
		table      string
		name       string
		definition string
	}{
		{"credential_bundles", "scope", "TEXT NOT NULL DEFAULT 'personal'"},
		{"monitor_schedules", "skills_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"monitor_schedules", "prompt", "TEXT NOT NULL DEFAULT ''"},
		{"monitor_schedules", "command", "TEXT NOT NULL DEFAULT ''"},
		{"monitor_schedules", "inputs_json", "TEXT NOT NULL DEFAULT '{}'"},
		{"monitor_runs", "prompt", "TEXT NOT NULL DEFAULT ''"},
		{"monitor_runs", "command", "TEXT NOT NULL DEFAULT ''"},
		{"monitor_runs", "skills_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"monitor_run_items", "artifact_format", "TEXT NOT NULL DEFAULT ''"},
		{"monitor_run_items", "artifact_markdown", "TEXT NOT NULL DEFAULT ''"},
		{"monitor_run_items", "artifact_json", "TEXT NOT NULL DEFAULT ''"},
		{"monitor_run_items", "raw_output", "TEXT NOT NULL DEFAULT ''"},
		{"monitor_run_items", "artifact_dir", "TEXT NOT NULL DEFAULT ''"},
		{"monitor_run_items", "artifact_bytes", "INTEGER NOT NULL DEFAULT 0"},
		{"monitor_run_items", "artifact_sha256", "TEXT NOT NULL DEFAULT ''"},
		{"monitor_run_items", "artifact_truncated", "INTEGER NOT NULL DEFAULT 0"},
		{"platform_skills", "file_count", "INTEGER NOT NULL DEFAULT 0"},
		{"platform_skills", "storage_dir", "TEXT NOT NULL DEFAULT ''"},
		{"platform_skills", "ui_manifest_json", "TEXT NOT NULL DEFAULT ''"},
		{"platform_skills", "ui_manifest_error", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	// Apply the idempotent asset/inventory/monitoring/resource-sync schema inside
	// a single transaction so fresh and upgraded SQLite deployments match MySQL
	// startup parity atomically without paying per-statement WAL commits.
	assetTx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := assetTx.Exec(gatewaySQLiteAssetSchemaSQL); err != nil {
		_ = assetTx.Rollback()
		return err
	}
	if err := assetTx.Commit(); err != nil {
		return err
	}
	if err := s.ensureColumn("inventory_machines", "active", "INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1))"); err != nil {
		return err
	}
	if err := s.ensureColumn("inventory_machines", "created_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureAssetMonitorSeriesSchema(); err != nil {
		return err
	}
	// The startup seeds (bootstrap rows, built-in system skills, built-in MCP
	// servers, audit index maintenance) live in one shared list so the MySQL
	// install path runs exactly the same set; see gateway_runtime_seed.go.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if err := s.seedRuntimeDefaults(ctx); err != nil {
		return err
	}
	return nil
}

// ensureAssetMonitorSeriesSchema upgrades legacy SQLite asset_monitor_series
// tables whose CHECK constraints predate device dimensions, disk I/O,
// container/agent_host origins, or database metrics. Fresh databases already
// pass the rolled-back probe without running any DDL.
func (s *GatewayStore) ensureAssetMonitorSeriesSchema() error {
	if err := s.ensureColumn("asset_monitor_series", "device_id", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	needsUpgrade := false
	probes := [][5]string{
		{"database", "exporter", "db_tps", "", "probe-db"},
		{"machine", "agent_host", "disk_read_iops", "nvme0n1", "probe-disk"},
		{"container", "container_stats", "memory_limit", "", "probe-ctr"},
	}
	for _, p := range probes {
		_, probeErr := tx.Exec(
			`INSERT INTO asset_monitor_series(cluster,instance,namespace,type,asset_id,origin,metric,device_id) VALUES('__doops_schema_probe__','__doops_schema_probe__','',?,?,?,?,?)`,
			p[0], p[4], p[1], p[2], p[3],
		)
		if probeErr == nil {
			continue
		}
		if strings.Contains(probeErr.Error(), "CHECK constraint failed") {
			needsUpgrade = true
			break
		}
		if strings.Contains(probeErr.Error(), "UNIQUE constraint failed") {
			continue
		}
		_ = tx.Rollback()
		return probeErr
	}
	_ = tx.Rollback()
	if !needsUpgrade {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, gatewaySQLiteAssetMonitorUpgradeSQL)
	return err
}
