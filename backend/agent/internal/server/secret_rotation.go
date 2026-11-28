package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const defaultSecretRotationBatchSize = 200

var (
	// ErrSecretRotationIncomplete means the self-check found stored payloads that
	// still need the previous key, so the marker was left alone. The rotation can
	// be rerun once the cause is fixed.
	ErrSecretRotationIncomplete = errors.New("secret key rotation incomplete: stored payloads still require the previous key")
	// ErrSecretRotationPayload means one stored payload could not be opened with
	// the primary or the previous key. It never carries ids, digests or
	// ciphertext.
	ErrSecretRotationPayload = errors.New("secret key rotation: stored payload cannot be decrypted with either configured key")
)

// SecretRotationOptions tunes one RotateSecretKey run. The zero value is safe.
type SecretRotationOptions struct {
	// BatchSize is the number of payload rows rewritten per transaction.
	BatchSize int
	// BeforeBatchRewrite is invoked after a batch is read and before its
	// transaction opens. A non-nil error aborts the run with that batch left
	// untouched. Tests use it to simulate a concurrent writer between the read
	// and the compare-and-set.
	BeforeBatchRewrite func(batch int) error
	// AfterBatchCommit is invoked after each committed batch. A non-nil error
	// aborts the run before the next batch and before the marker update; the
	// committed batches stay committed, which is exactly the crash shape the
	// rotation must survive.
	AfterBatchCommit func(batch int) error
	// BeforeMarkerCommit is invoked after the self-check passes and before the
	// marker transaction commits. A non-nil error leaves the marker untouched.
	BeforeMarkerCommit func() error
}

// SecretRotationReport is the audit-free summary of one rotation. It carries
// counts and table names only: no payload ids, digests, ciphertexts or
// plaintext.
type SecretRotationReport struct {
	CredentialVersions int
	MachineLoginVault  int
	Skipped            int
	AlreadyPrimary     int
}

// SecretRotationPreview is the dry-run result: how many stored payloads the next
// rotation would rewrite, split the same way the real report is. It carries
// counts only.
type SecretRotationPreview struct {
	CredentialVersions int
	MachineLoginVault  int
	AlreadyPrimary     int
}

// PreviewSecretRotation counts the payloads RotateSecretKey would rewrite without
// writing anything. It reuses the rotation's own batch readers and classifier so
// the preview and the real run cannot disagree about what needs the previous
// key.
func (s *GatewayStore) PreviewSecretRotation(ctx context.Context) (SecretRotationPreview, error) {
	var preview SecretRotationPreview
	ring, err := s.secretKeyRing()
	if err != nil {
		return preview, err
	}
	for _, source := range []struct {
		pending *int
		read    func(context.Context, string, int) ([]secretPayloadRow, error)
	}{
		{&preview.CredentialVersions, s.readCredentialPayloadBatch},
		{&preview.MachineLoginVault, s.readVaultPayloadBatch},
	} {
		lastID := ""
		for {
			rows, err := source.read(ctx, lastID, defaultSecretRotationBatchSize)
			if err != nil {
				return preview, err
			}
			if len(rows) == 0 {
				break
			}
			lastID = rows[len(rows)-1].versionID
			for _, row := range rows {
				present, requiresPrevious, err := classifySecretPayload(ring, row)
				if err != nil {
					return preview, err
				}
				if !present {
					continue
				}
				if requiresPrevious {
					*source.pending++
				} else {
					preview.AlreadyPrimary++
				}
			}
		}
	}
	return preview, nil
}

// PreviewSecretRotationAtPath runs the dry-run against a SQLite database file
// without opening the writable store: no migrate, no chmod, no journal mode
// switch, no marker. A missing file stays missing.
func PreviewSecretRotationAtPath(ctx context.Context, path string) (SecretRotationPreview, error) {
	path = strings.TrimSpace(path)
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     path,
		RawQuery: "mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)",
	}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return SecretRotationPreview{}, err
	}
	db.SetMaxOpenConns(1)
	store := &GatewayStore{db: db, dbPath: path, dialect: gatewayDialectSQLite, backend: "sqlite"}
	defer store.Close()
	gatewaySetDialect(gatewayDialectSQLite)
	return store.PreviewSecretRotation(ctx)
}

