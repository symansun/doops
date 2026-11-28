package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func webhookTS(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func (s *GatewayStore) CreateWebhookEndpoint(ctx context.Context, in WebhookCreateInput) (*WebhookEndpoint, string, error) {
	if err := validateWebhookName(in.Name); err != nil {
		return nil, "", err
	}
	if len(strings.TrimSpace(in.Description)) > webhookMaxDescription {
		return nil, "", ErrWebhookNameInvalid
	}
	if err := validateWebhookScope(in.ScopeCluster, in.ScopeInstance); err != nil {
		return nil, "", err
	}
	events, err := normalizeWebhookEventTypes(in.EventTypes)
	if err != nil {
		return nil, "", err
	}
	normalized, host, digest, err := validateWebhookURL(ctx, in.URL)
	if err != nil {
		return nil, "", err
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM webhook_endpoints WHERE deleted_at = ''`).Scan(&count); err != nil {
		return nil, "", err
	}
	if count >= webhookMaxEndpoints {
		return nil, "", ErrWebhookLimitExceeded
	}
	id := randomWebhookID("wh_", 12)
	secret := randomWebhookID("", webhookSecretBytes)
	urlCipher, err := s.encryptSecretWithAAD(normalized, webhookAADURL(id))
	if err != nil {
		return nil, "", err
	}
	secretCipher, err := s.encryptSecretWithAAD(secret, webhookAADSecret(id))
	if err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO webhook_endpoints
		(id,name,description,url_ciphertext,url_host,url_sha256,secret_ciphertext,previous_secret_ciphertext,previous_secret_expires_at_ms,enabled,scope_cluster,scope_instance,created_by,created_at,updated_at,last_success_at_ms,last_failure_at_ms,last_error_category,deleted_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,0,'','')`,
		id, strings.TrimSpace(in.Name), strings.TrimSpace(in.Description), urlCipher, host, digest, secretCipher, "", 0,
		boolInt(enabled), strings.TrimSpace(in.ScopeCluster), strings.TrimSpace(in.ScopeInstance), in.CreatedBy,
		webhookTS(now), webhookTS(now)); err != nil {
		return nil, "", err
	}
	if err := replaceWebhookSubscriptions(ctx, tx, id, events, now); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	ep, err := s.GetWebhookEndpoint(ctx, id)
	if err != nil {
		return nil, "", err
	}
	return ep, secret, nil
}

func replaceWebhookSubscriptions(ctx context.Context, tx *sql.Tx, endpointID string, events []string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM webhook_subscriptions WHERE endpoint_id = ?`, endpointID); err != nil {
		return err
	}
	for _, eventType := range events {
		if _, err := tx.ExecContext(ctx, `INSERT INTO webhook_subscriptions(endpoint_id,event_type,created_at) VALUES(?,?,?)`, endpointID, eventType, webhookTS(now)); err != nil {
			return err
		}
	}
	return nil
}

func (s *GatewayStore) GetWebhookEndpoint(ctx context.Context, id string) (*WebhookEndpoint, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,name,description,url_host,url_sha256,enabled,scope_cluster,scope_instance,created_by,created_at,updated_at,last_success_at_ms,last_failure_at_ms,last_error_category
		FROM webhook_endpoints WHERE id = ? AND deleted_at = ''`, id)
	ep, err := scanWebhookEndpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWebhookNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.loadWebhookSubscriptions(ctx, ep); err != nil {
		return nil, err
	}
	return ep, nil
}

