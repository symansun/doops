package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestAdminWebhookDeliveriesHTTPContractSnakeCase(t *testing.T) {
	t.Setenv(secretKeyEnv, "test-webhook-http-contract-key")
	store, err := OpenGatewayStore(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	adminUser, err := store.CreateUser("wh-admin")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := store.SetUserAdmin(adminUser.ID, true); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	adminTok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: adminUser.ID, Name: "admin"})
	if err != nil {
		t.Fatalf("create admin token: %v", err)
	}
	viewerUser, err := store.CreateUser("wh-viewer")
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	viewerTok, err := store.CreateToken(CreateTokenRequest{Kind: TokenKindUser, UserID: viewerUser.ID, Name: "viewer"})
	if err != nil {
		t.Fatalf("create viewer token: %v", err)
	}

	hub := NewGatewayHub(store, GatewayHubOptions{AgentLease: time.Minute})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	doReq := func(method, path, token string, body any) (*http.Response, []byte) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			reader = bytes.NewReader(raw)
		}
		req, err := http.NewRequest(method, ts.URL+path, reader)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		return resp, raw
	}

	// Auth boundaries: unauthenticated 401, non-admin 403.
	if resp, _ := doReq(http.MethodGet, "/v1/admin/webhooks", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth GET /v1/admin/webhooks status = %d, want 401", resp.StatusCode)
	}
	if resp, _ := doReq(http.MethodGet, "/v1/admin/webhooks", viewerTok.Plaintext, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin GET /v1/admin/webhooks status = %d, want 403", resp.StatusCode)
	}

	const canaryPath = "secret-hook-path-8e4c"
	const canaryQuery = "tok=canary-webhook-query-9a1b"
	createResp, createRaw := doReq(http.MethodPost, "/v1/admin/webhooks", adminTok.Plaintext, map[string]any{
		"name":        "contract-hook",
		"description": "delivery JSON contract test",
		"url":         "https://93.184.216.34/" + canaryPath + "?" + canaryQuery,
		"event_types": []string{"execution.finished"},
	})
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/admin/webhooks status = %d, body = %s", createResp.StatusCode, createRaw)
	}
	if got := createResp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("create Cache-Control = %q, want no-store", got)
	}
	var created struct {
		ID       string `json:"id"`
		Endpoint struct {
			URLHost   string `json:"url_host"`
			URLSHA256 string `json:"url_sha256"`
			Secret    string `json:"secret"`
		} `json:"endpoint"`
	}
	if err := json.Unmarshal(createRaw, &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || len(created.Endpoint.Secret) != 64 || created.Endpoint.URLHost != "93.184.216.34" {
		t.Fatalf("unexpected create payload: %s", createRaw)
	}
	for _, leak := range []string{canaryPath, canaryQuery} {
		if strings.Contains(string(createRaw), leak) {
			t.Fatalf("create response leaked URL secret %q: %s", leak, createRaw)
		}
	}

	// Non-admin must also be rejected on deliveries endpoint.
	if resp, _ := doReq(http.MethodGet, "/v1/admin/webhooks/"+created.ID+"/deliveries", viewerTok.Plaintext, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin GET deliveries status = %d, want 403", resp.StatusCode)
	}

	// Empty deliveries list must return [] (not null).
	emptyResp, emptyRaw := doReq(http.MethodGet, "/v1/admin/webhooks/"+created.ID+"/deliveries", adminTok.Plaintext, nil)
	if emptyResp.StatusCode != http.StatusOK {
		t.Fatalf("empty GET deliveries status = %d, body = %s", emptyResp.StatusCode, emptyRaw)
	}
	if !strings.Contains(string(emptyRaw), `"deliveries":[]`) {
		t.Fatalf("empty deliveries must serialize as [], got: %s", emptyRaw)
	}

	// Enqueue two deliveries: one delivered (204), one terminally failed (0 + error_category).
	ctx := context.Background()
	if _, err := store.EnqueueWebhookTestEvent(ctx, created.ID, adminUser.ID); err != nil {
		t.Fatalf("enqueue first test event: %v", err)
	}
	job1, err := store.ClaimDueWebhookDelivery(ctx, time.Now().UTC().Add(time.Second))
	if err != nil || job1 == nil {
		t.Fatalf("claim job1 = (%v, %v)", job1, err)
	}
	if err := store.CompleteWebhookDelivery(ctx, job1, http.StatusNoContent); err != nil {
		t.Fatalf("complete job1: %v", err)
	}

	if _, err := store.EnqueueWebhookTestEvent(ctx, created.ID, adminUser.ID); err != nil {
		t.Fatalf("enqueue second test event: %v", err)
	}
	job2, err := store.ClaimDueWebhookDelivery(ctx, time.Now().UTC().Add(time.Second))
	if err != nil || job2 == nil {
		t.Fatalf("claim job2 = (%v, %v)", job2, err)
	}
	if err := store.FailWebhookDelivery(ctx, job2, 0, "endpoint_disabled", time.Now().UTC(), true); err != nil {
		t.Fatalf("fail job2: %v", err)
	}

	listResp, listRaw := doReq(http.MethodGet, "/v1/admin/webhooks/"+created.ID+"/deliveries?limit=10", adminTok.Plaintext, nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("GET deliveries status = %d, body = %s", listResp.StatusCode, listRaw)
	}
	for _, leak := range []string{created.Endpoint.Secret, canaryPath, canaryQuery} {
		if strings.Contains(string(listRaw), leak) {
			t.Fatalf("deliveries response leaked %q: %s", leak, listRaw)
		}
	}

	var decoded struct {
		Deliveries []map[string]any `json:"deliveries"`
	}
	if err := json.Unmarshal(listRaw, &decoded); err != nil {
		t.Fatalf("decode deliveries JSON: %v", err)
	}
	if len(decoded.Deliveries) != 2 {
		t.Fatalf("deliveries count = %d, want 2; raw = %s", len(decoded.Deliveries), listRaw)
	}

	wantKeys := []string{
		"id",
		"endpoint_id",
		"event_id",
		"status",
		"attempt",
		"next_attempt_at_ms",
		"response_status",
		"error_category",
		"created_at_ms",
		"updated_at_ms",
		"delivered_at_ms",
	}
	byID := map[string]map[string]any{}
	for idx, item := range decoded.Deliveries {
		if len(item) != len(wantKeys) {
			t.Fatalf("delivery[%d] key count = %d, want %d: %v", idx, len(item), len(wantKeys), item)
		}
		for k := range item {
			for _, r := range k {
				if unicode.IsUpper(r) {
					t.Fatalf("delivery[%d] emitted non-snake_case key %q: %s", idx, k, listRaw)
				}
			}
		}
		for _, want := range wantKeys {
			if _, ok := item[want]; !ok {
				t.Fatalf("delivery[%d] missing required snake_case key %q: %s", idx, want, listRaw)
			}
		}
		idVal, _ := item["id"].(string)
		byID[idVal] = item
	}

	d1 := byID[job1.DeliveryID]
	if d1 == nil {
		t.Fatalf("missing delivered record %s in %s", job1.DeliveryID, listRaw)
	}
	if d1["status"] != "delivered" || d1["response_status"] != float64(http.StatusNoContent) || d1["error_category"] != "" {
		t.Fatalf("unexpected delivered record: %v", d1)
	}
	if deliveredAt, _ := d1["delivered_at_ms"].(float64); deliveredAt <= 0 {
		t.Fatalf("delivered record delivered_at_ms = %v, want > 0", d1["delivered_at_ms"])
	}

	d2 := byID[job2.DeliveryID]
	if d2 == nil {
		t.Fatalf("missing failed record %s in %s", job2.DeliveryID, listRaw)
	}
	if d2["status"] != "failed" || d2["response_status"] != float64(0) || d2["error_category"] != "endpoint_disabled" {
		t.Fatalf("unexpected failed record: %v", d2)
	}
	if deliveredAt, _ := d2["delivered_at_ms"].(float64); deliveredAt != 0 {
		t.Fatalf("failed record delivered_at_ms = %v, want 0", d2["delivered_at_ms"])
	}
}
