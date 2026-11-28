package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCatalogBusinessWorkflows(t *testing.T) {
	f := newMachineFixture(t)
	hub := NewGatewayHub(f.store, GatewayHubOptions{})
	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)
	for _, file := range []string{"cluster_inventory.sql", "asset_catalog.sql"} {
		data, err := os.ReadFile("../../../docs/migrations/" + file)
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, string(data))
	}
	call := func(token, path string, body map[string]any, want int) string {
		t.Helper()
		m := "GET"
		var b []byte
		if body != nil {
			m = "POST"
			b, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(m, "/v1/assets/"+path, strings.NewReader(string(b)))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: wanted %d got %d: %s", m, path, want, w.Code, w.Body)
		}
		return w.Body.String()
	}
	idOf := func(s string) string {
		var v struct{ ID string }
		if err := json.Unmarshal([]byte(s), &v); err != nil || v.ID == "" {
			t.Fatal("missing created id")
		}
		return v.ID
	}
	entry := map[string]any{"kind": "database", "op": "create", "name": "orders", "details": map[string]string{"engine": "PostgreSQL", "host": "db.example.test", "port": "5432", "database": "orders"}}
	id := idOf(call(f.user, "catalog", entry, 200))
	call(f.user, "catalog", entry, 409)
	if strings.Contains(call(f.admin, "catalog?kind=database", nil, 200), id) {
		t.Fatal("private pending record visible")
	}
	entry["op"] = "update"
	entry["id"] = id
	entry["revision"] = 1
	entry["cluster"] = "denied"
	entry["instance"] = "missing"
	call(f.user, "catalog", entry, 403)
	delete(entry, "cluster")
	delete(entry, "instance")
	call(f.admin, "catalog", entry, 409)
	entry["name"] = "订单库"
	call(f.user, "catalog", entry, 200)
	call(f.user, "catalog", entry, 409)
	entry["revision"] = 2
	entry["op"] = "delete"
	f.exec(t, "CREATE TRIGGER deny_catalog_audit BEFORE UPDATE ON audit_events WHEN NEW.status='success' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END")
	call(f.user, "catalog", entry, 503)
	if !strings.Contains(call(f.user, "catalog?kind=database", nil, 200), id) {
		t.Fatal("unaudited deletion persisted")
	}
	f.exec(t, "DROP TRIGGER deny_catalog_audit")
	call(f.user, "catalog", entry, 200)
	pr := map[string]any{"op": "create", "name": "QA cluster", "environment": "QA", "contact": "tests"}
	pid := idOf(call(f.user, "cluster-profiles", pr, 200))
	if strings.Contains(call(f.admin, "cluster-profiles", nil, 200), pid) {
		t.Fatal("pending cluster must stay private")
	}
	pr["id"] = pid
	pr["revision"] = 1
	pr["op"] = "update"
	call(f.admin, "cluster-profiles", pr, 409)
	call(f.user, "cluster-profiles", pr, 200)
	pr["revision"] = 2
	pr["op"] = "delete"
	call(f.user, "cluster-profiles", pr, 200)
	for _, r := range []catalogRow{{Kind: "website", Details: map[string]string{"scheme": "https", "domain": "example.test", "path": "/?token=synthetic"}}, {Kind: "image", Details: map[string]string{"registry": "u:p@host", "repository": "a/b", "version": "main"}}} {
		if _, ok := catalogIdentity(r); ok {
			t.Fatal("invalid resource accepted")
		}
	}
}
