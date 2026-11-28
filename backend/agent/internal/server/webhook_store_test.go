package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestWebhookStoreCreateEnqueueAndClaim(t *testing.T) {
	t.Setenv(secretKeyEnv, "test-webhook-secret-key")
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	ep, secret, err := store.CreateWebhookEndpoint(ctx, WebhookCreateInput{
		Name: "ops", URL: "https://93.184.216.34/hook", EventTypes: []string{"execution.finished"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ep.ID == "" || len(secret) != webhookSecretBytes*2 || ep.URLHost != "93.184.216.34" {
		t.Fatalf("unexpected endpoint: %+v secret_len=%d", ep, len(secret))
	}
	ev, err := newPlatformEvent("execution.finished", time.Now(), "usr_test", "c1", "i1", "audit-1", map[string]any{"audit_id": "1"}, "audit-1")
	if err != nil {
		t.Fatal(err)
	}
	id, inserted, err := store.EnqueuePlatformEvent(ctx, ev)
	if err != nil || !inserted || id != ev.ID {
		t.Fatalf("enqueue: id=%s inserted=%v err=%v", id, inserted, err)
	}
	if dup, inserted, err := store.EnqueuePlatformEvent(ctx, ev); err != nil || inserted || dup != ev.ID {
		t.Fatalf("dedupe: id=%s inserted=%v err=%v", dup, inserted, err)
	}
	job, err := store.ClaimDueWebhookDelivery(ctx, time.Now().UTC().Add(time.Second))
	if err != nil || job == nil {
		t.Fatalf("claim: job=%+v err=%v", job, err)
	}
	if job.DeliveryID != webhookDeliveryID(ep.ID, ev.ID) || job.EndpointID != ep.ID || len(job.Body) == 0 || job.Secret != secret {
		t.Fatalf("unexpected job: %+v", job)
	}
	if err := store.CompleteWebhookDelivery(ctx, job, 204); err != nil {
		t.Fatal(err)
	}
	if next, ok, err := store.NextWebhookDueAt(ctx); err != nil || ok {
		t.Fatalf("expected no due delivery, got %v ok=%v err=%v", next, ok, err)
	}
}

func TestWebhookDeliveryLeaseRecoversAfterExpiry(t *testing.T) {
	t.Setenv(secretKeyEnv, "test-webhook-lease-key")
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	ep, _, err := store.CreateWebhookEndpoint(ctx, WebhookCreateInput{
		Name: "lease", URL: "https://93.184.216.34/hook", EventTypes: []string{"execution.finished"},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	ev, err := newPlatformEvent("execution.finished", now, "usr_lease", "c1", "i1", "", map[string]any{"audit_id": "1"}, "lease-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnqueuePlatformEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimDueWebhookDelivery(ctx, now.Add(time.Second))
	if err != nil || first == nil {
		t.Fatalf("first claim: job=%+v err=%v", first, err)
	}
	if blocked, err := store.ClaimDueWebhookDelivery(ctx, now.Add(2*time.Second)); err != nil || blocked != nil {
		t.Fatalf("live lease should block reclaim: job=%+v err=%v", blocked, err)
	}
	second, err := store.ClaimDueWebhookDelivery(ctx, now.Add(32*time.Second))
	if err != nil || second == nil {
		t.Fatalf("expired lease should be reclaimed: job=%+v err=%v", second, err)
	}
	if second.Attempt != 2 || second.LeaseToken == first.LeaseToken {
		t.Fatalf("unexpected reclaimed job: %+v", second)
	}
	if err := store.CompleteWebhookDelivery(ctx, first, 204); err != nil {
		t.Fatalf("stale completion should be a no-op: %v", err)
	}
	items, err := store.ListWebhookDeliveries(ctx, ep.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "delivering" || items[0].Attempt != 2 {
		t.Fatalf("stale completion changed state: %+v", items)
	}
	if err := store.CompleteWebhookDelivery(ctx, second, 204); err != nil {
		t.Fatal(err)
	}
	items, err = store.ListWebhookDeliveries(ctx, ep.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != "delivered" || items[0].ResponseStatus != 204 {
		t.Fatalf("fresh completion did not land: %+v", items)
	}
}

func TestFinishExecutionAuditWritesEventInSameTransaction(t *testing.T) {
	t.Setenv(secretKeyEnv, "test-webhook-finish-key")
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, _, err := store.CreateWebhookEndpoint(ctx, WebhookCreateInput{
		Name: "finish", URL: "https://93.184.216.34/hook", EventTypes: []string{"execution.finished"},
	}); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-time.Minute)
	id, err := store.StartAudit(AuditEvent{
		UserID: "usr_finish", Cluster: "c1", Instance: "i1", Action: "exec",
		Status: "running", StartedAt: started,
	})
	if err != nil {
		t.Fatal(err)
	}
	ended := time.Now().UTC()
	ev, err := newPlatformEvent("execution.finished", ended, "usr_finish", "c1", "i1", "", map[string]any{
		"execution_id": "op-1", "action": "exec", "status": "success",
	}, "finish-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishExecutionAudit(ctx, id, AuditFinish{Status: "success", EndedAt: ended}, ev); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := store.db.QueryRowContext(ctx, "SELECT status FROM audit_events WHERE id=?", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "success" {
		t.Fatalf("audit status = %q, want success", status)
	}
	var events, deliveries int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM platform_events WHERE id=?", ev.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_deliveries WHERE event_id=?", ev.ID).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if events != 1 || deliveries != 1 {
		t.Fatalf("outbox rows: events=%d deliveries=%d, want 1/1", events, deliveries)
	}
}

func TestPruneWebhookDataKeepsActiveDeliveries(t *testing.T) {
	t.Setenv(secretKeyEnv, "test-webhook-prune-key")
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, _, err := store.CreateWebhookEndpoint(ctx, WebhookCreateInput{
		Name: "prune", URL: "https://93.184.216.34/hook", EventTypes: []string{"execution.finished"},
	}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-48 * time.Hour)
	delivered, err := newPlatformEvent("execution.finished", old, "usr_prune", "c1", "i1", "", map[string]any{"audit_id": "1"}, "prune-delivered")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnqueuePlatformEvent(ctx, delivered); err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimDueWebhookDelivery(ctx, time.Now().UTC())
	if err != nil || job == nil {
		t.Fatalf("claim: job=%+v err=%v", job, err)
	}
	if err := store.CompleteWebhookDelivery(ctx, job, 204); err != nil {
		t.Fatal(err)
	}
	pending, err := newPlatformEvent("execution.finished", old, "usr_prune", "c1", "i1", "", map[string]any{"audit_id": "2"}, "prune-pending")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnqueuePlatformEvent(ctx, pending); err != nil {
		t.Fatal(err)
	}
	events, deliveries, err := store.PruneWebhookData(ctx, time.Now().UTC().Add(-24*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if events != 1 || deliveries != 1 {
		t.Fatalf("pruned events=%d deliveries=%d, want 1/1", events, deliveries)
	}
	var n int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM platform_events WHERE id=?", pending.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("pending event kept=%d err=%v", n, err)
	}
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM webhook_deliveries WHERE event_id=?", pending.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("pending delivery kept=%d err=%v", n, err)
	}
}
