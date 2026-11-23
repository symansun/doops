package server

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/user/doops/agent/api"
)

type monitorRunPlan struct {
	ScheduleID   string
	ScheduleName string
	TriggerKind  string // scheduled | manual | retest
	TriggeredBy  string
	Auth         TokenAuth
	ScopeMode    string // all_online | targets | tags
	Targets      []monitorTargetRef
	Tags         []string
	Skills       []string
	Prompt       string
	Command      string
	Thresholds   api.MonitorInspectThresholds
	RetestItems  map[string][]api.MonitorInspectAsset // keyed by cluster/instance
}

func (h *GatewayHub) StartMonitorRunner(tickInterval time.Duration) {
	if tickInterval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			h.RunDueMonitorSchedules(ctx, time.Now().UTC())
			cancel()
		}
	}()
	log.Printf("[gateway] periodic monitor & inspection runner started (tick=%s)", tickInterval)
}

// RunDueMonitorSchedules claims due enabled schedules and executes them.
// Safe to call directly from tests without background goroutines.
func (h *GatewayHub) RunDueMonitorSchedules(ctx context.Context, now time.Time) []monitorRunRecord {
	due, err := h.store.ClaimDueMonitorSchedules(ctx, now, 4)
	if err != nil {
		log.Printf("[gateway] claim due monitor schedules failed: %v", err)
		return nil
	}
	runs := make([]monitorRunRecord, 0, len(due))
	for _, sched := range due {
		if _, busy := h.monitorRunning.LoadOrStore(sched.ID, true); busy {
			continue
		}
		func(s monitorScheduleRecord) {
			defer h.monitorRunning.Delete(s.ID)
			run, _, err := h.ExecuteMonitorRun(ctx, monitorRunPlan{
				ScheduleID:   s.ID,
				ScheduleName: s.Name,
				TriggerKind:  "scheduled",
				TriggeredBy:  "system",
				ScopeMode:    s.ScopeMode,
				Targets:      s.Targets,
				Tags:         s.Tags,
				Skills:       s.Skills,
				Prompt:       s.Prompt,
				Command:      s.Command,
				Thresholds:   s.Thresholds,
			})
			if err != nil {
				log.Printf("[gateway] scheduled monitor run failed (schedule=%s): %v", s.ID, err)
				return
			}
			runs = append(runs, run)
		}(sched)
	}
	return runs
}

func (h *GatewayHub) resolveMonitorPlanTargets(ctx context.Context, plan monitorRunPlan) []monitorTargetRef {
	seen := map[string]bool{}
	out := []monitorTargetRef{}
	isAdmin := plan.TriggeredBy == "system" || (plan.Auth.UserID != "" && h.store.UserIsPlatformAdmin(plan.Auth.UserID))
	allowed := func(cluster, instance string) bool {
		if isAdmin {
			return true
		}
		return plan.Auth.UserID != "" && h.store.UserCanUseTarget(plan.Auth.UserID, cluster, instance)
	}
	add := func(cluster, instance string) {
		cluster = strings.TrimSpace(cluster)
		instance = strings.TrimSpace(instance)
		if cluster == "" || instance == "" || !allowed(cluster, instance) {
			return
		}
		k := tunnelKey(cluster, instance)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, monitorTargetRef{Cluster: cluster, Instance: instance})
	}

	if len(plan.RetestItems) > 0 {
		for k := range plan.RetestItems {
			c, i, ok := strings.Cut(k, "/")
			if ok {
				add(c, i)
			}
		}
		sort.Slice(out, func(i, j int) bool {
			return tunnelKey(out[i].Cluster, out[i].Instance) < tunnelKey(out[j].Cluster, out[j].Instance)
		})
		return out
	}

	switch plan.ScopeMode {
	case "targets":
		for _, t := range plan.Targets {
			add(t.Cluster, t.Instance)
		}
	case "tags":
		if h.store.tableExists(ctx, "inventory_machines") && h.store.tableExists(ctx, "inventory_machine_tags") && len(plan.Tags) > 0 {
			for _, tag := range plan.Tags {
				rows, err := h.store.db.QueryContext(ctx, `SELECT DISTINCT m.cluster, m.instance
					FROM inventory_machines m JOIN inventory_machine_tags t ON t.asset_id=m.asset_id
					WHERE m.cluster<>'' AND m.instance<>'' AND t.tag=?`, strings.ToLower(strings.TrimSpace(tag)))
				if err != nil {
					continue
				}
				for rows.Next() {
					var c, inst string
					if rows.Scan(&c, &inst) == nil {
						add(c, inst)
					}
				}
				rows.Close()
			}
		}
	default: // all_online
		for _, t := range h.ListTargets() {
			add(t.Cluster, t.Instance)
		}
		// Include non-archived registered targets that have bound machines so offline gaps are visible too.
		if statuses, err := h.store.ListAgentStatus(); err == nil {
			for _, st := range statuses {
				if meaningfulInstanceTime(st.ArchivedAt) {
					continue
				}
				if st.Status == "online" || agentInstanceActive(st.Status, st.LastSeen, time.Now().UTC()) {
					add(st.Cluster, st.Instance)
				}
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return tunnelKey(out[i].Cluster, out[i].Instance) < tunnelKey(out[j].Cluster, out[j].Instance)
	})
	return out
}

