package server

import (
	"database/sql"
	"errors"
	"log"
	"time"
)

const (
	DefaultLoginTokenTTL         = 30 * 24 * time.Hour
	DefaultLoginTokenMaxLifetime = 90 * 24 * time.Hour
	passwordLoginTokenCap        = 10
	tokenIssuedViaPasswordLogin  = "password-login"
)

var identifiablePasswordLoginNames = []string{
	"password login",
	"doops-console",
	"doops login",
}

func loginTokenRenewLead(ttl time.Duration) time.Duration {
	lead := time.Hour
	if ttl < 2*lead {
		return ttl / 2
	}
	return lead
}

func normalizeLoginTokenLifetimes(ttl, maxLifetime time.Duration) (time.Duration, time.Duration) {
	if ttl <= 0 {
		ttl = DefaultLoginTokenTTL
	}
	if maxLifetime <= 0 {
		maxLifetime = DefaultLoginTokenMaxLifetime
	}
	if maxLifetime < ttl {
		maxLifetime = ttl
	}
	return ttl, maxLifetime
}

func (s *GatewayStore) backfillPasswordLoginTokens() error {
	if len(identifiablePasswordLoginNames) == 0 {
		return nil
	}
	placeholders := ""
	args := []any{tokenIssuedViaPasswordLogin, string(TokenKindUser)}
	for i, name := range identifiablePasswordLoginNames {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += "?"
		args = append(args, name)
	}
	// Identifiable existing password-login rows (default console/CLI labels)
	// become renewable. Custom labels stay unchanged until the user logs in again.
	_, err := s.db.Exec(`UPDATE tokens
		SET issued_via = ?
		WHERE kind = ? AND issued_via = '' AND expires_at != '' AND name IN (`+placeholders+`)`, args...)
	return err
}

func (s *GatewayStore) MaybeRenewPasswordLoginToken(auth TokenAuth, ttl, maxLifetime time.Duration, now time.Time) error {
	if auth.Kind != TokenKindUser || auth.TokenID == "" {
		return nil
	}
	ttl, maxLifetime = normalizeLoginTokenLifetimes(ttl, maxLifetime)
	now = now.UTC()
	var issuedVia, expires, created, revoked string
	err := s.db.QueryRow(`SELECT issued_via, expires_at, created_at, revoked_at
		FROM tokens WHERE id = ? AND kind = ?`, auth.TokenID, string(TokenKindUser)).
		Scan(&issuedVia, &expires, &created, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if issuedVia != tokenIssuedViaPasswordLogin || revoked != "" || expires == "" {
		return nil
	}
	exp, err := parseTime(expires)
	if err != nil {
		return err
	}
	createdAt, err := parseTime(created)
	if err != nil {
		return err
	}
	lead := loginTokenRenewLead(ttl)
	if exp.Sub(now) >= ttl-lead {
		return nil
	}
	newExp := now.Add(ttl)
	deadline := createdAt.Add(maxLifetime)
	if newExp.After(deadline) {
		newExp = deadline
	}
	if !newExp.After(exp) {
		return nil
	}
	_, err = s.db.Exec(`UPDATE tokens SET expires_at = ?
		WHERE id = ? AND issued_via = ? AND revoked_at = ''`,
		formatTime(newExp), auth.TokenID, tokenIssuedViaPasswordLogin)
	return err
}

func (s *GatewayStore) RevokeExcessPasswordLoginTokens(userID, keepTokenID string, cap int, now time.Time) (int, error) {
	if cap <= 0 || userID == "" {
		return 0, nil
	}
	now = now.UTC()
	rows, err := s.db.Query(`SELECT id, created_at FROM tokens
		WHERE user_id = ? AND kind = ? AND issued_via = ? AND revoked_at = ''
		AND (expires_at = '' OR expires_at > ?)
		ORDER BY created_at ASC, id ASC`,
		userID, string(TokenKindUser), tokenIssuedViaPasswordLogin, formatTime(now))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type row struct {
		id        string
		createdAt string
	}
	var tokens []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.id, &item.createdAt); err != nil {
			return 0, err
		}
		tokens = append(tokens, item)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(tokens) <= cap {
		return 0, nil
	}
	revokedAt := formatTime(now)
	revoked := 0
	overflow := len(tokens) - cap
	for _, item := range tokens {
		if overflow <= 0 {
			break
		}
		if item.id == keepTokenID {
			continue
		}
		res, err := s.db.Exec(`UPDATE tokens SET revoked_at = ? WHERE id = ? AND revoked_at = ''`, revokedAt, item.id)
		if err != nil {
			return revoked, err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			revoked++
			overflow--
		}
	}
	return revoked, nil
}

func (s *GatewayStore) tokenIssuedVia(id string) (string, error) {
	var issuedVia string
	err := s.db.QueryRow(`SELECT issued_via FROM tokens WHERE id = ?`, id).Scan(&issuedVia)
	return issuedVia, err
}

func logPasswordLoginRenewalError(tokenID string, err error) {
	if err != nil {
		log.Printf("[gateway] password-login token renewal failed: token_id=%s: %v", tokenID, err)
	}
}
