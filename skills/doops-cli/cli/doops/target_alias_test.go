package main

import "testing"

func TestFindServerMatchesNameAndAliases(t *testing.T) {
	servers := []Server{
		{Name: "prod", Aliases: []string{"p", "master"}, Gateway: "https://gateway.example.com", Cluster: "cluster-c", Instance: "node-3"},
		{Name: "staging", Aliases: []string{"stg"}, Gateway: "https://gateway.example.com", Cluster: "cluster-d", Instance: "master"},
	}

	if got := findServer(servers, "prod"); got == nil || got.Name != "prod" {
		t.Fatalf("expected canonical prod target, got %#v", got)
	}
	if got := findServer(servers, "p"); got == nil || got.Name != "prod" {
		t.Fatalf("expected alias p to resolve to prod, got %#v", got)
	}
	if got := findServer(servers, "master"); got == nil || got.Name != "prod" {
		t.Fatalf("expected alias master to resolve to prod, got %#v", got)
	}
	if got := findServer(servers, "stg"); got == nil || got.Name != "staging" {
		t.Fatalf("expected alias stg to resolve to staging, got %#v", got)
	}
}

func TestNormalizeAliasesSplitsCommasAndDeduplicates(t *testing.T) {
	got := normalizeAliases([]string{"prod, p", "prod", " master "})
	want := []string{"prod", "p", "master"}
	if len(got) != len(want) {
		t.Fatalf("alias count mismatch: %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("alias mismatch at %d: want %q got %q", i, want[i], got[i])
		}
	}
}

func TestAliasConflictDetection(t *testing.T) {
	servers := []Server{
		{Name: "prod", Aliases: []string{"p", "master"}},
	}
	if got := findAliasConflict(servers, Server{Name: "staging", Aliases: []string{"p"}}); got != "prod" {
		t.Fatalf("expected alias conflict with prod, got %q", got)
	}
	if got := findAliasConflict(servers, Server{Name: "prod", Aliases: []string{"new"}}); got != "" {
		t.Fatalf("updating canonical target should not conflict with itself, got %q", got)
	}
}