func (h *GatewayHub) ExecuteMonitorRun(ctx context.Context, plan monitorRunPlan) (monitorRunRecord, []monitorRunItemRecord, error) {
	started := time.Now().UTC()
	runID := "run_" + randomHex(8)
	plan.Skills = normalizeSkillSlugs(plan.Skills)
	plan.Prompt = strings.TrimSpace(plan.Prompt)
	plan.Command = strings.TrimSpace(plan.Command)
	plan.Thresholds = normalizeMonitorThresholds(plan.Thresholds)

	// Resolve the selected Skills. Execution is dispatched purely by each Skill
	// package (entrypoint + exec_mode); builtin_strategy / strategy_id no longer
	// influences the run (SKILL_FIRST_INSPECTION_SPEC.md 1.1 / 2.3, defect D9).
	var (
		selectedSkills   []platformSkillRecord
		drivenSkillSlugs []string
	)
	hasCustomAction := plan.Prompt != "" || plan.Command != ""
	if len(plan.Skills) > 0 && plan.RetestItems == nil {
		lookupAuth := plan.Auth
		if lookupAuth.UserID == "" {
			lookupAuth.UserID = "system"
		}
		for _, slug := range plan.Skills {
			sk, err := h.store.GetPlatformSkill(ctx, lookupAuth, slug)
			if err != nil || !sk.Enabled {
				continue
			}
			drivenSkillSlugs = append(drivenSkillSlugs, sk.Slug)
			selectedSkills = append(selectedSkills, sk)
		}
	}
	if plan.TriggerKind == "" {
		plan.TriggerKind = "manual"
	}
	if plan.TriggeredBy == "" {
		plan.TriggeredBy = plan.Auth.UserID
		if plan.TriggeredBy == "" {
			plan.TriggeredBy = "system"
		}
	}

	targets := h.resolveMonitorPlanTargets(ctx, plan)
	run := monitorRunRecord{
		ID:           runID,
		ScheduleID:   plan.ScheduleID,
		ScheduleName: plan.ScheduleName,
		TriggerKind:  plan.TriggerKind,
		TriggeredBy:  plan.TriggeredBy,
		Status:       "running",
		Skills:       drivenSkillSlugs,
		Prompt:       plan.Prompt,
		Command:      plan.Command,
		TotalTargets: len(targets),
		StartedAtMs:  started.UnixMilli(),
	}
	if err := h.store.CreateMonitorRun(ctx, run); err != nil {
		return run, nil, err
	}

	auditID, _ := h.store.StartAudit(AuditEvent{
		UserID:         plan.TriggeredBy,
		TokenID:        plan.Auth.TokenID,
		Action:         GatewayAction("monitor:run"),
		Session:        "monitor-" + runID,
		CommandSummary: fmt.Sprintf("run=%s trigger=%s targets=%d skills=%s has_prompt=%t has_cmd=%t", runID, plan.TriggerKind, len(targets), strings.Join(drivenSkillSlugs, ","), plan.Prompt != "", plan.Command != ""),
	})

	var (
		mu           sync.Mutex
		allItems     []monitorRunItemRecord
		allSnapshots []monitorResourceSnapshot
		allSeries    []monitorSeriesBridgeEntry
		offlineCount int

		retestSkillSlugs = map[string]bool{}
	)

	hasCommand := plan.Command != ""
	hasPromptOnly := !hasCommand && plan.Prompt != "" && len(selectedSkills) == 0

	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, tgt := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t monitorTargetRef) {
			defer wg.Done()
			defer func() { <-sem }()

			tStart := time.Now()
			var retestAssets []api.MonitorInspectAsset
			if plan.RetestItems != nil {
				retestAssets = plan.RetestItems[tunnelKey(t.Cluster, t.Instance)]
			}
			// A retest carries the asset rows of an older run. The retired native strategies
			// are translated to the Skill package that owns the capability now; nothing is
			// re-dispatched through the legacy strategy path (SKILL_FIRST_INSPECTION_SPEC.md 1.1).
			var (
				retestSkills []platformSkillRecord
				retestSkips  []monitorRetestSkip
			)
			if len(retestAssets) > 0 {
				retestSkills, retestSkips = h.resolveRetestSkills(ctx, plan.Auth, retestAssets)
				if len(retestSkills) > 0 {
					mu.Lock()
					for _, sk := range retestSkills {
						retestSkillSlugs[sk.Slug] = true
					}
					mu.Unlock()
				}
			}
			if len(retestSkills) == 0 && len(retestSkips) == 0 && len(selectedSkills) == 0 && !hasCommand && !hasPromptOnly {
				return
			}

			// Offline Agent check — never silently skipped!
			if h.getAgent(t.Cluster, t.Instance) == nil {
				nowMs := time.Now().UnixMilli()
				mu.Lock()
				offlineCount++
				appendOffline := func(assetID, assetName, assetKind, strategy, collectorMethod string) {
					allItems = append(allItems, monitorRunItemRecord{
						RunID:           runID,
						Cluster:         t.Cluster,
						Instance:        t.Instance,
						AssetID:         assetID,
						AssetName:       assetName,
						AssetKind:       assetKind,
						Strategy:        strategy,
						CollectorMethod: collectorMethod,
						Status:          "offline",
						Reason:          "target_offline",
						StartedAtMs:     tStart.UnixMilli(),
						FinishedAtMs:    nowMs,
						DurationMs:      nowMs - tStart.UnixMilli(),
						Checks: []api.MonitorInspectCheck{{
							ID: "agent.online", Name: "Agent 隧道连通性", Category: "availability",
							Status: "critical", Value: "离线", Threshold: "Agent 在线",
							Detail: fmt.Sprintf("目标 %s/%s 当前离线，无法下发巡检任务", t.Cluster, t.Instance),
						}},
					})
					allSnapshots = append(allSnapshots, monitorResourceSnapshot{
						Cluster: t.Cluster, Instance: t.Instance,
						AssetID: assetID, AssetName: assetName, AssetKind: assetKind,
						Strategy: strategy, CollectorMethod: collectorMethod,
						CollectedAtMs: nowMs, RunID: runID, Status: "offline",
					})
				}
				if hasCommand {
					appendOffline("shell-command", "自定义命令巡检", "command", "command", "shell_command")
				}
				for _, sk := range selectedSkills {
					appendOffline(sk.Slug, sk.Name, "skill", "skill:"+sk.Slug, skillCollectorMethod(sk, hasCustomAction))
				}
				if hasPromptOnly {
					appendOffline("custom-prompt", "提示词巡检", "prompt", "custom_prompt", "agent_prompt")
				}
				for _, sk := range retestSkills {
					appendOffline(sk.Slug, sk.Name, "skill", "skill:"+sk.Slug, skillCollectorMethod(sk, hasCustomAction))
				}
				for _, skip := range retestSkips {
					allItems = append(allItems, monitorRetestSkipItem(runID, t, skip, tStart))
				}
				mu.Unlock()
				return
			}

			if hasCommand {
				item := h.runCommandInspection(ctx, plan.Auth, t, runID, plan.Command, plan.Prompt, selectedSkills)
				mu.Lock()
				allItems = append(allItems, item)
				mu.Unlock()
			} else {
				// Dispatch each selected Skill by its package contract: a declared
				// entrypoint runs its deterministic script, otherwise the AI drives it.
				for _, sk := range selectedSkills {
					var item monitorRunItemRecord
					if resolveSkillDispatchMode(sk, hasCustomAction) == skillExecModeScript {
						item = h.runSkillScriptInspection(ctx, plan.Auth, t, runID, sk)
					} else {
						item = h.runAgentDrivenSkillInspection(ctx, plan.Auth, t, runID, sk, plan.Prompt)
					}
					mu.Lock()
					allItems = append(allItems, item)
					mu.Unlock()
				}
				if hasPromptOnly {
					item := h.runCustomPromptInspection(ctx, plan.Auth, t, runID, plan.Prompt, drivenSkillSlugs)
					mu.Lock()
					allItems = append(allItems, item)
					mu.Unlock()
				}
			}
			// Retest replay: each legacy asset runs through the Skill package that owns
			// the retired native strategy; unmappable assets are reported, not dropped.
			for _, sk := range retestSkills {
				var item monitorRunItemRecord
				if resolveSkillDispatchMode(sk, hasCustomAction) == skillExecModeScript {
					item = h.runSkillScriptInspection(ctx, plan.Auth, t, runID, sk)
				} else {
					item = h.runAgentDrivenSkillInspection(ctx, plan.Auth, t, runID, sk, plan.Prompt)
				}
				mu.Lock()
				allItems = append(allItems, item)
				mu.Unlock()
			}
			for _, skip := range retestSkips {
				item := monitorRetestSkipItem(runID, t, skip, tStart)
				mu.Lock()
				allItems = append(allItems, item)
				mu.Unlock()
			}
		}(tgt)
	}
	wg.Wait()
	recordedSkillSlugs := append([]string(nil), drivenSkillSlugs...)
	for slug := range retestSkillSlugs {
		recordedSkillSlugs = append(recordedSkillSlugs, slug)
	}
	if len(recordedSkillSlugs) > 0 {
		h.store.RecordPlatformSkillRun(ctx, recordedSkillSlugs, time.Now().UnixMilli())
	}

	sort.Slice(allItems, func(i, j int) bool {
		ki := allItems[i].Cluster + "/" + allItems[i].Instance + "/" + allItems[i].AssetKind + "/" + allItems[i].AssetID
		kj := allItems[j].Cluster + "/" + allItems[j].Instance + "/" + allItems[j].AssetKind + "/" + allItems[j].AssetID
		return ki < kj
	})

	// Aggregate run statistics & terminal status.
	hasCritical := false
	hasWarning := false
	hasError := false
	for _, item := range allItems {
		if item.Status == "error" {
			hasError = true
		}
		for _, c := range item.Checks {
			switch c.Status {
			case "pass":
				run.PassedChecks++
			case "warning":
				run.WarningChecks++
				hasWarning = true
			case "critical":
				run.FailedChecks++
				hasCritical = true
			default:
				run.SkippedChecks++
			}
		}
	}
	run.TotalAssets = len(allItems)
	run.OfflineTargets = offlineCount
	finished := time.Now().UTC()
	run.FinishedAtMs = finished.UnixMilli()
	run.DurationMs = finished.Sub(started).Milliseconds()

	switch {
	case len(targets) == 0:
		run.Status = "success"
		run.Summary = "当前范围无可执行的在线或已登记目标"
	case offlineCount == len(targets):
		run.Status = "failed"
		run.Summary = fmt.Sprintf("全部 %d 个目标均离线", offlineCount)
	case offlineCount > 0 || hasError:
		run.Status = "partial"
		run.Summary = fmt.Sprintf("部分完成：离线 %d，通过 %d，告警 %d，严重 %d", offlineCount, run.PassedChecks, run.WarningChecks, run.FailedChecks)
	case hasCritical:
		run.Status = "critical"
		run.Summary = fmt.Sprintf("发现 %d 项严重异常、%d 项告警，通过 %d 项", run.FailedChecks, run.WarningChecks, run.PassedChecks)
	case hasWarning:
		run.Status = "warning"
		run.Summary = fmt.Sprintf("发现 %d 项告警，通过 %d 项", run.WarningChecks, run.PassedChecks)
	default:
		run.Status = "success"
		run.Summary = fmt.Sprintf("全部 %d 项巡检通过，覆盖 %d 个目标", run.PassedChecks, run.TotalTargets)
	}

	written, err := h.store.PersistMonitorRunResults(ctx, run, allItems, allSnapshots, allSeries)
	if err != nil {
		if auditID > 0 {
			_ = h.store.FinishAudit(auditID, AuditFinish{Status: "error", Error: "monitor_persist_failed"})
		}
		return run, allItems, err
	}
	run.MetricsWritten = written
	// Retention runs after the payload is durable: a scan never races the run it
	// just stored. It is throttled, so the common path costs one atomic read.
	h.maybePruneMonitorRetention()
	if auditID > 0 {
		_ = h.store.FinishAudit(auditID, AuditFinish{
			Status:         "success",
			CommandSummary: fmt.Sprintf("run=%s status=%s assets=%d written=%d", run.ID, run.Status, run.TotalAssets, written),
		})
	}
	return run, allItems, nil
}

