package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	hash := tokenFastHash(plain)
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
	_, err := s.db.Exec(`INSERT INTO tokens
		(id, kind, user_id, name, prefix, hash, cluster, instance, expires_at, revoked_at, created_at, issued_via)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?)`,
		id, string(req.Kind), req.UserID, name, prefix, hash,
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
	if err := s.verifyTokenHash(token, tokenID, hash); err != nil {
		return TokenAuth{}, err
	}
	return classifyTokenRecord(TokenAuth{TokenID: id, UserID: userID, Kind: kind, Cluster: cluster, Instance: instance}, expires, revoked, disabled, time.Now().UTC())
}

// tokenFastHashPrefix marks the post-S-04 stored form of tokens.hash. The
// prefix keeps the two generations distinguishable forever, which is what makes
// the lazy upgrade possible without a migration script.
const tokenFastHashPrefix = "sha256:"

// tokenFastHashHexLength is the hex width of the digest tokenFastHash writes.
const tokenFastHashHexLength = 2 * sha256.Size

// tokenFastHashMaxLength is the longest stored form this generation can produce:
// prefix plus digest. Storage widths derive from it rather than from the literal
// 71, so changing the prefix or the digest size moves the schema guards with it.
const tokenFastHashMaxLength = len(tokenFastHashPrefix) + tokenFastHashHexLength

// tokenFastHash is the stored form for newly issued tokens: sha256 over the
// full plaintext, 128-bit random secret included. bcrypt bought nothing here -
// there is no dictionary to guess from a random 128-bit secret - and cost every
// request 50-100 ms.
func tokenFastHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return tokenFastHashPrefix + hex.EncodeToString(sum[:])
}

func isTokenFastHash(hash string) bool { return strings.HasPrefix(hash, tokenFastHashPrefix) }

// isLegacyTokenHash reports whether hash is one of the bcrypt forms issued
// before S-04.
func isLegacyTokenHash(hash string) bool {
	return strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2y$")
}

// verifyTokenHash checks token against the stored hash. A legacy bcrypt row is
// verified with bcrypt and then rewritten in the fast form in this same request;
// a failed comparison never rewrites anything. Revocation, expiry and
// disabled-user decisions stay in classifyTokenRecord and are still read from
// the database on every request.
func (s *GatewayStore) verifyTokenHash(token, tokenID, hash string) error {
	switch {
	case isTokenFastHash(hash):
		if !hmac.Equal([]byte(hash), []byte(tokenFastHash(token))) {
			return authFailure(AuthFailureInvalid)
		}
		return nil
	case isLegacyTokenHash(hash):
		if bcrypt.CompareHashAndPassword([]byte(hash), tokenPassword(token)) != nil {
			return authFailure(AuthFailureInvalid)
		}
		s.upgradeTokenHash(tokenID, hash, tokenFastHash(token))
		return nil
	default:
		return authFailure(AuthFailureInvalid)
	}
}

// upgradeTokenHash rewrites one legacy hash in place and returns the rows it
// actually changed. The hash predicate makes it a compare-and-set: a concurrent
// request that already upgraded the row leaves this update at zero rows, so no
// writer can put a stale hash back. The error is deliberately not fatal - the
// request has already authenticated, the legacy hash stays verifiable, and the
// next request retries the upgrade.
func (s *GatewayStore) upgradeTokenHash(tokenID, legacyHash, fastHash string) int64 {
	res, err := s.db.Exec(`UPDATE tokens SET hash = ? WHERE id = ? AND hash = ?`, fastHash, tokenID, legacyHash)
	if err != nil {
		return 0
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0
	}
	return affected
}

// enrollmentHashTable names one of the two enrollment tables whose hash column
// can be lazily upgraded from bcrypt to the fast form. It is a closed set: each
// value maps to a literal statement in upgradeEnrollmentHash, so no table name
// ever reaches SQL from request input.
type enrollmentHashTable string

const (
	agentEnrollmentHashTable enrollmentHashTable = "agent_enrollments"
	edgeEnrollmentHashTable  enrollmentHashTable = "edge_enrollments"
)

// upgradeEnrollmentHash rewrites one legacy enrollment hash in place and returns
// the rows it actually changed, using the same compare-and-set shape as
// upgradeTokenHash: the hash predicate means a concurrent request that already
// upgraded the row leaves this update at zero rows, so no writer can put a
// stale hash back. A failed comparison never reaches this function. The error is
// deliberately not fatal - the request has already authenticated, the legacy
// hash stays verifiable, and the next request retries the upgrade.
func (s *GatewayStore) upgradeEnrollmentHash(table enrollmentHashTable, id, legacyHash, fastHash string) int64 {
	var res sql.Result
	var err error
	switch table {
	case agentEnrollmentHashTable:
		res, err = s.db.Exec(`UPDATE agent_enrollments SET hash = ? WHERE id = ? AND hash = ?`, fastHash, id, legacyHash)
	case edgeEnrollmentHashTable:
		res, err = s.db.Exec(`UPDATE edge_enrollments SET hash = ? WHERE id = ? AND hash = ?`, fastHash, id, legacyHash)
	default:
		return 0
	}
	if err != nil {
		return 0
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0
	}
	return affected
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
