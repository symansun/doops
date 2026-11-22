package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

var (
	// ErrSecretKeyMismatch means DOOPS_GATEWAY_SECRET_KEY is not the key that
	// encrypted the stored credential payloads. Writes must stop: encrypting
	// with a different key still succeeds and silently mixes keys.
	ErrSecretKeyMismatch = errors.New("DOOPS_GATEWAY_SECRET_KEY does not match the stored credential payloads")
	// ErrCredentialCiphertextInvalid means the key was available but an
	// envelope could not be opened: a key mismatch, a tampered row, or data
	// written by an older envelope format.
	ErrCredentialCiphertextInvalid = errors.New("credential ciphertext could not be decrypted")
)

func (s *GatewayStore) encryptSecret(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	return s.encryptSecretEnvelope(plaintext, nil, "v1:")
}

func (s *GatewayStore) decryptSecret(ciphertext string) (string, error) {
	return s.decryptSecretEnvelope(ciphertext, nil, "v1:")
}

func (s *GatewayStore) encryptSecretWithAAD(plaintext string, aad []byte) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	return s.encryptSecretEnvelope(plaintext, aad, "v2:")
}

func (s *GatewayStore) decryptSecretWithAAD(ciphertext string, aad []byte) (string, error) {
	return s.decryptSecretEnvelope(ciphertext, aad, "v2:")
}

func (s *GatewayStore) encryptSecretEnvelope(plaintext string, aad []byte, prefix string) (string, error) {
	gcm, err := s.secretGCM()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), aad)
	raw := append(nonce, sealed...)
	return prefix + base64.RawStdEncoding.EncodeToString(raw), nil
}

func (s *GatewayStore) decryptSecretEnvelope(ciphertext string, aad []byte, prefix string) (string, error) {
	ciphertext = strings.TrimSpace(ciphertext)
	if ciphertext == "" {
		return "", nil
	}
	if !strings.HasPrefix(ciphertext, prefix) {
		return "", fmt.Errorf("%w: unexpected envelope prefix", ErrCredentialCiphertextInvalid)
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(ciphertext, prefix))
	if err != nil {
		return "", fmt.Errorf("%w: malformed envelope", ErrCredentialCiphertextInvalid)
	}
	// An absent key is reported as itself so callers keep the stable
	// credential_key_unavailable category.
	gcm, err := s.secretGCM()
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("%w: truncated envelope", ErrCredentialCiphertextInvalid)
	}
	nonce := raw[:gcm.NonceSize()]
	sealed := raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, sealed, aad)
	if err != nil {
		// The AEAD error is dropped so its text never carries data into a log
		// line, an HTTP body or an audit category.
		return "", ErrCredentialCiphertextInvalid
	}
	return string(plain), nil
}

func (s *GatewayStore) secretGCM() (cipher.AEAD, error) {
	key, err := s.secretKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *GatewayStore) secretKey() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv("DOOPS_GATEWAY_SECRET_KEY"))
	if raw == "" {
		return nil, ErrSecretKeyUnavailable
	}
	sum := sha256.Sum256([]byte(raw))
	return sum[:], nil
}

// secretKeyMAC binds the configured key to the gateway_secret_key_check row
// so startup can compare keys without decrypting a payload.
func (s *GatewayStore) secretKeyMAC() (string, error) {
	key, err := s.secretKey()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("doops/gateway-secret-key-check/v1"))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// GatewaySecretKeyState classifies a startup key check.
type GatewaySecretKeyState int

const (
	// GatewaySecretKeyReady: the key is verified (or absent on a database with
	// nothing encrypted). Credential features decide availability per request.
	GatewaySecretKeyReady GatewaySecretKeyState = iota
	// GatewaySecretKeyAbsent: no key is configured; the database holds no
	// payload and no marker, so credential features report
	// credential_key_unavailable until a key is configured.
	GatewaySecretKeyAbsent
)