func (s *GatewayStore) ListWebhookEndpoints(ctx context.Context) ([]*WebhookEndpoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,url_host,url_sha256,enabled,scope_cluster,scope_instance,created_by,created_at,updated_at,last_success_at_ms,last_failure_at_ms,last_error_category
		FROM webhook_endpoints WHERE deleted_at = '' ORDER BY created_at DESC, id DESC LIMIT ?`, webhookMaxEndpoints)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*WebhookEndpoint, 0, 8)
	for rows.Next() {
		ep, err := scanWebhookEndpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, ep := range out {
		if err := s.loadWebhookSubscriptions(ctx, ep); err != nil {
			return nil, err
		}
	}
	return out, nil
}

type webhookEndpointScanner interface {
	Scan(dest ...any) error
}

func scanWebhookEndpoint(scanner webhookEndpointScanner) (*WebhookEndpoint, error) {
	var ep WebhookEndpoint
	var enabled int
	if err := scanner.Scan(&ep.ID, &ep.Name, &ep.Description, &ep.URLHost, &ep.URLSHA256, &enabled,
		&ep.ScopeCluster, &ep.ScopeInstance, &ep.CreatedBy, &ep.CreatedAt, &ep.UpdatedAt,
		&ep.LastSuccessAtMs, &ep.LastFailureAtMs, &ep.LastErrorCategory); err != nil {
		return nil, err
	}
	ep.Enabled = enabled != 0
	return &ep, nil
}

func (s *GatewayStore) loadWebhookSubscriptions(ctx context.Context, ep *WebhookEndpoint) error {
	rows, err := s.db.QueryContext(ctx, `SELECT event_type FROM webhook_subscriptions WHERE endpoint_id = ? ORDER BY event_type`, ep.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	ep.EventTypes = ep.EventTypes[:0]
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return err
		}
		ep.EventTypes = append(ep.EventTypes, t)
	}
	return rows.Err()
}

func (s *GatewayStore) UpdateWebhookEndpoint(ctx context.Context, id string, in WebhookUpdateInput) (*WebhookEndpoint, error) {
	ep, err := s.GetWebhookEndpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		if err := validateWebhookName(*in.Name); err != nil {
			return nil, err
		}
		ep.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		if len(strings.TrimSpace(*in.Description)) > webhookMaxDescription {
			return nil, ErrWebhookNameInvalid
		}
		ep.Description = strings.TrimSpace(*in.Description)
	}
	cluster, instance := ep.ScopeCluster, ep.ScopeInstance
	if in.ScopeCluster != nil {
		cluster = strings.TrimSpace(*in.ScopeCluster)
	}
	if in.ScopeInstance != nil {
		instance = strings.TrimSpace(*in.ScopeInstance)
	}
	if err := validateWebhookScope(cluster, instance); err != nil {
		return nil, err
	}
	ep.ScopeCluster, ep.ScopeInstance = cluster, instance
	if in.Enabled != nil {
		ep.Enabled = *in.Enabled
	}
	var events []string
	if in.EventTypesSet {
		events, err = normalizeWebhookEventTypes(in.EventTypes)
		if err != nil {
			return nil, err
		}
	}
	urlCipher, host, digest := "", "", ""
	if in.URL != nil {
		normalized, h, d, err := validateWebhookURL(ctx, *in.URL)
		if err != nil {
			return nil, err
		}
		urlCipher, err = s.encryptSecretWithAAD(normalized, webhookAADURL(id))
		if err != nil {
			return nil, err
		}
		host, digest = h, d
	}
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if urlCipher != "" {
		_, err = tx.ExecContext(ctx, "UPDATE webhook_endpoints SET name=?,description=?,url_ciphertext=?,url_host=?,url_sha256=?,scope_cluster=?,scope_instance=?,enabled=?,updated_at=? WHERE id=? AND deleted_at=''",
			ep.Name, ep.Description, urlCipher, host, digest, ep.ScopeCluster, ep.ScopeInstance, boolInt(ep.Enabled), webhookTS(now), id)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE webhook_endpoints SET name=?,description=?,scope_cluster=?,scope_instance=?,enabled=?,updated_at=? WHERE id=? AND deleted_at=''",
			ep.Name, ep.Description, ep.ScopeCluster, ep.ScopeInstance, boolInt(ep.Enabled), webhookTS(now), id)
	}
	if err != nil {
		return nil, err
	}
	if in.EventTypesSet {
		if err := replaceWebhookSubscriptions(ctx, tx, id, events, now); err != nil {
			return nil, err
		}
	}
	if !ep.Enabled {
		if _, err := tx.ExecContext(ctx, "UPDATE webhook_deliveries SET status='failed',error_category='endpoint_disabled',updated_at_ms=? WHERE endpoint_id=? AND status IN ('pending','delivering')", now.UnixMilli(), id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetWebhookEndpoint(ctx, id)
}

func (s *GatewayStore) DeleteWebhookEndpoint(ctx context.Context, id string) error {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, "UPDATE webhook_endpoints SET deleted_at=?, enabled=0, updated_at=? WHERE id=? AND deleted_at=''", webhookTS(now), webhookTS(now), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrWebhookNotFound
	}
	_, _ = s.db.ExecContext(ctx, "UPDATE webhook_deliveries SET status='failed',error_category='endpoint_deleted',updated_at_ms=? WHERE endpoint_id=? AND status IN ('pending','delivering')", now.UnixMilli(), id)
	return nil
}

func (s *GatewayStore) RotateWebhookSecret(ctx context.Context, id string) (string, error) {
	ep, err := s.GetWebhookEndpoint(ctx, id)
	if err != nil {
		return "", err
	}
	var current string
	if err := s.db.QueryRowContext(ctx, "SELECT secret_ciphertext FROM webhook_endpoints WHERE id=? AND deleted_at=''", id).Scan(&current); err != nil {
		return "", err
	}
	oldSecret, err := s.decryptSecretWithAAD(current, webhookAADSecret(ep.ID))
	if err != nil {
		return "", err
	}
	secret := randomWebhookID("", webhookSecretBytes)
	cipher, err := s.encryptSecretWithAAD(secret, webhookAADSecret(ep.ID))
	if err != nil {
		return "", err
	}
	previousCipher, err := s.encryptSecretWithAAD(oldSecret, webhookAADPrevious(ep.ID))
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	expires := now.Add(webhookPreviousSecretTTL).UnixMilli()
	if _, err := s.db.ExecContext(ctx, "UPDATE webhook_endpoints SET previous_secret_ciphertext=?,previous_secret_expires_at_ms=?,secret_ciphertext=?,updated_at=? WHERE id=?",
		previousCipher, expires, cipher, webhookTS(now), id); err != nil {
		return "", err
	}
	return secret, nil
}

// FinishExecutionAudit writes the terminal audit row and its outbox event in one
// transaction. A delivery can therefore never observe a finished execution that
// has no event, and a crash can never leave an event for an unfinished audit.
func (s *GatewayStore) FinishExecutionAudit(ctx context.Context, id int64, finish AuditFinish, ev PlatformEvent) error {
	if id == 0 {
		return nil
	}
	var (
		docEvent    AuditEvent
		dbFinish    = finish
		tailRelPath string
		docEnabled  = s.auditDocStoreEnabled()
	)
	if docEnabled {
		var err error
		docEvent, dbFinish, tailRelPath, err = s.prepareAuditDocFinish(id, finish)
		if err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		if tailRelPath != "" {
			s.removeAuditTailDoc(tailRelPath)
		}
		return err
	}
	defer tx.Rollback()
	transitioned, err := finishAuditRow(tx, id, dbFinish)
	if err != nil {
		if tailRelPath != "" {
			s.removeAuditTailDoc(tailRelPath)
		}
		return err
	}
	_, inserted, err := enqueuePlatformEventTx(ctx, tx, ev)
	if err != nil {
		if tailRelPath != "" {
			s.removeAuditTailDoc(tailRelPath)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		if tailRelPath != "" {
			s.removeAuditTailDoc(tailRelPath)
		}
		return err
	}
	if docEnabled {
		s.takeInFlightAudit(id)
		if transitioned {
			if err := s.appendAuditDocRecord(docEvent); err != nil {
				if inserted && s.webhookWake != nil {
					s.webhookWake()
				}
				return err
			}
		} else if tailRelPath != "" {
			s.removeAuditTailDoc(tailRelPath)
		}
	}
	if inserted && s.webhookWake != nil {
		s.webhookWake()
	}
	return nil
}

func (s *GatewayStore) EnqueuePlatformEvent(ctx context.Context, ev PlatformEvent) (string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	id, inserted, err := enqueuePlatformEventTx(ctx, tx, ev)
	if err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	if inserted && s.webhookWake != nil {
		s.webhookWake()
	}
	return id, inserted, nil
}

func enqueuePlatformEventTx(ctx context.Context, tx *sql.Tx, ev PlatformEvent) (string, bool, error) {
	var dedupe any
	if ev.DedupeKey != "" {
		dedupe = ev.DedupeKey
	}
	now := time.Now().UTC()
	upsert := gatewayUpsert{
		insert: "INSERT INTO platform_events(id,type,occurred_at_ms,occurred_at,actor_user_id,cluster,instance,correlation_id,body_json,dedupe_key,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
		target: []string{"dedupe_key"}, doNothing: true,
	}
	res, err := tx.ExecContext(ctx, gatewayUpsertSQL(upsert), ev.ID, ev.Type, ev.OccurredAt.UnixMilli(), ev.OccurredAt.UTC().Format(time.RFC3339Nano),
		ev.ActorUserID, ev.Cluster, ev.Instance, ev.CorrelationID, string(ev.Body), dedupe, webhookTS(now))
	if err != nil {
		return "", false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return "", false, err
	}
	if affected == 0 {
		if ev.DedupeKey == "" {
			return "", false, errors.New("platform event insert was ignored without a dedupe key")
		}
		var existing string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM platform_events WHERE dedupe_key=?", ev.DedupeKey).Scan(&existing); err != nil {
			return "", false, err
		}
		return existing, false, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT e.id FROM webhook_endpoints e JOIN webhook_subscriptions s ON s.endpoint_id = e.id WHERE s.event_type = ? AND e.enabled = 1 AND e.deleted_at = '' AND (e.scope_cluster = '' OR (e.scope_cluster = ? AND (e.scope_instance = '' OR e.scope_instance = ?)))",
		ev.Type, ev.Cluster, ev.Instance)
	if err != nil {
		return "", false, err
	}
	var endpointIDs []string
	for rows.Next() {
		var endpointID string
		if err := rows.Scan(&endpointID); err != nil {
			rows.Close()
			return "", false, err
		}
		endpointIDs = append(endpointIDs, endpointID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", false, err
	}
	if err := rows.Close(); err != nil {
		return "", false, err
	}
	if len(endpointIDs) > 0 {
		insert := gatewayUpsert{insert: "INSERT INTO webhook_deliveries(id,endpoint_id,event_id,status,attempt,next_attempt_at_ms,lease_token,lease_until_ms,last_response_status,error_category,created_at_ms,updated_at_ms,delivered_at_ms) VALUES(?,?,?,'pending',0,?,'',0,0,'',?,?,0)", target: []string{"id"}, doNothing: true}
		nowMs := now.UnixMilli()
		args := make([]any, 0, len(endpointIDs)*6)
		for _, endpointID := range endpointIDs {
			args = append(args, webhookDeliveryID(endpointID, ev.ID), endpointID, ev.ID, nowMs, nowMs, nowMs)
		}
		if _, err := tx.ExecContext(ctx, gatewayUpsertBatchSQL(insert, len(endpointIDs)), args...); err != nil {
			return "", false, err
		}
	}
	return ev.ID, true, nil
}

func (s *GatewayStore) EnqueueWebhookTestEvent(ctx context.Context, endpointID, actor string) (string, error) {
	ep, err := s.GetWebhookEndpoint(ctx, endpointID)
	if err != nil {
		return "", err
	}
	if !ep.Enabled {
		return "", ErrWebhookEndpointDisabled
	}
	ev, err := newPlatformEvent("webhook.test", time.Now().UTC(), actor, ep.ScopeCluster, ep.ScopeInstance, "", map[string]any{
		"endpoint_id":  ep.ID,
		"requested_by": actor,
	}, "")
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, "INSERT INTO platform_events(id,type,occurred_at_ms,occurred_at,actor_user_id,cluster,instance,correlation_id,body_json,dedupe_key,created_at) VALUES(?,?,?,?,?,?,?,?,?,NULL,?)",
		ev.ID, ev.Type, ev.OccurredAt.UnixMilli(), ev.OccurredAt.UTC().Format(time.RFC3339Nano), ev.ActorUserID, ev.Cluster, ev.Instance, ev.CorrelationID, string(ev.Body), webhookTS(now)); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO webhook_deliveries(id,endpoint_id,event_id,status,attempt,next_attempt_at_ms,lease_token,lease_until_ms,last_response_status,error_category,created_at_ms,updated_at_ms,delivered_at_ms) VALUES(?,?,?,'pending',0,?,'',0,0,'',?,?,0)",
		webhookDeliveryID(endpointID, ev.ID), endpointID, ev.ID, now.UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if s.webhookWake != nil {
		s.webhookWake()
	}
	return ev.ID, nil
}

func (s *GatewayStore) ListWebhookDeliveries(ctx context.Context, endpointID string, limit int) ([]WebhookDelivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, "SELECT id,endpoint_id,event_id,status,attempt,next_attempt_at_ms,last_response_status,error_category,created_at_ms,updated_at_ms,delivered_at_ms FROM webhook_deliveries WHERE endpoint_id=? ORDER BY created_at_ms DESC, id DESC LIMIT ?", endpointID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]WebhookDelivery, 0, limit)
	for rows.Next() {
		var d WebhookDelivery
		if err := rows.Scan(&d.ID, &d.EndpointID, &d.EventID, &d.Status, &d.Attempt, &d.NextAttemptAtMs, &d.ResponseStatus, &d.ErrorCategory, &d.CreatedAtMs, &d.UpdatedAtMs, &d.DeliveredAtMs); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *GatewayStore) webhookEndpointDestination(ctx context.Context, id string) (string, string, string, int64, error) {
	var urlCipher, secretCipher, previousCipher string
	var previousExpires int64
	if err := s.db.QueryRowContext(ctx, "SELECT url_ciphertext,secret_ciphertext,previous_secret_ciphertext,previous_secret_expires_at_ms FROM webhook_endpoints WHERE id=? AND deleted_at=''", id).Scan(&urlCipher, &secretCipher, &previousCipher, &previousExpires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", "", 0, ErrWebhookNotFound
		}
		return "", "", "", 0, err
	}
	u, err := s.decryptSecretWithAAD(urlCipher, webhookAADURL(id))
	if err != nil {
		return "", "", "", 0, fmt.Errorf("webhook endpoint %s URL cannot be decrypted: %w", id, err)
	}
	secret, err := s.decryptSecretWithAAD(secretCipher, webhookAADSecret(id))
	if err != nil {
		return "", "", "", 0, err
	}
	previous := ""
	if previousCipher != "" && previousExpires > time.Now().UnixMilli() {
		previous, _ = s.decryptSecretWithAAD(previousCipher, webhookAADPrevious(id))
	}
	return u, secret, previous, previousExpires, nil
}

func (s *GatewayStore) ClaimDueWebhookDelivery(ctx context.Context, now time.Time) (*webhookDeliveryJob, error) {
	lease := randomWebhookID("lease_", 12)
	nowMs := now.UnixMilli()
	row := s.db.QueryRowContext(ctx, "SELECT d.id,d.endpoint_id,d.event_id,d.attempt,ev.type,ev.body_json FROM webhook_deliveries d JOIN webhook_endpoints e ON e.id=d.endpoint_id JOIN platform_events ev ON ev.id=d.event_id WHERE ((d.status='pending' AND d.next_attempt_at_ms<=?) OR (d.status='delivering' AND d.lease_until_ms<?)) AND e.enabled=1 AND e.deleted_at='' ORDER BY d.next_attempt_at_ms,d.id LIMIT 1", nowMs, nowMs)
	var job webhookDeliveryJob
	var body string
	if err := row.Scan(&job.DeliveryID, &job.EndpointID, &job.EventID, &job.Attempt, &job.EventType, &body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE webhook_deliveries SET status='delivering',attempt=attempt+1,lease_token=?,lease_until_ms=?,updated_at_ms=? WHERE id=? AND ((status='pending' AND next_attempt_at_ms<=?) OR (status='delivering' AND lease_until_ms<?))", lease, nowMs+30000, nowMs, job.DeliveryID, nowMs, nowMs)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, nil
	}
	job.Attempt++
	job.LeaseToken = lease
	job.Body = []byte(body)
	job.URL, job.Secret, job.PreviousSecret, job.PreviousSecretExpires, err = s.webhookEndpointDestination(ctx, job.EndpointID)
	if err != nil {
		_, _ = s.db.ExecContext(ctx, "UPDATE webhook_deliveries SET status='pending',lease_token='',lease_until_ms=0,next_attempt_at_ms=?,updated_at_ms=? WHERE id=? AND lease_token=?", nowMs+60000, nowMs, job.DeliveryID, lease)
		return nil, err
	}
	return &job, nil
}

func (s *GatewayStore) CompleteWebhookDelivery(ctx context.Context, job *webhookDeliveryJob, statusCode int) error {
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx, "UPDATE webhook_deliveries SET status='delivered',lease_token='',lease_until_ms=0,last_response_status=?,error_category='',updated_at_ms=?,delivered_at_ms=? WHERE id=? AND lease_token=?", statusCode, now, now, job.DeliveryID, job.LeaseToken)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	_, err = s.db.ExecContext(ctx, "UPDATE webhook_endpoints SET last_success_at_ms=?,last_error_category='' WHERE id=?", now, job.EndpointID)
	return err
}

func (s *GatewayStore) FailWebhookDelivery(ctx context.Context, job *webhookDeliveryJob, statusCode int, category string, retryAt time.Time, terminal bool) error {
	now := time.Now().UnixMilli()
	if terminal {
		res, err := s.db.ExecContext(ctx, "UPDATE webhook_deliveries SET status='failed',lease_token='',lease_until_ms=0,last_response_status=?,error_category=?,updated_at_ms=? WHERE id=? AND lease_token=?", statusCode, category, now, job.DeliveryID, job.LeaseToken)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		_, err = s.db.ExecContext(ctx, "UPDATE webhook_endpoints SET last_failure_at_ms=?,last_error_category=? WHERE id=?", now, category, job.EndpointID)
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE webhook_deliveries SET status='pending',lease_token='',lease_until_ms=0,last_response_status=?,error_category=?,next_attempt_at_ms=?,updated_at_ms=? WHERE id=? AND lease_token=?", statusCode, category, retryAt.UnixMilli(), now, job.DeliveryID, job.LeaseToken)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	_, err = s.db.ExecContext(ctx, "UPDATE webhook_endpoints SET last_failure_at_ms=?,last_error_category=? WHERE id=?", now, category, job.EndpointID)
	return err
}

func (s *GatewayStore) NextWebhookDueAt(ctx context.Context) (time.Time, bool, error) {
	var next int64
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MIN(CASE WHEN d.status='pending' THEN d.next_attempt_at_ms ELSE d.lease_until_ms END),0) FROM webhook_deliveries d JOIN webhook_endpoints e ON e.id=d.endpoint_id WHERE ((d.status='pending') OR (d.status='delivering' AND d.lease_until_ms>0)) AND e.enabled=1 AND e.deleted_at=''").Scan(&next); err != nil {
		return time.Time{}, false, err
	}
	if next <= 0 {
		return time.Time{}, false, nil
	}
	return time.UnixMilli(next), true, nil
}

// PruneWebhookData deletes terminal deliveries and their events older than
// before. Events with a pending or delivering row are kept whole so a replay
// can still find the original body. It returns how many rows it removed.
func (s *GatewayStore) PruneWebhookData(ctx context.Context, before time.Time, limit int) (int64, int64, error) {
	if limit <= 0 {
		limit = 5000
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT ev.id FROM platform_events ev
		WHERE ev.occurred_at_ms < ?
		AND NOT EXISTS (SELECT 1 FROM webhook_deliveries d WHERE d.event_id = ev.id AND d.status IN ('pending','delivering'))
		LIMIT ?`, before.UTC().UnixMilli(), limit)
	if err != nil {
		return 0, 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	var events, deliveries int64
	for start := 0; start < len(ids); start += 400 {
		end := start + 400
		if end > len(ids) {
			end = len(ids)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", end-start), ",")
		args := make([]any, 0, end-start)
		for _, id := range ids[start:end] {
			args = append(args, id)
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM webhook_deliveries WHERE event_id IN (`+placeholders+`)`, args...)
		if err != nil {
			return 0, 0, err
		}
		n, _ := res.RowsAffected()
		deliveries += n
		res, err = tx.ExecContext(ctx, `DELETE FROM platform_events WHERE id IN (`+placeholders+`)`, args...)
		if err != nil {
			return 0, 0, err
		}
		n, _ = res.RowsAffected()
		events += n
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return events, deliveries, nil
}
