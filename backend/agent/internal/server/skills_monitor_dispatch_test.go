package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestAllBuiltinSkillsProduceRunnableItems is the SF-02 end-to-end lock: every
// builtin capability can still be selected from the inspection run API and
// produces a real item driven only by its Skill package. The six packages that
// declare scripts/inspect.sh run that script on the target Agent (CollectorMethod
// skill_script); the two AI-driven packages stay on the agent prompt path
// (agent_skill) instead of silently disappearing.
func TestAllBuiltinSkillsProduceRunnableItems(t *testing.T) {
	f := monitorEngineFixture(t)
	operator := f.user(t, "skill-dispatch-op", ScopeGrant{Cluster: "dev", Instance: "local"})
	t.Setenv("DOOPS_AGENT_SKILLS_ROOT", t.TempDir())
	// Point the agent-prompt path at a dead local port so the two AI-driven
	// packages fail fast instead of depending on a developer doagent service.
	t.Setenv("DO_AGENT_URL", "http://127.0.0.1:1")
	f.httpTimeout = 60 * time.Second
	inventoryTestAgent(t, f, "local")

	slugs := make([]string, 0, len(builtinSystemSkills))
	wantMethod := map[string]string{}
	for i := range builtinSystemSkills {
		spec := builtinSystemSkills[i]
		slugs = append(slugs, spec.Slug)
		if spec.Entrypoint != "" {
			wantMethod[spec.Slug] = "skill_script"
		} else {
			wantMethod[spec.Slug] = "agent_skill"
		}
	}

	body, err := json.Marshal(map[string]any{
		"targets": []map[string]string{{"cluster": "dev", "instance": "local"}},
		"skills":  slugs,
	})
	if err != nil {
		t.Fatalf("encode run request: %v", err)
	}
	runRaw := f.http(t, operator, "POST", "/v1/monitor/runs", string(body), 200)
	var runResp struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(runRaw, &runResp); err != nil {
		t.Fatalf("decode builtin skill run: %v", err)
	}
	if len(runResp.Items) != len(slugs) {
		t.Fatalf("expected %d items (one per builtin Skill), got %d: %+v", len(slugs), len(runResp.Items), runResp.Items)
	}
	seen := map[string]bool{}
	for _, it := range runResp.Items {
		want, ok := wantMethod[it.AssetID]
		if !ok {
			t.Fatalf("unexpected run item asset %q: %+v", it.AssetID, it)
		}
		seen[it.AssetID] = true
		if it.AssetKind != "skill" || it.Strategy != "skill:"+it.AssetID {
			t.Fatalf("item %s not bound to its Skill package: kind=%s strategy=%s", it.AssetID, it.AssetKind, it.Strategy)
		}
		if it.CollectorMethod != want {
			t.Fatalf("item %s collector = %q, want %q (evidence=%+v)", it.AssetID, it.CollectorMethod, want, it.Evidence)
		}
		if it.Evidence["skill_slug"] != it.AssetID {
			t.Fatalf("item %s evidence skill_slug = %v", it.AssetID, it.Evidence["skill_slug"])
		}
		if it.Evidence["synced"] != true {
			t.Fatalf("expected skill %s materialized on the target Agent: %+v", it.AssetID, it.Evidence)
		}
		if want == "skill_script" {
			if ep, _ := it.Evidence["entrypoint"].(string); ep != "scripts/inspect.sh" {
				t.Fatalf("item %s entrypoint evidence = %v", it.AssetID, it.Evidence["entrypoint"])
			}
		} else if it.Reason != "skill_synced_ai_unavailable" {
			t.Fatalf("AI-driven skill %s must report the missing model path, got status=%s reason=%s", it.AssetID, it.Status, it.Reason)
		}
	}
	for _, slug := range slugs {
		if !seen[slug] {
			t.Fatalf("builtin Skill %s produced no runnable item: %+v", slug, runResp.Items)
		}
	}
}

// TestMonitorRunLegacyStrategyCompatibility keeps pre-SF-02 data readable: a
// historic run item still carries Strategy=vm_host plus a skill_slug evidence
// entry. Reading it and retesting it must both succeed (no 500) after the
// builtin_strategy side channel was removed.
func TestMonitorRunLegacyStrategyCompatibility(t *testing.T) {
	f := monitorEngineFixture(t)
	operator := f.user(t, "legacy-op", ScopeGrant{Cluster: "dev", Instance: "local"})
	now := time.Now().UnixMilli()
	run := monitorRunRecord{
		ID:           "run_legacy_compat",
		TriggerKind:  "manual",
		TriggeredBy:  "legacy-op",
		Status:       "critical",
		TotalTargets: 1,
		TotalAssets:  1,
		FailedChecks: 1,
		StartedAtMs:  now,
		FinishedAtMs: now,
		Summary:      "legacy builtin_strategy run",
	}
	if err := f.store.CreateMonitorRun(context.Background(), run); err != nil {
		t.Fatalf("create legacy run: %v", err)
	}
	items := []monitorRunItemRecord{{
		RunID:           run.ID,
		Cluster:         "dev",
		Instance:        "local",
		AssetID:         "legacy-vm",
		AssetName:       "legacy vm",
		AssetKind:       "vm",
		Strategy:        "vm_host",
		CollectorMethod: "agent_local",
		Status:          "critical",
		Reason:          "legacy_reason",
		StartedAtMs:     now,
		FinishedAtMs:    now,
		Evidence:        map[string]any{"skill_slug": "inspect-vm-host"},
	}}
	if _, err := f.store.PersistMonitorRunResults(context.Background(), run, items, nil, nil); err != nil {
		t.Fatalf("persist legacy run: %v", err)
	}

	detailRaw := f.http(t, operator, "GET", "/v1/monitor/runs/"+run.ID, "", 200)
	var detail struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(detailRaw, &detail); err != nil {
		t.Fatalf("decode legacy detail: %v", err)
	}
	if len(detail.Items) != 1 || detail.Items[0].Strategy != "vm_host" {
		t.Fatalf("legacy item not preserved: %+v", detail.Items)
	}

	retestRaw := f.http(t, operator, "POST", "/v1/monitor/runs", `{"retest_run_id":"`+run.ID+`"}`, 200)
	var retest struct {
		Run   monitorRunRecord       `json:"run"`
		Items []monitorRunItemRecord `json:"items"`
	}
	if err := json.Unmarshal(retestRaw, &retest); err != nil {
		t.Fatalf("decode legacy retest: %v", err)
	}
	if retest.Run.TriggerKind != "retest" || len(retest.Items) == 0 {
		t.Fatalf("expected a retest run carrying the legacy asset, got run=%+v items=%+v", retest.Run, retest.Items)
	}
}
