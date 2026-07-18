package server

import (
	"database/sql"
	"fmt"
)

func (s *GatewayStore) migrateCredentialGrantScopes() error {
	hasScope, err := s.tableHasColumn("credential_grants", "scope_json")
	if err != nil || hasScope {
		return err
	}
	var activeGrants, liveMaterializations int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM credential_grants WHERE state != 'revoked'`).Scan(&activeGrants); err != nil {
		return err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM credential_materializations WHERE status != 'cleaned'`).Scan(&liveMaterializations); err != nil {
		return err
	}
	if activeGrants != 0 || liveMaterializations != 0 {
		return fmt.Errorf("credential scope migration blocked: revoke old grants and clean materializations first")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM credential_verifications`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM credential_materializations`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP INDEX IF EXISTS idx_credential_grants_authorize`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE credential_grants`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE credential_grants (
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
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX idx_credential_grants_authorize
		ON credential_grants(grantee_id, state, credential_id)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *GatewayStore) migrateCredentialBundleItems() error {
	hasUse, err := s.tableHasColumn("credential_bundle_items", "use")
	if err != nil || !hasUse {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE credential_bundle_items_v2 (
		bundle_id TEXT NOT NULL,
		credential_id TEXT NOT NULL,
		PRIMARY KEY(bundle_id, credential_id),
		FOREIGN KEY(bundle_id) REFERENCES credential_bundles(id),
		FOREIGN KEY(credential_id) REFERENCES credentials(id)
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO credential_bundle_items_v2 (bundle_id, credential_id)
		SELECT bundle_id, credential_id FROM credential_bundle_items`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP INDEX IF EXISTS idx_credential_bundle_items_resource`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE credential_bundle_items`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE credential_bundle_items_v2 RENAME TO credential_bundle_items`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX idx_credential_bundle_items_resource
		ON credential_bundle_items(credential_id)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *GatewayStore) migrateCredentialVerifications() error {
	hasContextKey, err := s.tableHasColumn("credential_verifications", "context_key")
	if err != nil || hasContextKey {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE credential_verifications_v2 (
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
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO credential_verifications_v2
		(id, credential_id, version_id, grant_id, use, context_key, request_json, evidence_json, status, verified_at)
		SELECT id, credential_id, version_id, grant_id, use, id, request_json, evidence_json, status, verified_at
		FROM credential_verifications`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE credential_verifications`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE credential_verifications_v2 RENAME TO credential_verifications`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE INDEX idx_credential_verifications_resource
		ON credential_verifications(credential_id, version_id, status)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *GatewayStore) tableHasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
