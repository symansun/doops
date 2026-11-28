package server

import (
	"database/sql"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"time"
)

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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE users SET password_hash = ? WHERE id = ? AND disabled = 0`, hash, userID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	// MySQL reports changed rows, not matched rows: a repeated identical hash
	// yields 0 for an eligible row. Re-check inside the same transaction; a
	// missing or still-disabled user keeps returning sql.ErrNoRows.
	if affected == 0 {
		var disabled int
		if err := tx.QueryRow(`SELECT disabled FROM users WHERE id = ?`, userID).Scan(&disabled); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return sql.ErrNoRows
			}
			return err
		}
		if disabled != 0 {
			return sql.ErrNoRows
		}
	}
	return tx.Commit()
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
