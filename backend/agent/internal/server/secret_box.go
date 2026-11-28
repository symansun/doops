package server

import (
	"context"
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
	"log"
	"os"
	"strings"
	"time"
)

// The primary key is the only key that encrypts. The previous key is a
// read-only window that lets a rotation finish without losing access to payloads
// that have not been rewritten yet. Both are derived exactly like the original
// single key, so no stored envelope changes shape.
const (
	secretKeyEnv         = "DOOPS_GATEWAY_SECRET_KEY"
	previousSecretKeyEnv = "DOOPS_GATEWAY_SECRET_KEY_PREVIOUS"

	secretEnvelopePrefixV1 = "v1:"
	secretEnvelopePrefixV2 = "v2:"
)

var (
	// ErrSecretKeyMismatch means DOOPS_GATEWAY_SECRET_KEY is not the key that
	// encrypted the stored credential payloads. Writes must stop: encrypting
	// with a different key still succeeds and silently mixes keys.
	ErrSecretKeyMismatch = errors.New("DOOPS_GATEWAY_SECRET_KEY does not match the stored credential payloads")
	// ErrSecretKeyConflict means DOOPS_GATEWAY_SECRET_KEY and
	// DOOPS_GATEWAY_SECRET_KEY_PREVIOUS hold the same value. A rotation window
	// whose two keys are equal would hide a lost key, so it is refused before a
	// single payload is touched.
	ErrSecretKeyConflict = errors.New("gateway_secret_key_conflict: DOOPS_GATEWAY_SECRET_KEY and DOOPS_GATEWAY_SECRET_KEY_PREVIOUS are the same key")
	// ErrCredentialCiphertextInvalid is returned for an envelope that cannot be
	// decoded or opened. It never carries AEAD text or key material.
	ErrCredentialCiphertextInvalid = errors.New("credential ciphertext could not be decrypted")
)

// secretRowQueryer is the single-row read surface shared by a store handle and
// a transaction.
type secretRowQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func (s *GatewayStore) encryptSecret(plaintext string) (string, error) {
	return s.encryptSecretEnvelope(plaintext, nil, secretEnvelopePrefixV1)
}

func (s *GatewayStore) decryptSecret(ciphertext string) (string, error) {
	return s.decryptSecretEnvelope(ciphertext, nil, secretEnvelopePrefixV1)
}

func (s *GatewayStore) encryptSecretWithAAD(plaintext string, aad []byte) (string, error) {
	return s.encryptSecretEnvelope(plaintext, aad, secretEnvelopePrefixV2)
}

func (s *GatewayStore) decryptSecretWithAAD(ciphertext string, aad []byte) (string, error) {
	return s.decryptSecretEnvelope(ciphertext, aad, secretEnvelopePrefixV2)
}

func (s *GatewayStore) encryptSecretEnvelope(plaintext string, aad []byte, prefix string) (string, error) {
	if strings.TrimSpace(prefix) == "" {
		return "", errors.New("credential envelope prefix is required")
	}
	gcm, err := s.secretGCM()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("%w: nonce", ErrCredentialCiphertextInvalid)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), aad)
	return prefix + base64.RawStdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

// decryptSecretEnvelope opens a stored envelope with the primary key and, while
// a rotation window is configured, with the previous key. The previous key
// never encrypts; it only keeps payloads readable until RotateSecretKey
// rewrites them.
func (s *GatewayStore) decryptSecretEnvelope(ciphertext string, aad []byte, prefix string) (string, error) {
	raw, err := decodeSecretEnvelope(ciphertext, prefix)
	if err != nil || raw == nil {
		return "", err
	}
	ring, err := s.secretKeyRing()
	if err != nil {
		return "", err
	}
	plain, primaryErr := openSecretEnvelopeRaw(ring.primary, raw, aad)
	if primaryErr == nil {
		return plain, nil
	}
	if ring.previous != nil {
		if plain, previousErr := openSecretEnvelopeRaw(ring.previous, raw, aad); previousErr == nil {
			return plain, nil
		}
	}
	return "", primaryErr
}

// decodeSecretEnvelope checks the prefix and decodes the base64 body. An empty
// stored value yields (nil, nil): callers keep treating it as "no payload".
func decodeSecretEnvelope(ciphertext, prefix string) ([]byte, error) {
	ciphertext = strings.TrimSpace(ciphertext)
	if ciphertext == "" {
		return nil, nil
	}
	if strings.TrimSpace(prefix) == "" {
		return nil, errors.New("credential envelope prefix is required")
	}
	if !strings.HasPrefix(ciphertext, prefix) {
		return nil, fmt.Errorf("%w: unexpected envelope prefix", ErrCredentialCiphertextInvalid)
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(ciphertext, prefix))
	if err != nil {
		return nil, fmt.Errorf("%w: malformed envelope", ErrCredentialCiphertextInvalid)
	}
	return raw, nil
}

