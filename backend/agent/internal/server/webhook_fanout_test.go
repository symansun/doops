package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestWebhookFanoutScopeAndSecretRotation(t *testing.T) {
	t.Setenv(secretKeyEnv, "test-webhook-fanout-key")
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	ep1, oldSecret, err := store.CreateWebhookEndpoint(ctx, WebhookCreateInput{
		Name: "c1", URL: "https://93.184.216.34/hook", EventTypes: []string{"execution.finished"},
		ScopeCluster: "c1", ScopeInstance: "i1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ep2, _, err := store.CreateWebhookEndpoint(ctx, WebhookCreateInput{
		Name: "c2", URL: "https://93.184.216.34/hook", EventTypes: []string{"execution.finished"},
		ScopeCluster: "c2", ScopeInstance: "i1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := newPlatformEvent("execution.finished", time.Now().UTC(), "usr_scope", "c1", "i1", "", map[string]any{"audit_id": "1"}, "scope-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnqueuePlatformEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	job, err := store.ClaimDueWebhookDelivery(ctx, time.Now().UTC().Add(time.Second))
	if err != nil || job == nil || job.EndpointID != ep1.ID {
		t.Fatalf("claim = %+v err=%v, want only %s", job, err, ep1.ID)
	}
	if ep2Deliveries, err := store.ListWebhookDeliveries(ctx, ep2.ID, 5); err != nil || len(ep2Deliveries) != 0 {
		t.Fatalf("scope mismatch delivered: %+v err=%v", ep2Deliveries, err)
	}
	if err := store.CompleteWebhookDelivery(ctx, job, 204); err != nil {
		t.Fatal(err)
	}
	newSecret, err := store.RotateWebhookSecret(ctx, ep1.ID)
	if err != nil || newSecret == "" || newSecret == oldSecret {
		t.Fatalf("rotate = %q err=%v", newSecret, err)
	}
	ev2, err := newPlatformEvent("execution.finished", time.Now().UTC(), "usr_scope", "c1", "i1", "", map[string]any{"audit_id": "2"}, "scope-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnqueuePlatformEvent(ctx, ev2); err != nil {
		t.Fatal(err)
	}
	rotated, err := store.ClaimDueWebhookDelivery(ctx, time.Now().UTC().Add(time.Second))
	if err != nil || rotated == nil {
		t.Fatalf("claim after rotate = %+v err=%v", rotated, err)
	}
	if rotated.Secret != newSecret || rotated.PreviousSecret != oldSecret {
		t.Fatalf("rotation window secret mismatch: new=%q previous=%q", rotated.Secret, rotated.PreviousSecret)
	}
}