// VerifySecretKey runs once at startup, before the Gateway listens, and is the
// only place that creates or compares the gateway_secret_key_check marker:
//
//   - marker present: the key must be present and its MAC must match;
//   - no marker, ciphertext stored: the key must open one stored row, using the
//     same AAD as the write path (or the machine-login vault key check when
//     only that vault holds ciphertext); the marker is written in the same
//     transaction;
//   - no marker, no ciphertext: the marker is written when a key is present;
//     startup without a key is allowed and credential features stay unavailable.
//
// Any failure rolls back and returns an error that names no key, ciphertext or
// payload.
func (s *GatewayStore) VerifySecretKey() (GatewaySecretKeyState, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return GatewaySecretKeyAbsent, err
	}
	defer tx.Rollback()

	_, keyErr := s.secretKey()
	hasMarker, markerMatches, err := s.checkSecretKeyMarker(tx)
	if err != nil {
		return GatewaySecretKeyAbsent, err
	}
	if hasMarker {
		if keyErr != nil {
			return GatewaySecretKeyAbsent, ErrSecretKeyUnavailable
		}
		if !markerMatches {
			return GatewaySecretKeyAbsent, ErrSecretKeyMismatch
		}
		return GatewaySecretKeyReady, nil
	}
	if keyErr != nil {
		// A database created before the marker existed may already hold rows.
		var hasCiphertext bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM credential_versions WHERE payload_ciphertext <> '')`).Scan(&hasCiphertext); err != nil {
			return GatewaySecretKeyAbsent, err
		}
		if hasCiphertext {
			return GatewaySecretKeyAbsent, ErrSecretKeyUnavailable
		}
		return GatewaySecretKeyAbsent, nil
	}
	// No marker yet: first start of this version, or an upgrade from a version
	// that wrote ciphertext without a key check. Prove the key before recording it.
	if err := s.proveSecretKey(tx); err != nil {
		return GatewaySecretKeyAbsent, err
	}
	if err := s.writeSecretKeyMarker(tx); err != nil {
		return GatewaySecretKeyAbsent, err
	}
	return GatewaySecretKeyReady, tx.Commit()
}

func (s *GatewayStore) writeSecretKeyMarker(tx *sql.Tx) error {
	mac, err := s.secretKeyMAC()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO gateway_secret_key_check(singleton,key_mac,created_at) VALUES(1,?,?)`,
		mac, formatTime(time.Now().UTC()))
	return err
}

func (s *GatewayStore) checkSecretKeyMarker(tx *sql.Tx) (present, matches bool, err error) {
	var stored string
	err = tx.QueryRow(`SELECT key_mac FROM gateway_secret_key_check WHERE singleton=1`).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	mac, err := s.secretKeyMAC()
	if err != nil {
		return true, false, nil
	}
	return true, hmac.Equal([]byte(stored), []byte(mac)), nil
}

// proveSecretKey decrypts one stored row and discards the plaintext. Ordinary
// rows use the write path's AAD; machine-login rows carry an extra AAD binding
// to their inventory account, so the vault's own key check stands in for them.
func (s *GatewayStore) proveSecretKey(tx *sql.Tx) error {
	var credentialID, versionID, credentialType, ciphertext, digest string
	err := tx.QueryRow(`SELECT c.id,v.id,c.type,v.payload_ciphertext,v.payload_digest
 FROM credential_versions v JOIN credentials c ON c.id=v.credential_id
 WHERE v.payload_ciphertext <> '' AND c.scope <> ?
 ORDER BY v.created_at DESC LIMIT 1`, machineVaultScope).Scan(
		&credentialID, &versionID, &credentialType, &ciphertext, &digest)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// No ordinary row; maybe only the machine-login vault holds payloads.
	case err != nil:
		return err
	default:
		aad := credentialPayloadAAD(credentialID, versionID, CredentialType(credentialType), digest)
		plain, openErr := s.decryptSecretWithAAD(ciphertext, aad)
		discardSecret(plain)
		if openErr == nil {
			return nil
		}
		if errors.Is(openErr, ErrSecretKeyUnavailable) {
			return openErr
		}
		return ErrSecretKeyMismatch
	}

	var machineCiphertext string
	err = tx.QueryRow(`SELECT v.payload_ciphertext FROM credential_versions v JOIN credentials c ON c.id=v.credential_id
 WHERE c.scope=? AND v.payload_ciphertext <> '' LIMIT 1`, machineVaultScope).Scan(&machineCiphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.checkMachineLoginKey(tx, false); err != nil {
		if errors.Is(err, ErrSecretKeyUnavailable) {
			return ErrSecretKeyMismatch
		}
		return err
	}
	return nil
}

func discardSecret(plain string) {
	if plain == "" {
		return
	}
	buf := []byte(plain)
	for i := range buf {
		buf[i] = 0
	}
}
