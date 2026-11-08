package server

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UserSummary 用于管理后台列出用户，附带授权数量统计。
type UserSummary struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Disabled   bool      `json:"disabled"`
	HasPasswrd bool      `json:"has_password"`
	GrantCount int       `json:"grant_count"`
	IsAdmin    bool      `json:"is_admin"`
	CreatedAt  time.Time `json:"created_at"`
}

// StoredGrant 表示一条已落库的授权规则。
type StoredGrant struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"user_id"`
	UserName  string    `json:"user_name,omitempty"`
	Cluster   string    `json:"cluster"`
	Instance  string    `json:"instance"`
	CreatedAt time.Time `json:"created_at"`
}

// TokenInfo 表示一条令牌的元信息（不含明文，明文仅创建时返回一次）。
type TokenInfo struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	UserID    string     `json:"user_id,omitempty"`
	UserName  string     `json:"user_name,omitempty"`
	Name      string     `json:"name"`
	Prefix    string     `json:"prefix"`
	Cluster   string     `json:"cluster,omitempty"`
	Instance  string     `json:"instance,omitempty"`
	Revoked   bool       `json:"revoked"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// ListUsers 返回全部用户，并附带授权数量及是否具备 admin 权限。
func (s *GatewayStore) ListUsers() ([]UserSummary, error) {
	rows, err := s.db.Query(`SELECT id, name, password_hash, disabled, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []UserSummary
	for rows.Next() {
		var u UserSummary
		var passwordHash string
		var disabled int
		var created string
		if err := rows.Scan(&u.ID, &u.Name, &passwordHash, &disabled, &created); err != nil {
			return nil, err
		}
		u.Disabled = disabled != 0
		u.HasPasswrd = strings.TrimSpace(passwordHash) != ""
		u.CreatedAt, _ = parseTime(created)
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range users {
		users[i].GrantCount = s.countGrants(users[i].ID)
		users[i].IsAdmin = s.UserIsPlatformAdmin(users[i].ID)
	}
	return users, nil
}

func (s *GatewayStore) countGrants(userID string) int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM grants WHERE user_id = ?`, userID).Scan(&n)
	return n
}

// FindUserByID 按 ID 查找用户。
func (s *GatewayStore) FindUserByID(id string) (GatewayUser, error) {
	var user GatewayUser
	var created string
	err := s.db.QueryRow(`SELECT id, name, password_hash, created_at FROM users WHERE id = ?`,
		strings.TrimSpace(id)).Scan(&user.ID, &user.Name, &user.PasswordHash, &created)
	if err != nil {
		return GatewayUser{}, err
	}
	user.CreatedAt, _ = parseTime(created)
	return user, nil
}

