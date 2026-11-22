package server

import (
	"bytes"
	"encoding/json"
	"net/url"
	"slices"
	"testing"
)

func TestMachineInventoryTagLifecycleAndVisibility(t *testing.T) {
	f := newMachineFixture(t)
	if err := f.store.MarkAgentOnline(AgentStatus{Cluster: "ops", Instance: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.GrantUser(f.userID, ScopeGrant{Cluster: "ops", Instance: "test"}); err != nil {
		t.Fatal(err)
	}
	m := machineInventoryInput{AssetID: "visible", Name: "test", Kind: "vm", Cluster: "ops", Instance: "test", Tags: []string{" QA ", "测试", "qa", "院系=音乐工程", "Env=Prod"}}
	saved := f.save(t, "create", m, 0, 200)
	if !slices.Equal(saved.Tags, []string{"env=prod", "qa", "测试", "院系=音乐工程"}) {
		t.Fatal(saved.Tags)
	}
	f.save(t, "create", machineInventoryInput{AssetID: "unbound", Name: "hidden", Kind: "physical", Tags: []string{"qa"}}, 0, 200)
	check := func(token, query string, ids ...string) {
		t.Helper()
		raw := f.request(t, token, "GET", query, nil, 200)
		var response struct{ Machines []inventoryMachine }
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, row := range response.Machines {
			got = append(got, row.AssetID)
		}
		if !slices.Equal(got, ids) {
			t.Fatalf("got %v, want %v", got, ids)
		}
		if token == f.user && bytes.Contains(raw, []byte("login_accounts")) {
			t.Fatal("login accounts leaked")
		}
	}
	check(f.admin, "?tag=qa", "unbound", "visible")
	check(f.user, "?tag=QA", "visible")
	check(f.admin, "?tag=qa&tag="+url.QueryEscape("测试"), "visible")
	check(f.user, "?tag="+url.QueryEscape("院系=音乐工程"), "visible")
	check(f.user, "?tag=env%3Dprod", "visible")
	mSecret := m
	mSecret.Tags = []string{"password=canary"}
	f.save(t, "update", mSecret, 1, 400)
	check(f.user, "?tag=q")
	f.request(t, f.user, "POST", "", map[string]any{"action": "update", "machine": m, "expected_revision": 1}, 403)
	m.Tags = nil
	if !slices.Equal(f.save(t, "update", m, 1, 200).Tags, saved.Tags) {
		t.Fatal("an older client erased labels")
	}
	m.Tags = []string{"bad/format"}
	f.save(t, "update", m, 2, 400)
	f.request(t, f.user, "GET", "?tag=", nil, 400)
	m.Tags = []string{}
	f.save(t, "update", m, 1, 409)
	check(f.user, "?tag=qa", "visible")
	f.save(t, "update", m, 2, 200)
	check(f.user, "?tag=qa")
	check(f.user, "", "visible")
}
