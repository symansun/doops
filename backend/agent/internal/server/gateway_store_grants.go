package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// GrantUser lets a user operate every target matched by cluster/instance.
// "*" is the only wildcard. Repeating an existing grant is a no-op.
func (s *GatewayStore) GrantUser(userID string, grant ScopeGrant) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return fmt.Errorf("user id is required")
	}
	cluster := strings.TrimSpace(grant.Cluster)
	instance := strings.TrimSpace(grant.Instance)
	if cluster == "" {
		cluster = "*"
	}
	if instance == "" {
		instance = "*"
	}
	_, err := s.db.Exec(`INSERT INTO grants (user_id, cluster, instance, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, cluster, instance) DO NOTHING`,
		userID, cluster, instance, formatTime(time.Now().UTC()))
	return err
}

// UserCanUseTarget is the only target authorization check. Platform admins can
// use every target; other users need a grant matching cluster and instance.
func (s *GatewayStore) UserCanUseTarget(userID, cluster, instance string) bool {
	userID = strings.TrimSpace(userID)
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	if userID == "" || cluster == "" || instance == "" {
		return false
	}
	if s.UserIsPlatformAdmin(userID) {
		return true
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(1) FROM grants g JOIN users u ON u.id = g.user_id
		WHERE g.user_id = ? AND u.disabled = 0
		  AND (g.cluster = '*' OR g.cluster = ?) AND (g.instance = '*' OR g.instance = ?)`,
		userID, cluster, instance).Scan(&n)
	return err == nil && n > 0
}

func (s *GatewayStore) UserIsPlatformAdmin(userID string) bool {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false
	}
	var admin int
	err := s.db.QueryRow(`SELECT is_admin FROM users WHERE id = ? AND disabled = 0`, userID).Scan(&admin)
	return err == nil && admin == 1
}

// UserScopeActions derives execution metadata from current target authority.
// It does not restore per-action grants or accept scopes from an RPC caller.
func (s *GatewayStore) UserScopeActions(userID, cluster, instance string) ([]string, error) {
	var admin, disabled, granted int
	if err := s.db.QueryRow(`SELECT u.is_admin, u.disabled, EXISTS (
		SELECT 1 FROM grants g WHERE g.user_id = u.id
		AND (g.cluster = '*' OR g.cluster = ?) AND (g.instance = '*' OR g.instance = ?)
	) FROM users u WHERE u.id = ?`, cluster, instance, userID).Scan(&admin, &disabled, &granted); err != nil {
		return nil, err
	}
	if disabled != 0 || (admin != 1 && granted == 0) {
		return nil, ErrForbidden
	}
	if admin == 1 {
		return []string{"admin", "exec"}, nil
	}
	return []string{"exec"}, nil
}

func (s *GatewayStore) SetUserAdmin(userID string, admin bool) error {
	value := 0
	if admin {
		value = 1
	}
	res, err := s.db.Exec(`UPDATE users SET is_admin = ? WHERE id = ?`, value, strings.TrimSpace(userID))
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

// migrateGrantActions converts per-action grants into target grants. A grant
// with any recognized action becomes full use of its target; "*"/"admin" on
// */* becomes the user's admin flag. Rows that granted nothing are dropped.
func (s *GatewayStore) migrateGrantActions() error {
	legacy, err := s.tableHasColumn("grants", "actions_json")
	if err != nil || !legacy {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id, user_id, cluster, instance, actions_json, created_at FROM grants ORDER BY id`)
	if err != nil {
		return err
	}
	type legacyGrant struct {
		id                                 int64
		userID, cluster, instance, created string
	}
	var keep []legacyGrant
	admins := map[string]bool{}
	for rows.Next() {
		var g legacyGrant
		var raw string
		if err := rows.Scan(&g.id, &g.userID, &g.cluster, &g.instance, &raw, &g.created); err != nil {
			rows.Close()
			return err
		}
		var actions []string
		if json.Unmarshal([]byte(raw), &actions) != nil || len(actions) == 0 {
			continue
		}
		for _, action := range actions {
			if (action == "admin" || action == "*") && g.cluster == "*" && g.instance == "*" {
				admins[g.userID] = true
			}
		}
		keep = append(keep, g)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS grants_v2`,
		`CREATE TABLE grants_v2 (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id TEXT NOT NULL,
			cluster TEXT NOT NULL,
			instance TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(user_id, cluster, instance),
			FOREIGN KEY(user_id) REFERENCES users(id)
		)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	for _, g := range keep {
		if _, err := tx.Exec(`INSERT INTO grants_v2 (id, user_id, cluster, instance, created_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(user_id, cluster, instance) DO NOTHING`,
			g.id, g.userID, g.cluster, g.instance, g.created); err != nil {
			return err
		}
	}
	for userID := range admins {
		if _, err := tx.Exec(`UPDATE users SET is_admin = 1 WHERE id = ?`, userID); err != nil {
			return err
		}
	}
	for _, stmt := range []string{
		`DROP TABLE grants`,
		`ALTER TABLE grants_v2 RENAME TO grants`,
		`CREATE INDEX IF NOT EXISTS idx_grants_user ON grants(user_id)`,
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}
