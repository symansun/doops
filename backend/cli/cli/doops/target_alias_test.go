package main

import "testing"

func TestFindServerMatchesNameAndAliases(t *testing.T) {
	servers := []Server{
		{Name: "demo", Aliases: []string{"jy", "oilan"}, Gateway: "https://gateway.example.com", Cluster: "doops-demo", Instance: "demo-228"},
		{Name: "ali", Aliases: []string{"aliyun"}, Gateway: "https://gateway.example.com", Cluster: "doops-ali", Instance: "master"},
	}

	if got := findServer(servers, "demo"); got == nil || got.Name != "demo" {
		t.Fatalf("expected canonical demo target, got %#v", got)
	}
	if got := findServer(servers, "jy"); got == nil || got.Name != "demo" {
		t.Fatalf("expected alias jy to resolve to demo, got %#v", got)
	}
	if got := findServer(servers, "oilan"); got == nil || got.Name != "demo" {
		t.Fatalf("expected alias oilan to resolve to demo, got %#v", got)
	}
	if got := findServer(servers, "aliyun"); got == nil || got.Name != "ali" {
		t.Fatalf("expected alias aliyun to resolve to ali, got %#v", got)
	}
}

func TestNormalizeAliasesSplitsCommasAndDeduplicates(t *testing.T) {
	got := normalizeAliases([]string{"demo, jy", "demo", " oilan "})
	want := []string{"demo", "jy", "oilan"}
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
		{Name: "demo", Aliases: []string{"jy", "oilan"}},
	}
	if got := findAliasConflict(servers, Server{Name: "ali", Aliases: []string{"jy"}}); got != "demo" {
		t.Fatalf("expected alias conflict with demo, got %q", got)
	}
	if got := findAliasConflict(servers, Server{Name: "demo", Aliases: []string{"new"}}); got != "" {
		t.Fatalf("updating canonical target should not conflict with itself, got %q", got)
	}
}

func TestRouteConflictDetection(t *testing.T) {
	servers := []Server{
		{Name: "oilan", Gateway: "https://gateway.example.com", Cluster: "doops-oilan", Instance: "node-1"},
	}

	duplicate := Server{Name: "oilan-admin", Gateway: "https://gateway.example.com/", Cluster: "doops-oilan", Instance: "node-1"}
	if got := findRouteConflict(servers, duplicate); got != "oilan" {
		t.Fatalf("expected route conflict with oilan, got %q", got)
	}

	update := Server{Name: "oilan", Gateway: "https://gateway.example.com", Cluster: "doops-oilan", Instance: "node-1"}
	if got := findRouteConflict(servers, update); got != "" {
		t.Fatalf("updating the canonical target should not conflict, got %q", got)
	}

	otherInstance := Server{Name: "oilan-worker", Gateway: "https://gateway.example.com", Cluster: "doops-oilan", Instance: "node-2"}
	if got := findRouteConflict(servers, otherInstance); got != "" {
		t.Fatalf("different instance should not conflict, got %q", got)
	}
}
