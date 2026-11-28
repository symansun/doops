package main

import (
	"os"
	"strings"
	"testing"
)

func TestMergeLiveGatewayRoutesAddsOnlyMissingAuthorizedTargets(t *testing.T) {
	existing := []Server{
		{
			Name: "gw-demo", Aliases: []string{"demo"}, Use: "hand written",
			Gateway: "https://doops.example.com/", Cluster: "doops-demo", Instance: "demo-228",
		},
		{
			Name: "old-offline", Use: "keep me",
			Gateway: "https://doops.example.com", Cluster: "doops-89", Instance: "master-node",
		},
	}
	live := []GatewayTarget{
		{Cluster: "unicom", Instance: "unicom-1"},
		{Cluster: "doops-demo", Instance: "demo-228"},
		{Cluster: " ", Instance: "blank"},
		{Cluster: "*", Instance: "*"},
		{Cluster: "unicom", Instance: "unicom-1"},
	}

	updated, added := mergeLiveGatewayRoutes(existing, "https://doops.example.com", live)
	if len(added) != 1 || added[0].Name != "unicom-1" || added[0].Cluster != "unicom" || added[0].Instance != "unicom-1" {
		t.Fatalf("added = %#v", added)
	}
	if added[0].Gateway != "https://doops.example.com" || added[0].Use != "Synced from Gateway" || len(added[0].Aliases) != 0 {
		t.Fatalf("new route changed identity fields: %#v", added[0])
	}
	if len(updated) != 3 || updated[0].Name != "gw-demo" || updated[0].Use != "hand written" || len(updated[0].Aliases) != 1 {
		t.Fatalf("existing route was rewritten: %#v", updated[0])
	}
	if updated[1].Name != "old-offline" {
		t.Fatalf("offline local route was removed: %#v", updated)
	}
	if findSameRoute(updated, "https://doops.example.com/", "unicom", "unicom-1") != "unicom-1" {
		t.Fatal("trailing-slash gateway spelling did not match the new route")
	}
}

func TestMergeLiveGatewayRoutesAvoidsNameAndAliasCollisions(t *testing.T) {
	existing := []Server{{
		Name: "taken", Aliases: []string{"unicom-1"},
		Gateway: "https://other.example.com", Cluster: "other", Instance: "node",
	}}
	_, added := mergeLiveGatewayRoutes(existing, "https://doops.example.com", []GatewayTarget{
		{Cluster: "unicom", Instance: "unicom-1"},
	})
	if len(added) != 1 || added[0].Name != "unicom-unicom-1" {
		t.Fatalf("name collision was not avoided: %#v", added)
	}
	if routeNameFree(append(existing, added...), "unicom-unicom-1") {
		t.Fatal("added name is still considered free")
	}
}

func TestSanitizeRouteNameKeepsAStableTargetName(t *testing.T) {
	if got := sanitizeRouteName(" unicom/1 "); got != "unicom-1" {
		t.Fatalf("sanitize = %q", got)
	}
	if got := sanitizeRouteName("中文 node"); got != "node" {
		t.Fatalf("sanitize letters = %q", got)
	}
	if strings.Trim(sanitizeRouteName("///"), "-") != "" {
		t.Fatalf("empty sanitize = %q", sanitizeRouteName("///"))
	}
}

func TestSyncGatewaySeedsSelectsOneGatewayOrEveryGateway(t *testing.T) {
	servers := []Server{
		{Name: "demo", Gateway: "https://doops.example.com", Cluster: "doops-demo", Instance: "demo-228"},
		{Name: "zy", Aliases: []string{"zheyin"}, Gateway: "https://doops.example.com/", Cluster: "doops-zheyin", Instance: "node-239"},
		{Name: "oilan", Gateway: "https://oilan.example.com", Cluster: "oilan", Instance: "oilan"},
	}
	one, err := syncGatewaySeeds(servers, "zheyin")
	if err != nil || len(one) != 1 || one[0].Name != "zy" {
		t.Fatalf("alias seed = %#v, %v", one, err)
	}
	all, err := syncGatewaySeeds(servers, "")
	if err != nil || len(all) != 2 || all[0].Name != "demo" || all[1].Name != "oilan" {
		t.Fatalf("all seeds = %#v, %v", all, err)
	}
	if _, err := syncGatewaySeeds(servers, "missing"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing target error = %v", err)
	}
}