// runCommandInspection executes an arbitrary shell command on the target Agent via
// the audited doops_shell tool and parses stdout into JSON / Markdown+JSON / text artifacts.
func (h *GatewayHub) runCommandInspection(
	ctx context.Context,
	auth TokenAuth,
	target monitorTargetRef,
	runID string,
	command string,
	prompt string,
	attachedSkills []platformSkillRecord,
) monitorRunItemRecord {
	started := time.Now()
	assetID := "shell-command"
	assetName := "命令巡检"
	assetKind := "command"
	strategy := "command"
	evidence := map[string]any{
		"command": trimTail(sanitizeSecretText(command), 1024),
	}
	if prompt != "" {
		evidence["prompt"] = trimTail(sanitizeSecretText(prompt), 1024)
		assetName = truncateUTF8Head(strings.Split(prompt, "\n")[0], 48)
	}
	if len(attachedSkills) > 0 {
		slugs := make([]string, 0, len(attachedSkills))
		for _, sk := range attachedSkills {
			_ = h.syncOneSkillToAgent(ctx, auth, target, sk)
			slugs = append(slugs, sk.Slug)
		}
		evidence["skills"] = slugs
		evidence["skill_slug"] = attachedSkills[0].Slug
		assetID = attachedSkills[0].Slug
		if prompt == "" {
			assetName = attachedSkills[0].Name
		}
		assetKind = "skill"
		strategy = "skill:" + attachedSkills[0].Slug
	}

	item := monitorRunItemRecord{
		RunID:           runID,
		Cluster:         target.Cluster,
		Instance:        target.Instance,
		AssetID:         assetID,
		AssetName:       assetName,
		AssetKind:       assetKind,
		Strategy:        strategy,
		CollectorMethod: "shell_command",
		StartedAtMs:     started.UnixMilli(),
		Evidence:        evidence,
	}

	callCtx, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 90*time.Second)
	defer cancel()
	rawOut, err := h.RunInternalToolCall(callCtx, target.Cluster, target.Instance, "doops_shell", map[string]any{
		"session_id": "inspect-cmd-" + runID,
		"command":    command,
	})
	item.FinishedAtMs = time.Now().UnixMilli()
	item.DurationMs = time.Since(started).Milliseconds()

	cleaned := stripShellCompletionSuffix(rawOut)
	parsed := extractInspectionArtifact(cleaned, assetName, "command")
	item.ArtifactFormat = parsed.Format
	item.ArtifactMarkdown = parsed.Markdown
	item.ArtifactJSON = parsed.JSON
	item.RawOutput = parsed.Raw
	item.Checks = parsed.Checks
	item.Status = parsed.Status

	if err != nil {
		cleanErr := trimTail(sanitizeSecretText(err.Error()), 240)
		item.Status = "critical"
		item.Reason = "command_failed"
		if item.ArtifactMarkdown == "" {
			item.ArtifactFormat = "markdown"
			item.ArtifactMarkdown = fmt.Sprintf("### 命令执行失败\n\n- **错误**：`%s`\n\n```text\n%s\n```", cleanErr, parsed.Raw)
		}
		item.Checks = []api.MonitorInspectCheck{{
			ID:       "command.exec",
			Name:     assetName,
			Category: "command",
			Status:   "critical",
			Value:    "执行失败",
			Detail:   cleanErr,
		}}
	}
	return item
}

