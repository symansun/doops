package main

import (
	"strings"
	"testing"
)

func TestMergeLiveGatewayRoutesAddsOnlyMissingAuthorizedTargets(t *testing.T) {
	existing := []Server{
		{
			Name: "gw-jm", Aliases: []string{"jm"}, Use: "hand written",
			Gateway: "https://doops.example.com/", Cluster: "doops-jm", Instance: "jm-228",
		},
		{
			Name: "old-offline", Use: "keep me",
			Gateway: "https://doops.example.com", Cluster: "doops-89", Instance: "master-node",
		},
	}
	live := []GatewayTarget{
		{Cluster: "unicom", Instance: "unicom-1"},
		{Cluster: "doops-jm", Instance: "jm-228"},
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
	if len(updated) != 3 || updated[0].Name != "gw-jm" || updated[0].Use != "hand written" || len(updated[0].Aliases) != 1 {
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
		{Name: "jm", Gateway: "https://doops.example.com", Cluster: "doops-jm", Instance: "jm-228"},
		{Name: "zy", Aliases: []string{"zheyin"}, Gateway: "https://doops.example.com/", Cluster: "doops-zheyin", Instance: "node-239"},
		{Name: "oilan", Gateway: "https://oilan.example.com", Cluster: "oilan", Instance: "oilan"},
	}
	one, err := syncGatewaySeeds(servers, "zheyin")
	if err != nil || len(one) != 1 || one[0].Name != "zy" {
		t.Fatalf("alias seed = %#v, %v", one, err)
	}
	all, err := syncGatewaySeeds(servers, "")
	if err != nil || len(all) != 2 || all[0].Name != "jm" || all[1].Name != "oilan" {
		t.Fatalf("all seeds = %#v, %v", all, err)
	}
	if _, err := syncGatewaySeeds(servers, "missing"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing target error = %v", err)
	}
}