// secretPayloadRow is one stored payload plus the bindings needed to rebuild its
// AAD exactly as the reader does.
type secretPayloadRow struct {
	table          string
	versionID      string
	credentialID   string
	credentialType CredentialType
	ciphertext     string
	digest         string
	assetID        string
	username       string
}

// RotateSecretKey rewrites every stored payload with the primary key, then moves
// the key marker to the primary key in its own transaction.
//
// It is restartable: a payload that already opens with the primary key is left
// untouched, and a crash between batches only leaves a mix that the previous key
// window still reads. The marker is written last, so an interrupted run keeps
// reporting GatewaySecretKeyRotationPending at startup instead of claiming a
// rotation that did not finish.
//
// Run it while the Gateway is stopped, or accept that a concurrent Gateway may
// write a payload with the previous key while it runs: the compare-and-set
// update makes a concurrent rewrite skip (and be retried by a later run) instead
// of being overwritten, and the self-check refuses to move the marker while any
// payload still needs the previous key. Rotation is a deliberate maintenance
// action, not a hot path.
func (s *GatewayStore) RotateSecretKey(ctx context.Context, opts SecretRotationOptions) (SecretRotationReport, error) {
	var report SecretRotationReport
	ring, err := s.secretKeyRing()
	if err != nil {
		return report, err
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = defaultSecretRotationBatchSize
	}
	batch := 0
	lastID := ""
	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		rows, err := s.readCredentialPayloadBatch(ctx, lastID, batchSize)
		if err != nil {
			return report, err
		}
		if len(rows) == 0 {
			break
		}
		lastID = rows[len(rows)-1].versionID
		if opts.BeforeBatchRewrite != nil {
			if err := opts.BeforeBatchRewrite(batch + 1); err != nil {
				return report, err
			}
		}
		rewritten, skipped, already, err := s.rewriteSecretPayloadBatch(ctx, ring, rows)
		report.CredentialVersions += rewritten
		report.Skipped += skipped
		report.AlreadyPrimary += already
		if err != nil {
			return report, err
		}
		batch++
		if opts.AfterBatchCommit != nil {
			if err := opts.AfterBatchCommit(batch); err != nil {
				return report, err
			}
		}
	}
	lastID = ""
	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		rows, err := s.readVaultPayloadBatch(ctx, lastID, batchSize)
		if err != nil {
			return report, err
		}
		if len(rows) == 0 {
			break
		}
		lastID = rows[len(rows)-1].versionID
		if opts.BeforeBatchRewrite != nil {
			if err := opts.BeforeBatchRewrite(batch + 1); err != nil {
				return report, err
			}
		}
		rewritten, skipped, already, err := s.rewriteSecretPayloadBatch(ctx, ring, rows)
		report.MachineLoginVault += rewritten
		report.Skipped += skipped
		report.AlreadyPrimary += already
		if err != nil {
			return report, err
		}
		batch++
		if opts.AfterBatchCommit != nil {
			if err := opts.AfterBatchCommit(batch); err != nil {
				return report, err
			}
		}
	}
	// Self-check: nothing may still depend on the previous key. The marker is
	// only moved after this holds, so a failed run still reads as pending.
	pending, err := s.countSecretPayloadsRequiringPrevious(ctx, true)
	if err != nil {
		return report, err
	}
	if pending != 0 {
		return report, fmt.Errorf("%w: %d stored payloads", ErrSecretRotationIncomplete, pending)
	}
	if opts.BeforeMarkerCommit != nil {
		if err := opts.BeforeMarkerCommit(); err != nil {
			return report, err
		}
	}
	if err := s.commitSecretKeyMarker(ctx); err != nil {
		return report, err
	}
	return report, nil
}

