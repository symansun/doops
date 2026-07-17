package server

import (
	"database/sql"
)

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