// runAgentDrivenSkillInspection syncs a Skill to the target Agent's /root/.agent/skills/<slug>/
// directory and drives it through doops_agent_prompt (or falls back to a bundled Skill script).
// newSkillRunItem seeds the shared fields of one Skill execution item.
func newSkillRunItem(runID string, target monitorTargetRef, sk platformSkillRecord, collectorMethod string, started time.Time) monitorRunItemRecord {
	return monitorRunItemRecord{
		RunID:           runID,
		Cluster:         target.Cluster,
		Instance:        target.Instance,
		AssetID:         sk.Slug,
		AssetName:       sk.Name,
		AssetKind:       "skill",
		Strategy:        "skill:" + sk.Slug,
		CollectorMethod: collectorMethod,
		StartedAtMs:     started.UnixMilli(),
		Evidence: map[string]any{
			"skill_slug":  sk.Slug,
			"skill_scope": sk.Scope,
			"skill_sha":   sk.SHA256,
		},
	}
}

// markSkillSyncFailure records a fatal Skill-to-Agent sync error on the item.
func markSkillSyncFailure(item monitorRunItemRecord, target monitorTargetRef, sk platformSkillRecord, started time.Time, err error) monitorRunItemRecord {
	item.Status = "error"
	item.Reason = "skill_sync_failed"
	item.FinishedAtMs = time.Now().UnixMilli()
	item.DurationMs = time.Since(started).Milliseconds()
	cleanErr := sanitizeSecretText(err.Error())
	item.ArtifactFormat = "markdown"
	item.ArtifactMarkdown = fmt.Sprintf("### Skill 同步失败 (`%s`)\n\n无法将 Skill 同步至目标 `%s/%s`：%s", sk.Slug, target.Cluster, target.Instance, cleanErr)
	item.Checks = []api.MonitorInspectCheck{{
		ID: "skill.sync", Name: sk.Name + " · 同步下发", Category: sk.Category,
		Status: "critical", Value: "同步失败",
		Detail: "无法将 Skill 同步至目标 Agent：" + cleanErr,
	}}
	return item
}

