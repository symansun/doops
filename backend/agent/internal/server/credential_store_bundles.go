package server

// Credential bundles: multi-item bundles resolved for a single use.

import (
	"fmt"
	"strings"
	"time"
)

func (s *GatewayStore) CreateCredentialBundle(req CredentialBundleCreateRequest) (CredentialBundle, error) {
	return s.createCredentialBundle(req, 0)
}

func (s *GatewayStore) CreateCredentialBundleAudited(req CredentialBundleCreateRequest, auditID int64) (CredentialBundle, error) {
	return s.createCredentialBundle(req, auditID)
}

func (s *GatewayStore) createCredentialBundle(req CredentialBundleCreateRequest, auditID int64) (CredentialBundle, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.OwnerID = strings.TrimSpace(req.OwnerID)
	req.CreatedBy = strings.TrimSpace(req.CreatedBy)
	if req.Name == "" || req.CreatedBy == "" || !validCredentialScope(req.Scope) || len(req.Items) == 0 {
		return CredentialBundle{}, fmt.Errorf("credential bundle metadata and items are required")
	}
	if req.Scope == CredentialScopePersonal {
		if req.OwnerID == "" || req.OwnerID != req.CreatedBy {
			return CredentialBundle{}, ErrCredentialForbidden
		}
	} else {
		req.OwnerID = ""
	}
	items := make([]CredentialBundleItem, 0, len(req.Items))
	seen := make(map[string]struct{}, len(req.Items))
	for _, item := range req.Items {
		item.CredentialID = strings.TrimSpace(item.CredentialID)
		resource, err := s.credentialByID(item.CredentialID)
		if err != nil {
			return CredentialBundle{}, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
		}
		if !s.canManageCredential(resource, req.CreatedBy, ActionCredentialGrant) {
			return CredentialBundle{}, ErrCredentialForbidden
		}
		if _, exists := seen[item.CredentialID]; exists {
			return CredentialBundle{}, fmt.Errorf("credential bundle contains a duplicate item")
		}
		seen[item.CredentialID] = struct{}{}
		items = append(items, item)
	}

	now := time.Now().UTC()
	bundle := CredentialBundle{
		ID: "credbundle_" + randomHex(12), Name: req.Name, Scope: req.Scope, OwnerID: req.OwnerID,
		State: CredentialStateActive, CreatedBy: req.CreatedBy, CreatedAt: now, UpdatedAt: now, Items: items,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return CredentialBundle{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO credential_bundles
		(id, name, scope, owner_id, state, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		bundle.ID, bundle.Name, bundle.Scope, bundle.OwnerID, bundle.State, bundle.CreatedBy,
		formatTime(bundle.CreatedAt), formatTime(bundle.UpdatedAt)); err != nil {
		return CredentialBundle{}, err
	}
	for _, item := range bundle.Items {
		if _, err := tx.Exec(`INSERT INTO credential_bundle_items
			(bundle_id, credential_id) VALUES (?, ?)`,
			bundle.ID, item.CredentialID); err != nil {
			return CredentialBundle{}, err
		}
	}
	if auditID != 0 {
		if err := finishAuditWith(tx, auditID, AuditFinish{Status: "success", EndedAt: now}); err != nil {
			return CredentialBundle{}, fmt.Errorf("%w: %v", ErrCredentialAuditUnavailable, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return CredentialBundle{}, err
	}
	return bundle, nil
}

func (s *GatewayStore) ResolveCredentialBundle(actorID, name string) (CredentialBundle, error) {
	actorID = strings.TrimSpace(actorID)
	name = strings.TrimSpace(name)
	rows, err := s.db.Query(`SELECT id, name, scope, owner_id, state, created_by, created_at, updated_at
		FROM credential_bundles
		WHERE name = ? AND state = ? AND (owner_id = ? OR (scope = ? AND owner_id = ''))
		ORDER BY CASE WHEN owner_id = ? THEN 0 ELSE 1 END`,
		name, CredentialStateActive, actorID, CredentialScopePlatform, actorID)
	if err != nil {
		return CredentialBundle{}, err
	}
	defer rows.Close()
	var matches []CredentialBundle
	for rows.Next() {
		var bundle CredentialBundle
		var createdAt, updatedAt string
		if err := rows.Scan(&bundle.ID, &bundle.Name, &bundle.Scope, &bundle.OwnerID, &bundle.State,
			&bundle.CreatedBy, &createdAt, &updatedAt); err != nil {
			return CredentialBundle{}, err
		}
		bundle.CreatedAt, _ = parseTime(createdAt)
		bundle.UpdatedAt, _ = parseTime(updatedAt)
		matches = append(matches, bundle)
	}
	if err := rows.Err(); err != nil {
		return CredentialBundle{}, err
	}
	if len(matches) == 0 {
		return CredentialBundle{}, ErrCredentialBundleNotFound
	}
	if len(matches) > 1 {
		return CredentialBundle{}, ErrCredentialAmbiguous
	}
	bundle := matches[0]
	itemRows, err := s.db.Query(`SELECT credential_id
		FROM credential_bundle_items WHERE bundle_id = ? ORDER BY credential_id`, bundle.ID)
	if err != nil {
		return CredentialBundle{}, err
	}
	defer itemRows.Close()
	for itemRows.Next() {
		var item CredentialBundleItem
		if err := itemRows.Scan(&item.CredentialID); err != nil {
			return CredentialBundle{}, err
		}
		bundle.Items = append(bundle.Items, item)
	}
	return bundle, itemRows.Err()
}
