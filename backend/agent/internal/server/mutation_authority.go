package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const mutationAuthoritySchema = "doagent.mutation-authority/v1"

var (
	processMutationAuthoritySecret string
	mutationAuthorityInitOnce      sync.Once
	mutationAuthorityInitErr       error
)

func InitializeMutationAuthority() error {
	mutationAuthorityInitOnce.Do(func() {
		secret := strings.TrimSpace(os.Getenv("DO_AGENT_MUTATION_AUTHORITY_SECRET"))
		if secret != "" && !isLowerHex(secret, 64) {
			mutationAuthorityInitErr = fmt.Errorf(
				"DO_AGENT_MUTATION_AUTHORITY_SECRET must be 64 lowercase hex characters",
			)
			return
		}
		if err := os.Unsetenv("DO_AGENT_MUTATION_AUTHORITY_SECRET"); err != nil {
			mutationAuthorityInitErr = fmt.Errorf("unset mutation authority secret: %w", err)
			return
		}
		processMutationAuthoritySecret = secret
	})
	return mutationAuthorityInitErr
}

func newMutationAuthorization(sessionID string, allowedTools []string, now time.Time) (string, int64, string, string, error) {
	if processMutationAuthoritySecret == "" {
		return "", 0, "", "", fmt.Errorf("mutation authority is not configured")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", 0, "", "", fmt.Errorf("generate mutation authority id: %w", err)
	}
	authorityID := hex.EncodeToString(random)
	issuedAt := now.Unix()
	token, digest, err := signMutationAuthorization(
		processMutationAuthoritySecret,
		sessionID,
		authorityID,
		issuedAt,
		allowedTools,
	)
	return authorityID, issuedAt, token, digest, err
}

func signMutationAuthorization(secret, sessionID, authorityID string, issuedAt int64, allowedTools []string) (string, string, error) {
	if !isLowerHex(secret, 64) {
		return "", "", fmt.Errorf("invalid mutation authority secret")
	}
	if !isLowerHex(authorityID, 64) {
		return "", "", fmt.Errorf("invalid mutation authority id")
	}
	toolsJSON, err := json.Marshal(allowedTools)
	if err != nil {
		return "", "", fmt.Errorf("encode mutation authority tools: %w", err)
	}
	var payload strings.Builder
	appendMutationAuthorityField(&payload, mutationAuthoritySchema)
	appendMutationAuthorityField(&payload, sessionID)
	appendMutationAuthorityField(&payload, authorityID)
	appendMutationAuthorityField(&payload, strconv.FormatInt(issuedAt, 10))
	appendMutationAuthorityField(&payload, string(toolsJSON))

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload.String()))
	binding := sha256.Sum256([]byte(payload.String()))
	return hex.EncodeToString(mac.Sum(nil)), "sha256:" + hex.EncodeToString(binding[:]), nil
}

func appendMutationAuthorityField(payload *strings.Builder, value string) {
	payload.WriteString(strconv.Itoa(len(value)))
	payload.WriteByte(':')
	payload.WriteString(value)
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