// skillScriptCommand builds the audited-shell command that runs one Skill
// entrypoint. The Skill root is resolved at execution time from
// DOOPS_AGENT_SKILLS_ROOT (default /root/.agent/skills), so tests and non-root
// deployments share the production code path.
func skillScriptCommand(slug, entrypoint string, target monitorTargetRef, runID string) string {
	return strings.Join([]string{
		`root="${DOOPS_AGENT_SKILLS_ROOT:-/root/.agent/skills}"`,
		`dir="$root"/` + slug,
		`export DOOPS_SKILL_DIR="$dir"`,
		`export DOOPS_SKILL_SLUG=` + shellSingleQuote(slug),
		`export DOOPS_TARGET_CLUSTER=` + shellSingleQuote(target.Cluster),
		`export DOOPS_TARGET_INSTANCE=` + shellSingleQuote(target.Instance),
		`export DOOPS_RUN_ID=` + shellSingleQuote(runID),
		`cd "$dir"`,
		`exec sh "$dir"/` + entrypoint,
	}, " && ")
}

// runSkillScriptInspection runs a Skill's deterministic entrypoint through the
// audited doops_shell tool after syncing the package to the target Agent.
func (h *GatewayHub) runSkillScriptInspection(
	ctx context.Context,
	auth TokenAuth,
	target monitorTargetRef,
	runID string,
	sk platformSkillRecord,
) monitorRunItemRecord {
	started := time.Now()
	item := newSkillRunItem(runID, target, sk, "skill_script", started)
	entrypoint := sk.Entrypoint
	if entrypoint == "" {
		entrypoint = findSkillExecutableScript(sk)
	}
	item.Evidence["entrypoint"] = entrypoint
	item.Evidence["exec_mode"] = resolveSkillExecMode(sk.SkillMD, entrypoint)

	if err := h.syncOneSkillToAgent(ctx, auth, target, sk); err != nil {
		return markSkillSyncFailure(item, target, sk, started, err)
	}
	item.Evidence["synced"] = true

	callCtx, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 90*time.Second)
	defer cancel()
	rawOut, err := h.RunInternalToolCall(callCtx, target.Cluster, target.Instance, "doops_shell", map[string]any{
		"session_id": "skill-script-" + runID + "-" + sk.Slug,
		"command":    skillScriptCommand(sk.Slug, entrypoint, target, runID),
	})
	item.FinishedAtMs = time.Now().UnixMilli()
	item.DurationMs = time.Since(started).Milliseconds()
	cleaned := stripShellCompletionSuffix(rawOut)
	parsed := extractInspectionArtifact(cleaned, sk.Name, sk.Category)
	item.ArtifactFormat = parsed.Format
	item.ArtifactMarkdown = parsed.Markdown
	item.ArtifactJSON = parsed.JSON
	item.RawOutput = parsed.Raw
	item.Checks = parsed.Checks
	item.Status = parsed.Status
	if err != nil {
		item.Status = "critical"
		item.Reason = "skill_script_failed"
		item.Checks = []api.MonitorInspectCheck{{
			ID: "skill.script", Name: sk.Name, Category: sk.Category,
			Status: "critical", Value: "脚本执行失败",
			Detail: trimTail(sanitizeSecretText(err.Error()), 240),
		}}
	}
	return item
}

