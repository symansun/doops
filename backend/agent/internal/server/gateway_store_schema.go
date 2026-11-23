package server

import (
	"context"
	"fmt"
	"time"
)

func (s *GatewayStore) migrate() error {
	stmts := []string{
		`PRAGMA busy_timeout=5000`,
		`PRAGMA journal_mode=WAL`,
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
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
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
	if err := s.ensureColumn("agent_status", "mode", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn("agent_status", "kubernetes_available", "INTEGER"); err != nil {
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
	} {
		if err := s.ensureColumn(column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if err := s.SeedBuiltinSystemSkills(ctx); err != nil {
		return fmt.Errorf("seed builtin system skills: %w", err)
	}
	if err := s.SeedBuiltinSystemMCPServers(ctx); err != nil {
		return fmt.Errorf("seed builtin system mcp servers: %w", err)
	}
	if _, err := s.optimizeAuditIndexes(ctx); err != nil {
		return fmt.Errorf("audit index maintenance: %w", err)
	}
	return nil
}
