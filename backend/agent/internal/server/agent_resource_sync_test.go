package server

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestMergeAgentResourcesStates(t *testing.T) {
	declared := []declaredAgentResource{
		{key: "web-1", identity: "vm-1", resource: targetManifestResource{Ref: "machine/web-1", Kind: "machine", Name: "web-1"}},
		{key: "web-2", identity: "vm-1", resource: targetManifestResource{Ref: "machine/web-2", Kind: "machine", Name: "web-2"}},
		{key: "web-3", identity: "vm-1", resource: targetManifestResource{Ref: "machine/web-3", Kind: "machine", Name: "web-3"}},
	}
	observed := []agentObservationRow{
		{agentResourceObservation: agentResourceObservation{Kind: "machine", Key: "web-1", Name: "web-1", Identity: "vm-1"}, seen: "2026-11-24T01:00:00Z"},
		{agentResourceObservation: agentResourceObservation{Kind: "machine", Key: "web-2", Name: "web-2", Identity: "vm-9"}, seen: "2026-11-24T01:00:00Z"},
		{agentResourceObservation: agentResourceObservation{Kind: "machine", Key: "web-3", Name: "web-3"}, seen: "2026-11-24T01:00:00Z", misses: 3},
		{agentResourceObservation: agentResourceObservation{Kind: "cluster", Key: "current", Name: "ops", Identity: "id-1"}, seen: "2026-11-24T01:00:00Z"},
		{agentResourceObservation: agentResourceObservation{Kind: "namespace", Key: "team-a", Name: "team-a", Identity: "id-2"}, seen: "2026-11-24T01:00:00Z"},
	}
	out := mergeAgentResources("ops", declared, observed, 3)
	states := map[string]targetManifestResource{}
	for _, r := range out {
		states[r.Ref] = r
	}
	if got := states["machine/web-1"].State; got != "verified" {
		t.Fatalf("declared+observed machine state = %q, want verified", got)
	}
	if got := states["machine/web-2"].State; got != "conflict" {
		t.Fatalf("identity mismatch state = %q, want conflict", got)
	}
	if got := states["machine/web-3"].State; got != "lost" {
		t.Fatalf("missed observation state = %q, want lost", got)
	}
	if got := states["cluster/current"].State; got != "discovered" {
		t.Fatalf("undecleared cluster state = %q, want discovered", got)
	}
	if !states["cluster/current"].CanRegister {
		t.Fatal("observed cluster with identity must be registrable")
	}
	if out := states["namespace/ops/team-a"]; out.State != "discovered" || out.CanRegister {
		t.Fatalf("namespace without registered cluster must not be registrable: %+v", out)
	}
}

func TestBoundTargetManifestTruncatesPerKind(t *testing.T) {
	manifest := targetManifest{Target: targetManifestTarget{Cluster: "ops", Instance: "left"}, Revision: 1}
	for i := 0; i < 3000; i++ {
		manifest.Resources = append(manifest.Resources,
			targetManifestResource{Ref: "machine/m-000000", Kind: "machine", Name: "m-000000-with-a-long-descriptive-name", State: "discovered", Source: []string{"observed"}},
			targetManifestResource{Ref: "site/s-000000", Kind: "site", Name: "s-000000-with-a-long-descriptive-name", State: "discovered", Source: []string{"observed"}},
		)
	}
	if err := boundTargetManifest(&manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.Truncated {
		t.Fatal("oversized manifest must be marked truncated")
	}
	counts := map[string]int{}
	for _, r := range manifest.Resources {
		counts[r.Kind]++
	}
	if counts["machine"] != 50 || counts["site"] != 50 {
		t.Fatalf("truncated counts = %v, want 50 per kind", counts)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if len(data)+160 > targetManifestMaxBytes {
		t.Fatalf("truncated manifest still exceeds budget: %d bytes", len(data))
	}
}

func TestReconcileAgentResourcesPublishesManifest(t *testing.T) {
	f := newMachineFixture(t)
	for _, name := range []string{"agent_resource_sync", "cluster_inventory", "machine_diagnostics", "business_inventory", "asset_catalog", "agent_asset_scope"} {
		data, err := os.ReadFile("../../../docs/migrations/" + name + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		f.exec(t, string(data))
	}
	connected := time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC)
	available := true
	if err := f.store.MarkAgentOnline(AgentStatus{Cluster: "ops", Instance: "left", Generation: 4, ConnectedAt: connected, Mode: "kubernetes", KubernetesAvailable: &available}); err != nil {
		t.Fatal(err)
	}
	agent := &GatewayAgent{Cluster: "ops", Instance: "left", Generation: 4, ConnectedAt: connected}
	snapshot := &agentResourceSnapshot{
		Mode:                "kubernetes",
		KubernetesAvailable: true,
		Complete:            []string{"cluster", "namespace"},
		Resources: []agentResourceObservation{
			{Kind: "cluster", Key: "current", Name: "ops", Identity: "id-1"},
			{Kind: "namespace", Key: "team-a", Name: "team-a", Identity: "id-2"},
		},
	}
	snapshot.canonicalize()
	ctx := context.Background()
	manifest, err := f.store.reconcileAgentResources(ctx, agent, snapshot, agentResourceDigest(*snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Revision != 1 || manifest.Digest == "" || manifest.Digest != manifest.contentDigest() {
		t.Fatalf("first manifest = revision %d digest %q", manifest.Revision, manifest.Digest)
	}
	if manifest.Target.Mode != "kubernetes" || manifest.Target.KubernetesAvailable == nil || !*manifest.Target.KubernetesAvailable {
		t.Fatalf("manifest target facts not published: %+v", manifest.Target)
	}
	if len(manifest.Resources) != 2 || manifest.Counts["cluster"] != 1 || manifest.Counts["namespace"] != 1 {
		t.Fatalf("manifest resources = %+v", manifest.Resources)
	}
	again, err := f.store.reconcileAgentResources(ctx, agent, snapshot, agentResourceDigest(*snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if again.Revision != manifest.Revision || again.Digest != manifest.Digest {
		t.Fatalf("identical observation bumped revision %d -> %d", manifest.Revision, again.Revision)
	}
	if _, err := f.store.reconcileAgentResources(ctx, agent, snapshot, "sha256:deadbeef"); err == nil {
		t.Fatal("digest mismatch must be rejected")
	}
	reduced := *snapshot
	reduced.Resources = reduced.Resources[:1]
	reduced.canonicalize()
	next, err := f.store.reconcileAgentResources(ctx, agent, &reduced, agentResourceDigest(reduced))
	if err != nil {
		t.Fatal(err)
	}
	if next.Revision != manifest.Revision+1 || next.Counts["namespace"] != 0 {
		t.Fatalf("dropped namespace did not republish: revision %d counts %v", next.Revision, next.Counts)
	}
}
