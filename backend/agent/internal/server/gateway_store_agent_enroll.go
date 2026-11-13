package server

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func (s *GatewayStore) migrateAgentEnrollments() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS agent_enrollments (
		id TEXT PRIMARY KEY,
		cluster TEXT NOT NULL,
		instance TEXT NOT NULL,
		prefix TEXT NOT NULL,
		hash TEXT NOT NULL,
		revoked_at TEXT NOT NULL DEFAULT '',
		confirmed_at TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`ALTER TABLE agent_enrollments ADD COLUMN confirmed_at TEXT NOT NULL DEFAULT ''`)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return err
	}
	return nil
}

// AgentEnrollment is the stored record for one agent registration credential.
// Plaintext is never stored.
type AgentEnrollment struct {
	ID        string    `json:"id"`
	Cluster   string    `json:"cluster"`
	Instance  string    `json:"instance"`
	Prefix    string    `json:"prefix"`
	Revoked   bool      `json:"revoked"`
	Enforced  bool      `json:"enforced"`
	CreatedAt time.Time `json:"created_at"`
}

// CreatedAgentEnrollment is returned once, when the credential is issued.
type CreatedAgentEnrollment struct {
	ID        string `json:"id"`
	Prefix    string `json:"prefix"`
	Plaintext string `json:"-"`
	Cluster   string `json:"cluster"`
	Instance  string `json:"instance"`
}

func (s *GatewayStore) CreateAgentEnrollment(cluster, instance string) (CreatedAgentEnrollment, error) {
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	if cluster == "" || instance == "" {
		return CreatedAgentEnrollment{}, errors.New("agent enrollment requires cluster and instance")
	}
	id := "aen_" + randomHex(12)
	prefix := "dga_aen"
	plain := prefix + "_" + id + "_" + randomHex(32)
	hash, err := bcrypt.GenerateFromPassword(tokenPassword(plain), bcrypt.DefaultCost)
	if err != nil {
		return CreatedAgentEnrollment{}, err
	}
	now := formatTime(time.Now().UTC())
	if _, err := s.db.Exec(`INSERT INTO agent_enrollments (id, cluster, instance, prefix, hash, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, id, cluster, instance, prefix, string(hash), now); err != nil {
		return CreatedAgentEnrollment{}, err
	}
	return CreatedAgentEnrollment{ID: id, Prefix: prefix, Plaintext: plain, Cluster: cluster, Instance: instance}, nil
}

// TargetHasActiveAgentEnrollment reports whether this target already accepted
// a connection carrying one of its enrollment tokens. Until then the target
// stays open and an existing Agent keeps its current registration.
func (s *GatewayStore) TargetHasActiveAgentEnrollment(cluster, instance string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM agent_enrollments
		WHERE cluster = ? AND instance = ? AND revoked_at = '' AND confirmed_at != '' LIMIT 1`,
		strings.TrimSpace(cluster), strings.TrimSpace(instance)).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ConfirmAgentEnrollment records that the Agent for this target connected with
// the issued token. Later connections to the same target must present one.
func (s *GatewayStore) ConfirmAgentEnrollment(id string) error {
	_, err := s.db.Exec(`UPDATE agent_enrollments SET confirmed_at = ?
		WHERE id = ? AND revoked_at = '' AND confirmed_at = ''`,
		formatTime(time.Now().UTC()), strings.TrimSpace(id))
	return err
}

func (s *GatewayStore) VerifyAgentEnrollment(token, cluster, instance string) (AgentEnrollment, error) {
	token = strings.TrimSpace(token)
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	if token == "" || cluster == "" || instance == "" {
		return AgentEnrollment{}, ErrUnauthorized
	}
	id := agentEnrollmentIDFromPlaintext(token)
	if id == "" {
		return AgentEnrollment{}, ErrUnauthorized
	}
	var row AgentEnrollment
	var hash, created, revoked string
	err := s.db.QueryRow(`SELECT id, cluster, instance, prefix, hash, revoked_at, created_at
		FROM agent_enrollments WHERE id = ?`, id).Scan(&row.ID, &row.Cluster, &row.Instance, &row.Prefix, &hash, &revoked, &created)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentEnrollment{}, ErrUnauthorized
		}
		return AgentEnrollment{}, err
	}
	if revoked != "" || row.Cluster != cluster || row.Instance != instance {
		return AgentEnrollment{}, ErrUnauthorized
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), tokenPassword(token)) != nil {
		return AgentEnrollment{}, ErrUnauthorized
	}
	row.CreatedAt, _ = parseTime(created)
	return row, nil
}

func (s *GatewayStore) RevokeAgentEnrollment(id string) error {
	res, err := s.db.Exec(`UPDATE agent_enrollments SET revoked_at = ? WHERE id = ? AND revoked_at = ''`,
		formatTime(time.Now().UTC()), strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *GatewayStore) ListAgentEnrollments() ([]AgentEnrollment, error) {
	rows, err := s.db.Query(`SELECT id, cluster, instance, prefix, revoked_at, confirmed_at, created_at FROM agent_enrollments ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentEnrollment
	for rows.Next() {
		var row AgentEnrollment
		var revoked, confirmed, created string
		if err := rows.Scan(&row.ID, &row.Cluster, &row.Instance, &row.Prefix, &revoked, &confirmed, &created); err != nil {
			return nil, err
		}
		row.Revoked = revoked != ""
		row.Enforced = revoked == "" && confirmed != ""
		row.CreatedAt, _ = parseTime(created)
		out = append(out, row)
	}
	return out, rows.Err()
}

func agentEnrollmentIDFromPlaintext(token string) string {
	parts := strings.Split(strings.TrimSpace(token), "_")
	if len(parts) != 5 || parts[0] != "dga" || parts[1] != "aen" || parts[2] != "aen" || parts[3] == "" || parts[4] == "" {
		return ""
	}
	return "aen_" + parts[3]
}
