package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMachinesMetadataOnly(t *testing.T) {
	const canary = "synthetic-private-value"
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/assets/machines" || strings.Join(r.URL.Query()["tag"], ",") != "test,blue" || r.Header.Get("Authorization") != "Bearer synthetic-session" {
			t.Error("incorrect inventory request")
		}
		w.Write([]byte(`{"machines":[{"asset_id":"test-vm","tags":["test","blue"],"login_accounts":[{"username":"` + canary + `"}],"password":"` + canary + `"}]}`))
	}))
	defer gateway.Close()
	dir := t.TempDir()
	t.Setenv("DOOPS_CONFIG", filepath.Join(dir, "config.json"))
	a := AuthStore{}
	a.SetGateway(gateway.URL, "synthetic-session")
	data, err := json.Marshal(&a)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "auth.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runMachines([]string{"--target", "route", "--tag", "test", "--tag", "blue"}, []Server{{Name: "route", Gateway: gateway.URL, Cluster: "a", Instance: "b"}}, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "test-vm") || strings.Contains(out.String(), canary) || strings.Contains(out.String(), "login_accounts") {
		t.Fatal("unexpected metadata projection")
	}
}
