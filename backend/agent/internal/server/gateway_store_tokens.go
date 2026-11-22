package server

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"time"
)

type TokenKind string

const (
	TokenKindUser TokenKind = "user"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
)

type AuthFailureReason string

const (
	AuthFailureMissing       AuthFailureReason = "missing"
	AuthFailureInvalid       AuthFailureReason = "invalid"
	AuthFailureExpired       AuthFailureReason = "expired"
	AuthFailureRevoked       AuthFailureReason = "revoked"
	AuthFailureUserDisabled  AuthFailureReason = "user_disabled"
	AuthFailureInvalidExpiry AuthFailureReason = "invalid_expiry"
)

// TokenAuthError keeps the authentication failure category while preserving
// errors.Is(err, ErrUnauthorized) for callers that only need the auth boundary.
type TokenAuthError struct {
	Reason AuthFailureReason
}

func (e *TokenAuthError) Error() string {
	return "gateway authentication failed: " + string(e.Reason)
}

func (e *TokenAuthError) Unwrap() error { return ErrUnauthorized }

func authFailure(reason AuthFailureReason) error { return &TokenAuthError{Reason: reason} }

type CreateTokenRequest struct {
	Kind      TokenKind
	UserID    string
	Name      string
	Cluster   string
	Instance  string
	ExpiresAt time.Time
	IssuedVia string
}

type CreatedToken struct {
	ID        string
	Prefix    string
	Plaintext string
	Name      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type TokenAuth struct {
	TokenID  string
	UserID   string
	Kind     TokenKind
	Cluster  string
	Instance string
}

func (s *GatewayStore) CreateToken(req CreateTokenRequest) (CreatedToken, error) {
	if req.Kind != TokenKindUser {
		return CreatedToken{}, fmt.Errorf("only user tokens are supported")
	}
	if strings.TrimSpace(req.UserID) == "" {
		return CreatedToken{}, fmt.Errorf("user token requires user id")
	}
	id := "tok_" + randomHex(12)
	prefix := "dgw_" + string(req.Kind) + "_" + randomHex(4)
	secret := randomHex(32)
	plain := prefix + "_" + id + "_" + secret
	hash, err := bcrypt.GenerateFromPassword(tokenPassword(plain), bcrypt.DefaultCost)
	if err != nil {
		return CreatedToken{}, err
	}
	createdAt := time.Now().UTC()
	expires := ""
	if !req.ExpiresAt.IsZero() {
		expires = formatTime(req.ExpiresAt.UTC())
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = string(req.Kind) + " token"
	}
	issuedVia := strings.TrimSpace(req.IssuedVia)
	_, err = s.db.Exec(`INSERT INTO tokens
		(id, kind, user_id, name, prefix, hash, cluster, instance, expires_at, created_at, issued_via)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, string(req.Kind), req.UserID, name, prefix, string(hash),
		strings.TrimSpace(req.Cluster), strings.TrimSpace(req.Instance), expires, formatTime(createdAt), issuedVia)
	if err != nil {
		return CreatedToken{}, err
	}
	return CreatedToken{ID: id, Prefix: prefix, Plaintext: plain, Name: name, CreatedAt: createdAt, ExpiresAt: req.ExpiresAt.UTC()}, nil
}

func (s *GatewayStore) VerifyUserToken(token string) (TokenAuth, error) {
	auth, err := s.verifyToken(token, TokenKindUser)
	if err != nil {
		return TokenAuth{}, err
	}
	return auth, nil
}

func (s *GatewayStore) verifyToken(token string, kind TokenKind) (TokenAuth, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return TokenAuth{}, authFailure(AuthFailureMissing)
	}
	if tokenID := tokenIDFromPlaintext(token); tokenID != "" {
		return s.verifyTokenByID(token, kind, tokenID)
	}
	return TokenAuth{}, authFailure(AuthFailureInvalid)
}

func (s *GatewayStore) verifyTokenByID(token string, kind TokenKind, tokenID string) (TokenAuth, error) {
	var id, userID, hash, cluster, instance, expires, revoked string
	var disabled int
	err := s.readDB().QueryRow(`SELECT t.id, t.user_id, t.hash, t.cluster, t.instance, t.expires_at, t.revoked_at, u.disabled
		FROM tokens t JOIN users u ON u.id = t.user_id
		WHERE t.id = ? AND t.kind = ?`, tokenID, string(kind)).
		Scan(&id, &userID, &hash, &cluster, &instance, &expires, &revoked, &disabled)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TokenAuth{}, authFailure(AuthFailureInvalid)
		}
		return TokenAuth{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), tokenPassword(token)) != nil {
		return TokenAuth{}, authFailure(AuthFailureInvalid)
	}
	return classifyTokenRecord(TokenAuth{TokenID: id, UserID: userID, Kind: kind, Cluster: cluster, Instance: instance}, expires, revoked, disabled, time.Now().UTC())
}

func classifyTokenRecord(auth TokenAuth, expires, revoked string, disabled int, now time.Time) (TokenAuth, error) {
	if disabled != 0 {
		return TokenAuth{}, authFailure(AuthFailureUserDisabled)
	}
	if revoked != "" {
		return TokenAuth{}, authFailure(AuthFailureRevoked)
	}
	if expires != "" {
		exp, err := parseTime(expires)
		if err != nil {
			return TokenAuth{}, authFailure(AuthFailureInvalidExpiry)
		}
		if !now.Before(exp) {
			return TokenAuth{}, authFailure(AuthFailureExpired)
		}
	}
	return auth, nil
}

// Recheck the verified connection identity without retaining its plaintext token.
func (s *GatewayStore) validateUserTokenAuth(auth TokenAuth) error {
	if auth.Kind != TokenKindUser || auth.TokenID == "" || auth.UserID == "" {
		return authFailure(AuthFailureInvalid)
	}
	var userID, expires, revoked string
	var disabled int
	err := s.readDB().QueryRow(`SELECT t.user_id, t.expires_at, t.revoked_at, u.disabled
		FROM tokens t JOIN users u ON u.id = t.user_id
		WHERE t.id = ? AND t.kind = ?`, auth.TokenID, string(auth.Kind)).Scan(&userID, &expires, &revoked, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return authFailure(AuthFailureInvalid)
	}
	if err != nil {
		return err
	}
	if userID != auth.UserID {
		return authFailure(AuthFailureInvalid)
	}
	_, err = classifyTokenRecord(auth, expires, revoked, disabled, time.Now().UTC())
	return err
}

func tokenIDFromPlaintext(token string) string {
	parts := strings.Split(strings.TrimSpace(token), "_")
	if len(parts) < 6 || parts[0] != "dgw" || parts[3] != "tok" || parts[4] == "" {
		return ""
	}
	return "tok_" + parts[4]
}

func (s *GatewayStore) DeleteExpiredTokens(now time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM tokens WHERE expires_at != '' AND expires_at <= ?`, formatTime(now.UTC()))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func tokenPassword(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
