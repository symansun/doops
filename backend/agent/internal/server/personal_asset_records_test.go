package server

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestPersonalAssetRecords(t *testing.T) {
	f := newMachineFixture(t)
	call := func(token, query, body string, want int) string {
		t.Helper()
		method := "GET"
		if body != "" {
			method = "POST"
		}
		r, err := http.NewRequest(method, f.server.URL+"/v1/assets/records"+query, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := f.server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != want {
			t.Fatalf("%s: got %d want %d: %s", method, res.StatusCode, want, out)
		}
		return string(out)
	}
	q := "?kind=database"
	call(f.user, q, "", 200)
	f.exec(t, `DROP TABLE personal_asset_records`)
	call(f.user, q, "", 503)
	b, err := os.ReadFile("../../../docs/migrations/personal_asset_records.sql")
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, string(b))
	call("", q, "", 401)
	create := `{"op":"create","kind":"database","id":"db-1","name":"测试库","environment":"QA"}`
	update := `{"op":"update","kind":"database","id":"db-1","name":"QA-db","revision":1}`
	call(f.user, "", create, 200)
	call(f.user, "", create, 409)
	if !strings.Contains(call(f.user, q, "", 200), "测试库") {
		t.Fatal("missing saved row")
	}
	if strings.Contains(call(f.admin, q, "", 200), "测试库") {
		t.Fatal("another user's metadata was returned")
	}
	call(f.admin, "", update, 409)
	call(f.user, "", update, 200)
	call(f.user, "", update, 409)
	if out := call(f.user, "", strings.Replace(create, "测试库", "dgw_user_synthetic-canary", 1), 400); strings.Contains(out, "synthetic-canary") {
		t.Fatal("invalid value leaked")
	}
	call(f.user, "", strings.Replace(create, "database", "credentials", 1), 400)
	call(f.user, "", strings.Replace(create, `"op"`, `"owner":"another","op"`, 1), 400)
	del := `{"op":"delete","kind":"database","id":"db-1","revision":2}`
	f.exec(t, "CREATE TRIGGER stop_personal_audit BEFORE UPDATE ON audit_events WHEN NEW.status='success' BEGIN SELECT RAISE(ABORT,'unavailable'); END")
	call(f.user, "", del, 503)
	if !strings.Contains(call(f.user, q, "", 200), "QA-db") {
		t.Fatal("unaudited deletion persisted")
	}
	f.exec(t, "DROP TRIGGER stop_personal_audit")
	call(f.user, "", del, 200)
	if strings.Contains(call(f.user, q, "", 200), "db-1") {
		t.Fatal("row not removed")
	}
}