// rewriteSecretPayloadBatch re-encrypts one batch inside a single transaction.
// Rows already opened by the primary key are counted, not rewritten.
func (s *GatewayStore) rewriteSecretPayloadBatch(ctx context.Context, ring secretKeyRing, rows []secretPayloadRow) (rewritten, skipped, already int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return rewritten, skipped, already, err
		}
		prefix, aad := secretPayloadAAD(row)
		raw, err := decodeSecretEnvelope(row.ciphertext, prefix)
		if err != nil {
			return rewritten, skipped, already, err
		}
		if raw == nil {
			continue
		}
		if _, err := openSecretEnvelopeRaw(ring.primary, raw, aad); err == nil {
			already++
			continue
		}
		plain, err := openSecretEnvelopeRaw(ring.previous, raw, aad)
		if err != nil {
			return rewritten, skipped, already, fmt.Errorf("%w: table %s", ErrSecretRotationPayload, row.table)
		}
		next, err := s.encryptSecretEnvelope(plain, aad, prefix)
		discardSecret(plain)
		if err != nil {
			return rewritten, skipped, already, err
		}
		// Compare-and-set: a row rewritten by someone else since the batch was
		// read must not be overwritten. It is skipped and left for a later run,
		// which the self-check enforces before the marker moves.
		// Both shapes store their ciphertext in credential_versions: the vault
		// binds that row, it does not own a second ciphertext column.
		result, err := tx.ExecContext(ctx, `UPDATE credential_versions SET payload_ciphertext = ? WHERE id = ? AND payload_ciphertext = ?`,
			next, row.versionID, row.ciphertext)
		if err != nil {
			return rewritten, skipped, already, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return rewritten, skipped, already, err
		}
		if affected == 0 {
			skipped++
			continue
		}
		rewritten++
	}
	if err := tx.Commit(); err != nil {
		return rewritten, skipped, already, err
	}
	return rewritten, skipped, already, nil
}

// secretPayloadAAD rebuilds the AAD and envelope prefix a stored row was written
// with. The prefix decides whether an AAD exists at all: v1 rows keep the
// unbound shape they were written with, v2 rows keep the binding they already
// have.
func secretPayloadAAD(row secretPayloadRow) (string, []byte) {
	prefix := secretEnvelopePrefixV2
	var aad []byte
	if row.table == secretRotationTableVault {
		aad = machineLoginAAD(row.credentialID, row.versionID, row.digest, row.assetID, row.username)
	} else {
		aad = credentialPayloadAAD(row.credentialID, row.versionID, row.credentialType, row.digest)
	}
	if hasSecretEnvelopePrefix(row.ciphertext, secretEnvelopePrefixV1) {
		return secretEnvelopePrefixV1, nil
	}
	return prefix, aad
}

func hasSecretEnvelopePrefix(ciphertext, prefix string) bool {
	return len(ciphertext) >= len(prefix) && ciphertext[:len(prefix)] == prefix
}

const (
	secretRotationTableCredentialVersions = "credential_versions"
	secretRotationTableVault              = "inventory_machine_login_accounts"
)