func (h *GatewayHub) runAgentDrivenSkillInspection(
	ctx context.Context,
	auth TokenAuth,
	target monitorTargetRef,
	runID string,
	sk platformSkillRecord,
	userPrompt string,
) monitorRunItemRecord {
	started := time.Now()
	item := newSkillRunItem(runID, target, sk, "agent_skill", started)
	if userPrompt != "" {
		item.Evidence["prompt"] = trimTail(sanitizeSecretText(userPrompt), 1024)
	}

	if err := h.syncOneSkillToAgent(ctx, auth, target, sk); err != nil {
		return markSkillSyncFailure(item, target, sk, started, err)
	}
	item.Evidence["synced"] = true

	instruction := buildSkillPromptInstruction(&sk, userPrompt)

	callCtx, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 90*time.Second)
	defer cancel()
	rawOut, err := h.RunInternalToolCall(callCtx, target.Cluster, target.Instance, "doops_agent_prompt", map[string]any{
		"session_id":  "skill-inspect-" + runID + "-" + sk.Slug,
		"instruction": instruction,
		"operation":   "ask",
		"skill":       sk.Slug,
	})
	if err != nil {
		// If the Skill ships an executable script (scripts/inspect.sh or scripts/run.sh),
		// run it directly through audited doops_shell so script-backed Skills work even without an LLM key.
		if scriptRel := findSkillExecutableScript(sk); scriptRel != "" {
			scriptCmd := skillScriptCommand(sk.Slug, scriptRel, target, runID)
			shCtx, shCancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 90*time.Second)
			shOut, shErr := h.RunInternalToolCall(shCtx, target.Cluster, target.Instance, "doops_shell", map[string]any{
				"session_id": "skill-script-" + runID + "-" + sk.Slug,
				"command":    scriptCmd,
			})
			shCancel()
			item.FinishedAtMs = time.Now().UnixMilli()
			item.DurationMs = time.Since(started).Milliseconds()
			item.CollectorMethod = "skill_script"
			item.Evidence["script"] = scriptRel
			item.Evidence["fallback_from_ai"] = trimTail(sanitizeSecretText(err.Error()), 180)
			cleaned := stripShellCompletionSuffix(shOut)
			parsed := extractInspectionArtifact(cleaned, sk.Name, sk.Category)
			item.ArtifactFormat = parsed.Format
			item.ArtifactMarkdown = parsed.Markdown
			item.ArtifactJSON = parsed.JSON
			item.RawOutput = parsed.Raw
			item.Checks = parsed.Checks
			item.Status = parsed.Status
			if shErr != nil {
				item.Status = "critical"
				item.Reason = "skill_script_failed"
				item.Checks = []api.MonitorInspectCheck{{
					ID: "skill.script", Name: sk.Name, Category: sk.Category,
					Status: "critical", Value: "脚本执行失败",
					Detail: trimTail(sanitizeSecretText(shErr.Error()), 240),
				}}
			}
			return item
		}

		item.FinishedAtMs = time.Now().UnixMilli()
		item.DurationMs = time.Since(started).Milliseconds()
		cleanErr := trimTail(sanitizeSecretText(err.Error()), 240)
		item.Status = "skipped"
		item.Reason = "skill_synced_ai_unavailable"
		item.ArtifactFormat = "markdown"
		item.ArtifactMarkdown = fmt.Sprintf(
			"### Skill 已同步，等待大模型凭据或直接命令\n\n- **Skill**：`%s`（已同步至 `/root/.agent/skills/%s`）\n- **目标**：`%s/%s`\n- **未执行原因**：`%s`\n\n> 请为目标绑定 `modelProvider` 凭据以驱动 AI Skill + 提示词，或在巡检栏「直接执行命令」输入任意 Shell 命令，或在 Skill 包内附带 `scripts/inspect.sh`。",
			sk.Slug, sk.Slug, target.Cluster, target.Instance, cleanErr,
		)
		item.Checks = []api.MonitorInspectCheck{{
			ID: "skill.execute", Name: sk.Name, Category: sk.Category,
			Status: "skipped", Value: "Skill 已同步 · 待配置大模型",
			Detail: fmt.Sprintf("Skill 已同步至目标 Agent（/root/.agent/skills/%s），但未能驱动 AI Skill 执行：%s", sk.Slug, cleanErr),
		}}
		return item
	}

	item.FinishedAtMs = time.Now().UnixMilli()
	item.DurationMs = time.Since(started).Milliseconds()
	parsed := extractInspectionArtifact(rawOut, sk.Name, sk.Category)
	item.ArtifactFormat = parsed.Format
	item.ArtifactMarkdown = parsed.Markdown
	item.ArtifactJSON = parsed.JSON
	item.RawOutput = parsed.Raw
	item.Checks = parsed.Checks
	item.Status = parsed.Status
	return item
}