// openSecretEnvelopeRaw opens one decoded envelope with exactly one key. It
// returns an error wrapping ErrCredentialCiphertextInvalid when the key is
// wrong, the AAD differs, or the envelope is truncated.
func openSecretEnvelopeRaw(key, raw, aad []byte) (string, error) {
	gcm, err := secretGCMFor(key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("%w: truncated envelope", ErrCredentialCiphertextInvalid)
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], aad)
	if err != nil {
		return "", ErrCredentialCiphertextInvalid
	}
	return string(plain), nil
}

// secretKeyRing is the primary key plus the optional previous key of a rotation
// window.
type secretKeyRing struct {
	primary  []byte
	previous []byte
}

func (s *GatewayStore) secretKeyRing() (secretKeyRing, error) {
	primary, err := s.secretKey()
	if err != nil {
		return secretKeyRing{}, err
	}
	rawPrevious := strings.TrimSpace(os.Getenv(previousSecretKeyEnv))
	if rawPrevious == "" {
		return secretKeyRing{primary: primary}, nil
	}
	if rawPrevious == strings.TrimSpace(os.Getenv(secretKeyEnv)) {
		return secretKeyRing{}, ErrSecretKeyConflict
	}
	sum := sha256.Sum256([]byte(rawPrevious))
	return secretKeyRing{primary: primary, previous: sum[:]}, nil
}

func (s *GatewayStore) secretGCM() (cipher.AEAD, error) {
	key, err := s.secretKey()
	if err != nil {
		return nil, err
	}
	return secretGCMFor(key)
}

func secretGCMFor(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: cipher", ErrCredentialCiphertextInvalid)
	}
	return cipher.NewGCM(block)
}

// secretKey derives the writer key from the environment on every call. The
// derivation has not changed: sha256 over the trimmed configured value.
func (s *GatewayStore) secretKey() ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(secretKeyEnv))
	if raw == "" {
		return nil, ErrSecretKeyUnavailable
	}
	sum := sha256.Sum256([]byte(raw))
	return sum[:], nil
}

func (s *GatewayStore) secretKeyMAC() (string, error) {
	key, err := s.secretKey()
	if err != nil {
		return "", err
	}
	return secretKeyMACValue(key), nil
}