func TestPruneOfflineGatewayRoutesKeepsOtherGatewaysAndLiveTargets(t *testing.T) {
	servers := []Server{
		{Name: "live-a", Gateway: "https://gw-a.example.com/", Cluster: "c1", Instance: "i1"},
		{Name: "stale-a", Gateway: "https://gw-a.example.com", Cluster: "c1", Instance: "old"},
		{Name: "other-gw", Gateway: "https://gw-b.example.com", Cluster: "c2", Instance: "i2"},
	}
	kept, pruned := pruneOfflineGatewayRoutes(servers, "https://gw-a.example.com", []GatewayTarget{
		{Cluster: "c1", Instance: "i1"},
	})
	if len(pruned) != 1 || pruned[0].Name != "stale-a" {
		t.Fatalf("pruned = %#v, want [stale-a]", pruned)
	}
	if len(kept) != 2 || kept[0].Name != "live-a" || kept[1].Name != "other-gw" {
		t.Fatalf("kept = %#v, want [live-a other-gw]", kept)
	}
}

func TestRunSyncPruneDryRunAndFailureSafety(t *testing.T) {
	servers := []Server{
		{Name: "live-a", Gateway: "https://gw-a.example.com", Cluster: "c1", Instance: "i1"},
		{Name: "stale-a", Gateway: "https://gw-a.example.com", Cluster: "c1", Instance: "gone"},
		{Name: "gw-b-node", Gateway: "https://gw-b.example.com", Cluster: "c2", Instance: "b1"},
	}
	resolve := func(Server) (string, error) { return "tok", nil }

	t.Run("default sync keeps stale route and prints hint", func(t *testing.T) {
		var saved []Server
		var out strings.Builder
		err := runSyncWithFetcher([]string{"--target", "live-a"}, servers, nil, &out, resolve,
			func(gw, tok string) ([]GatewayTarget, error) {
				return []GatewayTarget{{Cluster: "c1", Instance: "i1"}}, nil
			},
			func(s []Server) error { saved = s; return nil },
		)
		if err != nil {
			t.Fatalf("default sync: %v", err)
		}
		if len(saved) != 0 {
			t.Fatalf("default sync must not persist when nothing added: %#v", saved)
		}
		if !strings.Contains(out.String(), "stale-a") || !strings.Contains(out.String(), "doops sync --prune --dry-run") {
			t.Fatalf("missing stale hint:\n%s", out.String())
		}
	})

	t.Run("prune dry-run previews without saving", func(t *testing.T) {
		persisted := false
		var out strings.Builder
		err := runSyncWithFetcher([]string{"--target", "live-a", "--prune", "--dry-run"}, servers, nil, &out, resolve,
			func(gw, tok string) ([]GatewayTarget, error) {
				return []GatewayTarget{{Cluster: "c1", Instance: "i1"}, {Cluster: "c1", Instance: "new-node"}}, nil
			},
			func(s []Server) error { persisted = true; return nil },
		)
		if err != nil {
			t.Fatalf("dry-run prune: %v", err)
		}
		if persisted {
			t.Fatal("--dry-run must not persist changes")
		}
		got := out.String()
		if !strings.Contains(got, "Would add local route 'new-node'") || !strings.Contains(got, "Would prune local route 'stale-a'") {
			t.Fatalf("unexpected dry-run output:\n%s", got)
		}
	})

	t.Run("prune saves removal and skips failed gateways", func(t *testing.T) {
		var saved []Server
		var out strings.Builder
		err := runSyncWithFetcher([]string{"--prune"}, servers, nil, &out, resolve,
			func(gw, tok string) ([]GatewayTarget, error) {
				if strings.Contains(gw, "gw-b") {
					return nil, os.ErrPermission
				}
				return []GatewayTarget{{Cluster: "c1", Instance: "i1"}}, nil
			},
			func(s []Server) error { saved = append([]Server(nil), s...); return nil },
		)
		if err == nil || !strings.Contains(err.Error(), "permission") {
			t.Fatalf("expected gw-b failure, got %v", err)
		}
		// gw-a stale route pruned, gw-b route MUST survive because gw-b fetch failed.
		if len(saved) != 2 || saved[0].Name != "live-a" || saved[1].Name != "gw-b-node" {
			t.Fatalf("saved = %#v, want [live-a gw-b-node]", saved)
		}
		if !strings.Contains(out.String(), "Pruned local route 'stale-a' (c1/gone).") {
			t.Fatalf("output missing prune line:\n%s", out.String())
		}
	})
}
