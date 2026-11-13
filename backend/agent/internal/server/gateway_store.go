package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"golang.org/x/crypto/bcrypt"
)

type GatewayAction string

const (
	ActionAll                GatewayAction = "*"
	ActionTargetUse          GatewayAction = "target:use"
	ActionExec               GatewayAction = "exec"
	ActionAsk                GatewayAction = "ask"
	ActionRead               GatewayAction = "read"
	ActionWrite              GatewayAction = "write"
	ActionPush               GatewayAction = "push"
	ActionPull               GatewayAction = "pull"
	ActionInfo               GatewayAction = "info"
	ActionCheck              GatewayAction = "check"
	ActionClean              GatewayAction = "clean"
	ActionProxy              GatewayAction = "proxy"
	ActionAgentUpgrade       GatewayAction = "agent:upgrade"
	ActionTargetsList        GatewayAction = "targets:list"
	ActionCredentialCreate   GatewayAction = "credential:create"
	ActionCredentialMetadata GatewayAction = "credential:metadata"
	ActionCredentialGrant    GatewayAction = "credential:grant"
	ActionCredentialUse      GatewayAction = "credential:use"
	ActionCredentialVerify   GatewayAction = "credential:verify"
	ActionCredentialRotate   GatewayAction = "credential:rotate"
	ActionCredentialRevoke   GatewayAction = "credential:revoke"
	ActionCredentialAudit    GatewayAction = "credential:audit"
	ActionAdmin              GatewayAction = "admin"
	ActionEdgeAdmin          GatewayAction = "edge:admin"
	ActionEdgeView           GatewayAction = "edge:view"
)

var defaultGatewayUserActions = []GatewayAction{
	ActionTargetUse,
}

type TokenKind string