func secretKeyMACValue(key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("doops/gateway-secret-key-check/v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

// GatewaySecretKeyState reports what VerifySecretKey established about the
// configured key and the stored payloads.
type GatewaySecretKeyState int

const (
	// GatewaySecretKeyReady means the key marker matches the writer key.
	GatewaySecretKeyReady GatewaySecretKeyState = iota
	// GatewaySecretKeyAbsent means no key is configured and no payload needs one.
	GatewaySecretKeyAbsent
	// GatewaySecretKeyRotationPending means the marker still names the previous
	// key: reads work through the window, but RotateSecretKey has not finished.
	GatewaySecretKeyRotationPending
)

// secretKeyMarker is the single gateway_secret_key_check row.
type secretKeyMarker struct {
	present bool
	mac     string
}

func readSecretKeyMarker(q secretRowQueryer) (secretKeyMarker, error) {
	var marker secretKeyMarker
	err := q.QueryRow(`SELECT key_mac FROM gateway_secret_key_check WHERE singleton = 1`).Scan(&marker.mac)
	if errors.Is(err, sql.ErrNoRows) {
		return secretKeyMarker{}, nil
	}
	if err != nil {
		return secretKeyMarker{}, err
	}
	marker.present = true
	return marker, nil
}

func (m secretKeyMarker) matches(key []byte) bool {
	return m.present && key != nil && hmac.Equal([]byte(m.mac), []byte(secretKeyMACValue(key)))
}

// VerifySecretKey is the startup gate. It fails closed when the marker matches
// neither configured key, and reports GatewaySecretKeyRotationPending when the
// marker still names the previous key.
func (s *GatewayStore) VerifySecretKey() (GatewaySecretKeyState, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return GatewaySecretKeyAbsent, err
	}
	defer tx.Rollback()

	ring, ringErr := s.secretKeyRing()
	if ringErr != nil && !errors.Is(ringErr, ErrSecretKeyUnavailable) {
		return GatewaySecretKeyAbsent, ringErr
	}
	marker, err := readSecretKeyMarker(tx)
	if err != nil {
		return GatewaySecretKeyAbsent, err
	}
	if marker.present {
		if ringErr != nil {
			return GatewaySecretKeyAbsent, ErrSecretKeyUnavailable
		}
		if marker.matches(ring.primary) {
			return GatewaySecretKeyReady, nil
		}
		if marker.matches(ring.previous) {
			// Finish the transaction before counting: the SQLite store holds a
			// single connection and the count reads through it.
			if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				return GatewaySecretKeyAbsent, err
			}
			pending, err := s.countSecretPayloadsRequiringPrevious(context.Background(), false)
			if err != nil {
				return GatewaySecretKeyAbsent, err
			}
			log.Printf("[gateway] secret key rotation pending: previous key still required for %d stored payloads", pending)
			return GatewaySecretKeyRotationPending, nil
		}
		return GatewaySecretKeyAbsent, ErrSecretKeyMismatch
	}
	if ringErr != nil {
		var hasCiphertext bool
		err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM credential_versions WHERE payload_ciphertext <> '')`).Scan(&hasCiphertext)
		if err != nil {
			return GatewaySecretKeyAbsent, err
		}
		if hasCiphertext {
			return GatewaySecretKeyAbsent, ErrSecretKeyUnavailable
		}
		return GatewaySecretKeyAbsent, nil
	}
	if err := s.proveSecretKey(tx); err != nil {
		return GatewaySecretKeyAbsent, err
	}
	if err := s.writeSecretKeyMarker(tx); err != nil {
		return GatewaySecretKeyAbsent, err
	}
	if err := tx.Commit(); err != nil {
		return GatewaySecretKeyAbsent, err
	}
	return GatewaySecretKeyReady, nil
}

func (s *GatewayStore) writeSecretKeyMarker(tx *sql.Tx) error {
	mac, err := s.secretKeyMAC()
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO gateway_secret_key_check(singleton, key_mac, created_at) VALUES(1, ?, ?)`, mac, formatTime(time.Now().UTC()))
	return err
}

// proveSecretKey proves the writer key against one stored payload before the
// marker is created. Only the primary key may prove itself here: the marker must
// name the key that writes, never the read-only window key.
func (s *GatewayStore) proveSecretKey(tx *sql.Tx) error {
	key, err := s.secretKey()
	if err != nil {
		return err
	}
	var ciphertext, credentialID, versionID, credentialType, digest string
	err = tx.QueryRow(`SELECT v.payload_ciphertext, c.id, v.id, c.type, v.payload_digest
		FROM credential_versions v JOIN credentials c ON c.id = v.credential_id
		WHERE v.payload_ciphertext <> '' AND c.scope <> ?
		ORDER BY v.created_at DESC, v.id DESC LIMIT 1`, machineVaultScope).Scan(&ciphertext, &credentialID, &versionID, &credentialType, &digest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		aad := credentialPayloadAAD(credentialID, versionID, CredentialType(credentialType), digest)
		plain, err := openSecretEnvelopeWithKey(key, ciphertext, aad)
		if err != nil {
			return ErrSecretKeyMismatch
		}
		discardSecret(plain)
		return nil
	}
	// No ordinary payload exists: a machine-login-only vault is proved through
	// its own marker, which only accepts the writer key.
	err = tx.QueryRow(`SELECT v.payload_ciphertext FROM credential_versions v JOIN credentials c ON c.id = v.credential_id
		WHERE v.payload_ciphertext <> '' AND c.scope = ? LIMIT 1`, machineVaultScope).Scan(&ciphertext)
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

// openSecretEnvelopeWithKey opens one stored envelope with exactly the given
// key, for callers that must know which key matched. The envelope prefix, not
// the caller, decides whether an AAD is bound: a v1 row predates AAD and must
// still prove the key without one.
func openSecretEnvelopeWithKey(key []byte, ciphertext string, aad []byte) (string, error) {
	prefix := secretEnvelopePrefixV2
	if hasSecretEnvelopePrefix(strings.TrimSpace(ciphertext), secretEnvelopePrefixV1) {
		prefix = secretEnvelopePrefixV1
		aad = nil
	}
	raw, err := decodeSecretEnvelope(ciphertext, prefix)
	if err != nil || raw == nil {
		return "", err
	}
	return openSecretEnvelopeRaw(key, raw, aad)
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