// runCustomPromptInspection runs a free-form natural-language inspection prompt
// through doops_agent_prompt and persists the resulting Markdown/JSON artifact.
func (h *GatewayHub) runCustomPromptInspection(
	ctx context.Context,
	auth TokenAuth,
	target monitorTargetRef,
	runID string,
	userPrompt string,
	skillSlugs []string,
) monitorRunItemRecord {
	started := time.Now()
	title := truncateUTF8Head(strings.Split(strings.TrimSpace(userPrompt), "\n")[0], 48)
	if title == "" {
		title = "提示词巡检"
	}
	evidence := map[string]any{
		"prompt": trimTail(sanitizeSecretText(userPrompt), 1024),
	}
	if len(skillSlugs) > 0 {
		evidence["skills"] = skillSlugs
	}
	item := monitorRunItemRecord{
		RunID:           runID,
		Cluster:         target.Cluster,
		Instance:        target.Instance,
		AssetID:         "custom-prompt",
		AssetName:       title,
		AssetKind:       "prompt",
		Strategy:        "custom_prompt",
		CollectorMethod: "agent_prompt",
		StartedAtMs:     started.UnixMilli(),
		Evidence:        evidence,
	}

	instruction := buildSkillPromptInstruction(nil, userPrompt)
	args := map[string]any{
		"session_id":  "prompt-inspect-" + runID,
		"instruction": instruction,
		"operation":   "ask",
	}
	if len(skillSlugs) > 0 {
		args["skill"] = skillSlugs[0]
	}

	callCtx, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 90*time.Second)
	defer cancel()
	rawOut, err := h.RunInternalToolCall(callCtx, target.Cluster, target.Instance, "doops_agent_prompt", args)
	item.FinishedAtMs = time.Now().UnixMilli()
	item.DurationMs = time.Since(started).Milliseconds()

	if err != nil {
		cleanErr := trimTail(sanitizeSecretText(err.Error()), 240)
		item.Status = "skipped"
		item.Reason = "ai_unavailable"
		item.ArtifactFormat = "markdown"
		item.ArtifactMarkdown = fmt.Sprintf(
			"### 提示词巡检暂未执行（待配置大模型凭据）\n\n- **目标**：`%s/%s`\n- **提示词**：%s\n- **原因**：`%s`\n\n> 为目标绑定 `modelProvider` 凭据即可自动让 Agent 按提示词执行命令并返回 Markdown+JSON 产物；如需无大模型直接执行，可在「直接执行命令」栏输入 Shell 命令。",
			target.Cluster, target.Instance, sanitizeSecretText(title), cleanErr,
		)
		item.Checks = []api.MonitorInspectCheck{{
			ID: "prompt.execute", Name: title, Category: "prompt",
			Status: "skipped", Value: "待配置大模型",
			Detail: "未能驱动提示词巡检：" + cleanErr,
		}}
		return item
	}

	parsed := extractInspectionArtifact(rawOut, title, "prompt")
	item.ArtifactFormat = parsed.Format
	item.ArtifactMarkdown = parsed.Markdown
	item.ArtifactJSON = parsed.JSON
	item.RawOutput = parsed.Raw
	item.Checks = parsed.Checks
	item.Status = parsed.Status
	return item
}