const (
	TokenKindUser TokenKind = "user"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")

	ErrAgentStatusNotFound = errors.New("instance_not_found")
	ErrAgentStatusOnline   = errors.New("instance_online")
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

type GatewayStore struct {
	db *sql.DB
}

type GatewayUser struct {
	ID           string
	Name         string
	PasswordHash string
	CreatedAt    time.Time
}

type CreateUserRequest struct {
	Name     string
	Password string
}

type CreateTokenRequest struct {
	Kind      TokenKind
	UserID    string
	Name      string
	Cluster   string
	Instance  string
	ExpiresAt time.Time
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

type ScopeGrant struct {
	Cluster  string
	Instance string
}

type AuditEvent struct {
	ID              int64
	UserID          string
	TokenID         string
	Cluster         string
	Instance        string
	CredentialID    string
	VersionID       string
	GrantID         string
	Scope           *CredentialGrantScope
	ResourceVersion string
	Digest          string
	CorrelationID   string
	Action          GatewayAction
	Session         string
	CommandSummary  string
	Status          string
	Error           string
	Tail            string
	BytesIn         int64
	BytesOut        int64
	StartedAt       time.Time
	EndedAt         time.Time
}

type AgentStatus struct {
	Cluster     string
	Instance    string
	TokenID     string
	Remote      string
	Status      string
	Generation  uint64
	ConnectedAt time.Time
	LastSeen    time.Time
	UpdatedAt   time.Time
	ArchivedAt  time.Time
	ArchivedBy  string
}

type AuditFinish struct {
	Status          string
	Error           string
	Tail            string
	CommandSummary  string
	BytesIn         int64
	BytesOut        int64
	Cluster         string
	Instance        string
	VersionID       string
	GrantID         string
	Scope           *CredentialGrantScope
	ResourceVersion string
	Digest          string
	EndedAt         time.Time
}

type AuditFilter struct {
	UserID             string
	Cluster            string
	Instance           string
	Session            string
	Action             GatewayAction
	Status             string
	Limit              int
	BeforeID           int64
	CorrelationID      string
	Since              time.Time
	Until              time.Time
	HideUpgradeObserve bool
	// VisibleToUser restricts a non-admin query to that user's own events and
	// events that reference a credential they own. Empty means no extra scope.
	VisibleToUser string
}

var errAuditTerminalConflict = errors.New("audit terminal state conflict")

func OpenGatewayStore(path string) (*GatewayStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "/var/lib/doops-gateway/gateway.db"
	}
	storeDir := filepath.Dir(path)
	_, statErr := os.Stat(storeDir)
	storeDirCreated := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !storeDirCreated {
		return nil, statErr
	}
	if err := os.MkdirAll(storeDir, 0700); err != nil {
		return nil, err
	}
	if storeDirCreated {
		if err := os.Chmod(storeDir, 0700); err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	// busy_timeout must be set on the connection before the first statement:
	// switching to WAL takes a lock that a just-closed store may still hold.
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=busy_timeout(5000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &GatewayStore{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	for _, databaseFile := range []string{path, path + "-wal", path + "-shm"} {
		if err := chmodIfExists(databaseFile, 0600); err != nil {
			db.Close()
			return nil, err
		}
	}
	trackOpenGatewayStore(store)
	return store, nil
}

func chmodIfExists(path string, mode os.FileMode) error {
	if err := os.Chmod(path, mode); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *GatewayStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	untrackOpenGatewayStore(s)
	return s.db.Close()
}

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

func (s *GatewayStore) CreateUser(name string) (GatewayUser, error) {
	return s.CreateUserWithPassword(CreateUserRequest{Name: name})
}

func (s *GatewayStore) CreateUserWithPassword(req CreateUserRequest) (GatewayUser, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return GatewayUser{}, fmt.Errorf("user name is required")
	}
	passwordHash := ""
	if strings.TrimSpace(req.Password) != "" {
		hash, err := hashPassword(req.Password)
		if err != nil {
			return GatewayUser{}, err
		}
		passwordHash = hash
	}
	user := GatewayUser{
		ID:           "usr_" + randomHex(12),
		Name:         name,
		PasswordHash: passwordHash,
		CreatedAt:    time.Now().UTC(),
	}
	_, err := s.db.Exec(`INSERT INTO users (id, name, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		user.ID, user.Name, user.PasswordHash, formatTime(user.CreatedAt))
	if err != nil {
		return GatewayUser{}, err
	}
	return user, nil
}

func (s *GatewayStore) FindUserByName(name string) (GatewayUser, error) {
	var user GatewayUser
	var created string
	err := s.db.QueryRow(`SELECT id, name, password_hash, created_at FROM users WHERE name = ? AND disabled = 0`,
		strings.TrimSpace(name)).Scan(&user.ID, &user.Name, &user.PasswordHash, &created)
	if err != nil {
		return GatewayUser{}, err
	}
	user.CreatedAt, _ = parseTime(created)
	return user, nil
}

func (s *GatewayStore) SetUserPassword(userID, password string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return fmt.Errorf("user id is required")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ? AND disabled = 0`, hash, userID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// dummyPasswordHash keeps the missing-user path on the same bcrypt cost as a
// real password check. Both outcomes return ErrUnauthorized.
var dummyPasswordHash = func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("doops-login-timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return hash
}()

func (s *GatewayStore) VerifyUserPassword(name, password string) (GatewayUser, error) {
	user, err := s.FindUserByName(name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return GatewayUser{}, err
	}
	hash := dummyPasswordHash
	stored := false
	if err == nil && user.PasswordHash != "" {
		candidate := []byte(user.PasswordHash)
		if _, costErr := bcrypt.Cost(candidate); costErr != nil {
			// A stored hash that cannot be parsed is an authentication outage.
			// Still spend a bcrypt comparison so this path is not faster than a rejection.
			_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(password))
			return GatewayUser{}, costErr
		}
		hash = candidate
		stored = true
	} else {
		user = GatewayUser{}
	}
	compareErr := bcrypt.CompareHashAndPassword(hash, []byte(password))
	if !stored || errors.Is(compareErr, bcrypt.ErrMismatchedHashAndPassword) {
		return GatewayUser{}, ErrUnauthorized
	}
	if compareErr != nil {
		return GatewayUser{}, compareErr
	}
	return user, nil
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
	_, err = s.db.Exec(`INSERT INTO tokens
		(id, kind, user_id, name, prefix, hash, cluster, instance, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, string(req.Kind), req.UserID, name, prefix, string(hash),
		strings.TrimSpace(req.Cluster), strings.TrimSpace(req.Instance), expires, formatTime(createdAt))
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
	err := s.db.QueryRow(`SELECT t.id, t.user_id, t.hash, t.cluster, t.instance, t.expires_at, t.revoked_at, u.disabled
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
	err := s.db.QueryRow(`SELECT t.user_id, t.expires_at, t.revoked_at, u.disabled
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

func (s *GatewayStore) MarkAgentOnline(agent AgentStatus) error {
	if strings.TrimSpace(agent.Cluster) == "" || strings.TrimSpace(agent.Instance) == "" {
		return fmt.Errorf("cluster and instance are required")
	}
	now := time.Now().UTC()
	if agent.ConnectedAt.IsZero() {
		agent.ConnectedAt = now
	}
	if agent.LastSeen.IsZero() {
		agent.LastSeen = now
	}
	_, err := s.db.Exec(`INSERT INTO agent_status
		(cluster, instance, token_id, remote, status, generation, connected_at, last_seen, updated_at)
		VALUES (?, ?, ?, ?, 'online', ?, ?, ?, ?)
		ON CONFLICT(cluster, instance) DO UPDATE SET
			token_id = excluded.token_id,
			remote = excluded.remote,
			status = 'online',
			generation = excluded.generation,
			connected_at = excluded.connected_at,
			last_seen = excluded.last_seen,
			updated_at = excluded.updated_at,
			archived_at = '',
			archived_by = ''`,
		agent.Cluster, agent.Instance, agent.TokenID, agent.Remote, agent.Generation,
		formatTime(agent.ConnectedAt.UTC()), formatTime(agent.LastSeen.UTC()), formatTime(now))
	return err
}

func (s *GatewayStore) MarkAgentOffline(
	cluster,
	instance string,
	generation uint64,
	lastSeen time.Time,
) error {
	now := time.Now().UTC()
	if lastSeen.IsZero() {
		lastSeen = now
	}
	_, err := s.db.Exec(`UPDATE agent_status
		SET status = 'offline', last_seen = ?, updated_at = ?
		WHERE cluster = ? AND instance = ? AND generation = ?`,
		formatTime(lastSeen.UTC()), formatTime(now), cluster, instance, generation)
	return err
}

func (s *GatewayStore) MarkAllAgentsOffline() error {
	now := time.Now().UTC()
	_, err := s.db.Exec(`UPDATE agent_status
		SET status = 'offline', updated_at = ?
		WHERE status = 'online'`,
		formatTime(now))
	return err
}

func (s *GatewayStore) TouchAgentContext(
	ctx context.Context,
	cluster,
	instance string,
	generation uint64,
	lastSeen time.Time,
) error {
	if lastSeen.IsZero() {
		lastSeen = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE agent_status
		SET last_seen = ?, updated_at = ?
		WHERE cluster = ? AND instance = ? AND generation = ? AND status = 'online'`,
		formatTime(lastSeen.UTC()), formatTime(time.Now().UTC()), cluster, instance, generation)
	return err
}

const agentStatusColumns = `cluster, instance, token_id, remote, status, generation, connected_at, last_seen, updated_at,
	archived_at, archived_by`

func scanAgentStatus(row interface{ Scan(dest ...any) error }) (AgentStatus, error) {
	var agent AgentStatus
	var connected, lastSeen, updated, archived string
	if err := row.Scan(&agent.Cluster, &agent.Instance, &agent.TokenID, &agent.Remote,
		&agent.Status, &agent.Generation, &connected, &lastSeen, &updated, &archived, &agent.ArchivedBy); err != nil {
		return AgentStatus{}, err
	}
	if connected != "" {
		agent.ConnectedAt, _ = parseTime(connected)
	}
	if lastSeen != "" {
		agent.LastSeen, _ = parseTime(lastSeen)
	}
	if updated != "" {
		agent.UpdatedAt, _ = parseTime(updated)
	}
	if archived != "" {
		agent.ArchivedAt, _ = parseTime(archived)
	}
	return agent, nil
}

// ListAgentStatus returns every instance record, archived ones included; the
// registry needs all generations and callers filter what they display.
func (s *GatewayStore) ListAgentStatus() ([]AgentStatus, error) {
	rows, err := s.db.Query(`SELECT ` + agentStatusColumns + ` FROM agent_status ORDER BY cluster, instance`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var agents []AgentStatus
	for rows.Next() {
		agent, err := scanAgentStatus(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, agent)
	}
	return agents, rows.Err()
}

func (s *GatewayStore) agentStatus(q interface {
	QueryRow(query string, args ...any) *sql.Row
}, cluster, instance string) (AgentStatus, error) {
	agent, err := scanAgentStatus(q.QueryRow(`SELECT `+agentStatusColumns+`
		FROM agent_status WHERE cluster = ? AND instance = ?`, cluster, instance))
	if errors.Is(err, sql.ErrNoRows) {
		return AgentStatus{}, ErrAgentStatusNotFound
	}
	return agent, err
}

// ArchiveAgentStatus hides an offline instance record from the default
// instance list. It only marks the record: grants, credential bindings,
// tokens and audit stay as they are, and MarkAgentOnline clears the mark.
// Archiving an archived record keeps its first archive time and actor.
func (s *GatewayStore) ArchiveAgentStatus(cluster, instance, actorID string, at time.Time) (AgentStatus, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return AgentStatus{}, err
	}
	defer tx.Rollback()
	agent, err := s.agentStatus(tx, cluster, instance)
	if err != nil {
		return AgentStatus{}, err
	}
	if agent.Status == "online" {
		return AgentStatus{}, ErrAgentStatusOnline
	}
	if agent.ArchivedAt.IsZero() {
		agent.ArchivedAt, agent.ArchivedBy = at.UTC(), actorID
		if _, err := tx.Exec(`UPDATE agent_status SET archived_at = ?, archived_by = ?
			WHERE cluster = ? AND instance = ?`,
			formatTime(agent.ArchivedAt), agent.ArchivedBy, cluster, instance); err != nil {
			return AgentStatus{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return AgentStatus{}, err
	}
	return agent, nil
}

// UnarchiveAgentStatus clears the archive mark; a record that is not archived
// is returned unchanged.
func (s *GatewayStore) UnarchiveAgentStatus(cluster, instance string) (AgentStatus, error) {
	res, err := s.db.Exec(`UPDATE agent_status SET archived_at = '', archived_by = ''
		WHERE cluster = ? AND instance = ?`, cluster, instance)
	if err != nil {
		return AgentStatus{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return AgentStatus{}, ErrAgentStatusNotFound
	}
	return s.agentStatus(s.db, cluster, instance)
}

func (s *GatewayStore) StartAudit(event AuditEvent) (int64, error) {
	if event.StartedAt.IsZero() {
		event.StartedAt = time.Now().UTC()
	}
	scopeJSON, err := credentialAuditScopeJSON(event.Scope)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`INSERT INTO audit_events
		(user_id, token_id, cluster, instance, credential_id, credential_version_id,
		 credential_grant_id, scope_json, resource_version, digest, correlation_id, action,
		 session, command_summary, status, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.UserID, event.TokenID, event.Cluster, event.Instance, event.CredentialID,
		event.VersionID, event.GrantID, scopeJSON, event.ResourceVersion, event.Digest,
		event.CorrelationID, string(event.Action),
		event.Session, event.CommandSummary, firstNonEmpty(event.Status, "running"), formatTime(event.StartedAt.UTC()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *GatewayStore) FinishAudit(id int64, finish AuditFinish) error {
	if id == 0 {
		return nil
	}
	return finishAuditWith(s.db, id, finish)
}

type auditExecer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func finishAuditWith(execer auditExecer, id int64, finish AuditFinish) error {
	if finish.EndedAt.IsZero() {
		finish.EndedAt = time.Now().UTC()
	}
	if finish.Status == "" {
		if finish.Error != "" {
			finish.Status = "error"
		} else {
			finish.Status = "success"
		}
	}
	scopeJSON, err := credentialAuditScopeJSON(finish.Scope)
	if err != nil {
		return err
	}
	result, err := execer.Exec(`UPDATE audit_events
		SET status = ?, error = ?, tail = ?, bytes_in = ?, bytes_out = ?,
			command_summary = CASE WHEN ? = '' THEN command_summary ELSE ? END,
			cluster = CASE WHEN ? = '' THEN cluster ELSE ? END,
			instance = CASE WHEN ? = '' THEN instance ELSE ? END,
			credential_version_id = CASE WHEN ? = '' THEN credential_version_id ELSE ? END,
			credential_grant_id = CASE WHEN ? = '' THEN credential_grant_id ELSE ? END,
			scope_json = CASE WHEN ? = '' THEN scope_json ELSE ? END,
			resource_version = CASE WHEN ? = '' THEN resource_version ELSE ? END,
			digest = CASE WHEN ? = '' THEN digest ELSE ? END,
			ended_at = ?
		WHERE id = ? AND status IN ('running', 'executing')`,
		finish.Status, utf8Tail(redactSecretText(finish.Error), 1200), trimTail(finish.Tail, 8192), finish.BytesIn, finish.BytesOut,
		finish.CommandSummary, finish.CommandSummary,
		finish.Cluster, finish.Cluster, finish.Instance, finish.Instance,
		finish.VersionID, finish.VersionID, finish.GrantID, finish.GrantID,
		scopeJSON, scopeJSON,
		finish.ResourceVersion, finish.ResourceVersion, finish.Digest, finish.Digest,
		formatTime(finish.EndedAt.UTC()), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		// Terminal audit state is immutable. A late completion may race with
		// recovery or an uncertainty transition, but must not overwrite it.
		querier, ok := execer.(interface {
			QueryRow(string, ...any) *sql.Row
		})
		if !ok {
			return sql.ErrNoRows
		}
		var status string
		if err := querier.QueryRow("SELECT status FROM audit_events WHERE id = ?", id).Scan(&status); err != nil {
			return err
		}
		if status == "running" || status == "executing" {
			return sql.ErrNoRows
		}
		if status != finish.Status {
			return fmt.Errorf("%w: audit-%d is %s, requested %s", errAuditTerminalConflict, id, status, finish.Status)
		}
	}
	return nil
}

func (s *GatewayStore) ListAudit(limit int) ([]AuditEvent, error) {
	return s.ListAuditFiltered(AuditFilter{Limit: limit})
}

func (s *GatewayStore) ListAuditFiltered(filter AuditFilter) ([]AuditEvent, error) {
	query, args := auditFilterQuery(filter)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		var ev AuditEvent
		var action, started, ended, scopeJSON string
		if err := rows.Scan(&ev.ID, &ev.UserID, &ev.TokenID, &ev.Cluster, &ev.Instance,
			&ev.CredentialID, &ev.VersionID, &ev.GrantID, &scopeJSON,
			&ev.ResourceVersion, &ev.Digest, &ev.CorrelationID,
			&action, &ev.Session, &ev.CommandSummary, &ev.Status, &ev.Error, &ev.Tail,
			&ev.BytesIn, &ev.BytesOut, &started, &ended); err != nil {
			return nil, err
		}
		if scopeJSON != "" {
			var scope CredentialGrantScope
			if err := json.Unmarshal([]byte(scopeJSON), &scope); err != nil {
				return nil, err
			}
			ev.Scope = &scope
		}
		ev.Action = GatewayAction(action)
		ev.StartedAt, _ = parseTime(started)
		if ended != "" {
			ev.EndedAt, _ = parseTime(ended)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// auditFilterQuery builds the ListAuditFiltered statement. audit_indexes.sql
// keeps an index for each filter combination the console sends.
func auditFilterQuery(filter AuditFilter) (string, []any) {
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	conditions := make([]string, 0, 6)
	args := make([]any, 0, 7)
	if filter.BeforeID > 0 {
		conditions = append(conditions, "id < ?")
		args = append(args, filter.BeforeID)
	}
	if v := strings.TrimSpace(filter.CorrelationID); v != "" {
		conditions = append(conditions, "correlation_id = ?")
		args = append(args, v)
	}
	if !filter.Since.IsZero() {
		conditions = append(conditions, "julianday(started_at) >= julianday(?)")
		args = append(args, formatTime(filter.Since.UTC()))
	}
	if !filter.Until.IsZero() {
		conditions = append(conditions, "julianday(started_at) < julianday(?)")
		args = append(args, formatTime(filter.Until.UTC()))
	}
	if filter.HideUpgradeObserve {
		conditions = append(conditions, "NOT (action = 'agent:upgrade' AND status = 'observing')")
	}
	if v := strings.TrimSpace(filter.UserID); v != "" {
		conditions = append(conditions, "user_id = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.Cluster); v != "" {
		conditions = append(conditions, "cluster = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.Instance); v != "" {
		conditions = append(conditions, "instance = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.Session); v != "" {
		conditions = append(conditions, "session = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(string(filter.Action)); v != "" {
		conditions = append(conditions, "action = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.Status); v != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(filter.VisibleToUser); v != "" {
		conditions = append(conditions, `(user_id = ? OR credential_id IN (SELECT id FROM credentials WHERE owner_id = ?))`)
		args = append(args, v, v)
	}
	query := `SELECT id, user_id, token_id, cluster, instance, credential_id,
			credential_version_id, credential_grant_id, scope_json,
			resource_version, digest, correlation_id, action, session,
			command_summary, status, error, tail, bytes_in, bytes_out, started_at, ended_at
		FROM audit_events`
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, filter.Limit)
	return query, args
}

func credentialAuditScopeJSON(scope *CredentialGrantScope) (string, error) {
	if scope == nil {
		return "", nil
	}
	normalized := normalizeCredentialGrantScope(*scope)
	if err := validateCredentialGrantScope(normalized); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	return string(encoded), err
}

func (s *GatewayStore) DeleteAuditBefore(before time.Time) (int64, error) {
	res, err := s.db.Exec(deleteCompletedAuditSQL, formatTime(before.UTC()))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func trimTail(s string, max int) string {
	if max <= 0 {
		return s
	}
	return utf8Tail(s, max)
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func tokenPassword(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func hashPassword(password string) (string, error) {
	if strings.TrimSpace(password) == "" {
		return "", fmt.Errorf("password is required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func (s *GatewayStore) ensureColumn(table, column, definition string) error {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}