// readCredentialPayloadBatch reads ordinary (non machine-login) payload rows in
// id order after afterID.
func (s *GatewayStore) readCredentialPayloadBatch(ctx context.Context, afterID string, limit int) ([]secretPayloadRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT v.id, v.credential_id, c.type, v.payload_ciphertext, v.payload_digest
		FROM credential_versions v JOIN credentials c ON c.id = v.credential_id
		WHERE v.payload_ciphertext <> '' AND c.scope <> ? AND v.id > ?
		ORDER BY v.id LIMIT ?`, machineVaultScope, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secretPayloadRow
	for rows.Next() {
		var row secretPayloadRow
		var credentialType string
		row.table = secretRotationTableCredentialVersions
		if err := rows.Scan(&row.versionID, &row.credentialID, &credentialType, &row.ciphertext, &row.digest); err != nil {
			return nil, err
		}
		row.credentialType = CredentialType(credentialType)
		out = append(out, row)
	}
	return out, rows.Err()
}

// readVaultPayloadBatch reads machine-login vault payload rows in id order after
// afterID. The asset and username bindings come from the accounts row, because
// the AAD cannot be rebuilt without them.
func (s *GatewayStore) readVaultPayloadBatch(ctx context.Context, afterID string, limit int) ([]secretPayloadRow, error) {
	if !s.tableExists(ctx, "inventory_machine_login_accounts") {
		var stored int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM credential_versions v JOIN credentials c ON c.id = v.credential_id
			WHERE v.payload_ciphertext <> '' AND c.scope = ?`, machineVaultScope).Scan(&stored); err != nil {
			return nil, err
		}
		if stored != 0 {
			return nil, fmt.Errorf("%w: table %s", ErrSecretRotationPayload, secretRotationTableVault)
		}
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT v.id, v.credential_id, v.payload_ciphertext, v.payload_digest, a.asset_id, a.username
		FROM credential_versions v
		JOIN credentials c ON c.id = v.credential_id
		JOIN inventory_machine_login_accounts a ON a.version_id = v.id
		WHERE v.payload_ciphertext <> '' AND c.scope = ? AND v.id > ?
		ORDER BY v.id LIMIT ?`, machineVaultScope, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secretPayloadRow
	for rows.Next() {
		var row secretPayloadRow
		row.table = secretRotationTableVault
		if err := rows.Scan(&row.versionID, &row.credentialID, &row.ciphertext, &row.digest, &row.assetID, &row.username); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// classifySecretPayload reports whether one stored row carries a payload and,
// if so, whether only the previous key opens it. Rotation, its self-check and
// its dry-run share this function so all three agree on what "needs the previous
// key" means.
func classifySecretPayload(ring secretKeyRing, row secretPayloadRow) (present, requiresPrevious bool, err error) {
	prefix, aad := secretPayloadAAD(row)
	raw, err := decodeSecretEnvelope(row.ciphertext, prefix)
	if err != nil {
		return false, false, err
	}
	if raw == nil {
		return false, false, nil
	}
	if _, err := openSecretEnvelopeRaw(ring.primary, raw, aad); err == nil {
		return true, false, nil
	}
	plain, err := openSecretEnvelopeRaw(ring.previous, raw, aad)
	if err != nil {
		return true, false, fmt.Errorf("%w: table %s", ErrSecretRotationPayload, row.table)
	}
	discardSecret(plain)
	return true, true, nil
}

// countSecretPayloadsRequiringPrevious counts stored payloads that only the
// previous key opens. When strict is true a payload that neither key opens is an
// error rather than ignored: the rotation must not certify a table it cannot
// read.
func (s *GatewayStore) countSecretPayloadsRequiringPrevious(ctx context.Context, strict bool) (int, error) {
	ring, ringErr := s.secretKeyRing()
	if ringErr != nil {
		return 0, ringErr
	}
	count := 0
	for _, read := range []func(context.Context, string, int) ([]secretPayloadRow, error){s.readCredentialPayloadBatch, s.readVaultPayloadBatch} {
		lastID := ""
		for {
			rows, err := read(ctx, lastID, defaultSecretRotationBatchSize)
			if err != nil {
				return 0, err
			}
			if len(rows) == 0 {
				break
			}
			lastID = rows[len(rows)-1].versionID
			for _, row := range rows {
				present, requiresPrevious, err := classifySecretPayload(ring, row)
				if err != nil {
					if strict {
						return 0, err
					}
					continue
				}
				if present && requiresPrevious {
					count++
				}
			}
		}
	}
	return count, nil
}

// commitSecretKeyMarker moves gateway_secret_key_check to the primary key in its
// own transaction, after every payload is known to be readable with it.
func (s *GatewayStore) commitSecretKeyMarker(ctx context.Context) error {
	mac, err := s.secretKeyMAC()
	if err != nil {
		return err
	}
	// The machine-login vault keeps its own key marker, derived from the same
	// master key. Leaving it behind would make the vault unwritable the moment
	// the previous key is dropped, so it moves in the same final transaction.
	vaultMAC := ""
	if s.tableExists(ctx, "inventory_machine_login_key") {
		if vaultMAC, err = s.machineLoginMAC("vault-key", "v1"); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT key_mac FROM gateway_secret_key_check WHERE singleton = 1`).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO gateway_secret_key_check(singleton, key_mac, created_at) VALUES(1, ?, ?)`, mac, formatTime(time.Now().UTC()))
	case err != nil:
		return err
	case existing == mac:
	default:
		_, err = tx.ExecContext(ctx, `UPDATE gateway_secret_key_check SET key_mac = ? WHERE singleton = 1`, mac)
	}
	if err != nil {
		return err
	}
	if vaultMAC != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE inventory_machine_login_key SET key_mac = ? WHERE singleton = 1`, vaultMAC); err != nil {
			return err
		}
	}
	return tx.Commit()
}