// retiredStrategySkillSlugs maps the native strategy identifiers that used to
// drive inspection to the Skill package that owns the same capability today.
// The native collectors are retired (SKILL_FIRST_INSPECTION_SPEC.md 1.1 / 2.4);
// the map only keeps older runs, schedules and retest requests truthful.
var retiredStrategySkillSlugs = map[string]string{
	"vm_host":           "inspect-vm-host",
	"ssh_host":          "inspect-ssh-host",
	"k8s_cluster":       "inspect-k8s-cluster",
	"container_runtime": "inspect-container-runtime",
	"app_service":       "inspect-app-web",
	"database_service":  "inspect-database",
}

// skillSlugForRetiredStrategy resolves the Skill package that replaced a legacy
// native strategy. New runs carry "skill:<slug>" already, which passes through.
func skillSlugForRetiredStrategy(strategy string) (string, bool) {
	s := strings.TrimSpace(strategy)
	if rest, ok := strings.CutPrefix(s, "skill:"); ok {
		return rest, rest != ""
	}
	slug, ok := retiredStrategySkillSlugs[s]
	return slug, ok
}

// skillCollectorMethod reports how a Skill item will be executed.
func skillCollectorMethod(sk platformSkillRecord, hasCustomAction bool) string {
	if resolveSkillDispatchMode(sk, hasCustomAction) == skillExecModeScript {
		return "skill_script"
	}
	return "agent_skill"
}

// monitorRetestSkip records a retest asset that cannot be replayed through a
// Skill package, together with the reason reported to the operator.
type monitorRetestSkip struct {
	Asset  api.MonitorInspectAsset
	Reason string // legacy_strategy_retired | skill_unavailable
}

// resolveRetestSkills maps the asset rows of a previous run onto the Skill
// packages that own their capability. Retesting never re-enters the retired
// native dispatch path: it either replays the owning Skill or reports why not.
func (h *GatewayHub) resolveRetestSkills(ctx context.Context, auth TokenAuth, assets []api.MonitorInspectAsset) ([]platformSkillRecord, []monitorRetestSkip) {
	if len(assets) == 0 {
		return nil, nil
	}
	lookupAuth := auth
	if lookupAuth.UserID == "" {
		lookupAuth.UserID = "system"
	}
	skills := make([]platformSkillRecord, 0, len(assets))
	skips := make([]monitorRetestSkip, 0)
	seen := map[string]bool{}
	for _, a := range assets {
		slug, ok := skillSlugForRetiredStrategy(a.Strategy)
		if !ok {
			skips = append(skips, monitorRetestSkip{Asset: a, Reason: "legacy_strategy_retired"})
			continue
		}
		if seen[slug] {
			continue
		}
		sk, err := h.store.GetPlatformSkill(ctx, lookupAuth, slug)
		if err != nil || !sk.Enabled {
			skips = append(skips, monitorRetestSkip{Asset: a, Reason: "skill_unavailable"})
			continue
		}
		seen[slug] = true
		skills = append(skills, sk)
	}
	return skills, skips
}

// monitorRetestSkipItem renders an explicit skipped item so a retest never
// silently drops an asset the platform can no longer inspect.
func monitorRetestSkipItem(runID string, t monitorTargetRef, skip monitorRetestSkip, started time.Time) monitorRunItemRecord {
	nowMs := time.Now().UnixMilli()
	detail := "旧版原生策略 " + skip.Asset.Strategy + " 已退役，且没有 Skill 包接管该能力；请通过 skills 参数指定巡检 Skill"
	if skip.Reason == "skill_unavailable" {
		detail = "该资产对应的 Skill 包不存在或已停用，无法按 Skill 包重测；请在技能库中启用后再重试"
	}
	return monitorRunItemRecord{
		RunID:           runID,
		Cluster:         t.Cluster,
		Instance:        t.Instance,
		AssetID:         skip.Asset.AssetID,
		AssetName:       skip.Asset.AssetName,
		AssetKind:       skip.Asset.AssetKind,
		Strategy:        skip.Asset.Strategy,
		CollectorMethod: "retired_strategy",
		Status:          "skipped",
		Reason:          skip.Reason,
		StartedAtMs:     started.UnixMilli(),
		FinishedAtMs:    nowMs,
		DurationMs:      nowMs - started.UnixMilli(),
		Checks: []api.MonitorInspectCheck{{
			ID: "skill.dispatch", Name: "巡检能力来源", Category: "capability",
			Status: "warning", Detail: detail,
		}},
	}
}