// SetUserDisabled 启用/停用用户。
func (s *GatewayStore) SetUserDisabled(userID string, disabled bool) error {
	val := 0
	if disabled {
		val = 1
	}
	res, err := s.db.Exec(`UPDATE users SET disabled = ? WHERE id = ?`, val, strings.TrimSpace(userID))
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListGrants 返回授权规则；userID 为空时返回全部并填充用户名。
func (s *GatewayStore) ListGrants(userID string) ([]StoredGrant, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if strings.TrimSpace(userID) != "" {
		rows, err = s.db.Query(`SELECT g.id, g.user_id, u.name, g.cluster, g.instance, g.created_at
			FROM grants g LEFT JOIN users u ON u.id = g.user_id WHERE g.user_id = ? ORDER BY g.id`, strings.TrimSpace(userID))
	} else {
		rows, err = s.db.Query(`SELECT g.id, g.user_id, u.name, g.cluster, g.instance, g.created_at
			FROM grants g LEFT JOIN users u ON u.id = g.user_id ORDER BY g.id`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var grants []StoredGrant
	for rows.Next() {
		var g StoredGrant
		var name sql.NullString
		var created string
		if err := rows.Scan(&g.ID, &g.UserID, &name, &g.Cluster, &g.Instance, &created); err != nil {
			return nil, err
		}
		if name.Valid {
			g.UserName = name.String
		}
		g.CreatedAt, _ = parseTime(created)
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// DeleteGrant 删除一条授权规则。
func (s *GatewayStore) DeleteGrant(id int64) error {
	res, err := s.db.Exec(`DELETE FROM grants WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListTokens 返回令牌元信息；kind 为空时返回全部。
func (s *GatewayStore) ListTokens(kind TokenKind) ([]TokenInfo, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if strings.TrimSpace(string(kind)) != "" {
		rows, err = s.db.Query(`SELECT t.id, t.kind, t.user_id, u.name, t.name, t.prefix, t.cluster, t.instance, t.expires_at, t.revoked_at, t.created_at
			FROM tokens t LEFT JOIN users u ON u.id = t.user_id WHERE t.kind = ? ORDER BY t.created_at DESC`, string(kind))
	} else {
		rows, err = s.db.Query(`SELECT t.id, t.kind, t.user_id, u.name, t.name, t.prefix, t.cluster, t.instance, t.expires_at, t.revoked_at, t.created_at
			FROM tokens t LEFT JOIN users u ON u.id = t.user_id ORDER BY t.created_at DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tokens []TokenInfo
	for rows.Next() {
		var t TokenInfo
		var userID, userName, expires, revoked, created sql.NullString
		if err := rows.Scan(&t.ID, &t.Kind, &userID, &userName, &t.Name, &t.Prefix,
			&t.Cluster, &t.Instance, &expires, &revoked, &created); err != nil {
			return nil, err
		}
		if userID.Valid {
			t.UserID = userID.String
		}
		if userName.Valid {
			t.UserName = userName.String
		}
		if expires.Valid && expires.String != "" {
			parsed, parseErr := parseTime(expires.String)
			if parseErr != nil {
				return nil, authFailure(AuthFailureInvalidExpiry)
			}
			t.ExpiresAt = &parsed
		}
		t.Revoked = revoked.Valid && revoked.String != ""
		if created.Valid {
			t.CreatedAt, _ = parseTime(created.String)
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// CurrentUserTokenInfo returns only the authenticated token's metadata. Recheck
// the record so a revoke or account change during the request cannot publish a
// now-invalid credential as the current identity.
func (s *GatewayStore) CurrentUserTokenInfo(auth TokenAuth) (TokenInfo, bool, error) {
	if auth.Kind != TokenKindUser || auth.TokenID == "" || auth.UserID == "" {
		return TokenInfo{}, false, authFailure(AuthFailureInvalid)
	}
	var info TokenInfo
	var expires, revoked, created string
	var disabled, admin int
	err := s.db.QueryRow(`SELECT t.id, t.kind, t.user_id, u.name, t.name, t.expires_at, t.revoked_at, t.created_at, u.disabled, u.is_admin
		FROM tokens t JOIN users u ON u.id = t.user_id
		WHERE t.id = ? AND t.user_id = ? AND t.kind = ?`, auth.TokenID, auth.UserID, string(auth.Kind)).
		Scan(&info.ID, &info.Kind, &info.UserID, &info.UserName, &info.Name, &expires, &revoked, &created, &disabled, &admin)
	if errors.Is(err, sql.ErrNoRows) {
		return TokenInfo{}, false, authFailure(AuthFailureInvalid)
	}
	if err != nil {
		return TokenInfo{}, false, err
	}
	if _, err := classifyTokenRecord(auth, expires, revoked, disabled, time.Now().UTC()); err != nil {
		return TokenInfo{}, false, err
	}
	if expires != "" {
		parsed, err := parseTime(expires)
		if err != nil {
			return TokenInfo{}, false, authFailure(AuthFailureInvalidExpiry)
		}
		info.ExpiresAt = &parsed
	}
	info.CreatedAt, err = parseTime(created)
	if err != nil {
		return TokenInfo{}, false, fmt.Errorf("invalid token issuance metadata")
	}
	return info, admin == 1, nil
}

// RevokeToken 撤销一个令牌（标记 revoked_at）。
func (s *GatewayStore) RevokeToken(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("token id is required")
	}
	res, err := s.db.Exec(`UPDATE tokens SET revoked_at = ? WHERE id = ? AND revoked_at = ''`,
		formatTime(time.Now().UTC()), id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
