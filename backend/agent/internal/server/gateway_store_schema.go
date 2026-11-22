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
			status TEXT NOT NULL DEFAULT 'offline',
			generation INTEGER NOT NULL DEFAULT 0,
			connected_at TEXT NOT NULL DEFAULT '',
			last_seen TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL,
			archived_at TEXT NOT NULL DEFAULT '',
			archived_by TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(cluster, instance)
		)`,
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
	} {
		if err := s.ensureColumn("audit_events", column.name, column.definition); err != nil {
			return err
		}
	}
	for _, column := range []struct {
		table      string
		name       string
		definition string
	}{
		{"credential_bundles", "scope", "TEXT NOT NULL DEFAULT 'personal'"},
	} {
		if err := s.ensureColumn(column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if _, err := s.optimizeAuditIndexes(ctx); err != nil {
		return fmt.Errorf("audit index maintenance: %w", err)
	}
	return nil
}
