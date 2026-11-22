package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
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
	Strategies   []string
	Thresholds   api.MonitorInspectThresholds
	ExplicitSSH  []api.MonitorInspectAsset
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
				Strategies:   s.Strategies,
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
		sort.Slice(out, func(i, j int) bool { return tunnelKey(out[i].Cluster, out[i].Instance) < tunnelKey(out[j].Cluster, out[j].Instance) })
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

func (h *GatewayHub) buildAgentInspectAssets(ctx context.Context, cluster, instance string, strategies []string, explicitSSH []api.MonitorInspectAsset) []api.MonitorInspectAsset {
	stratSet := make(map[string]bool, len(strategies))
	for _, s := range strategies {
		stratSet[s] = true
	}
	assets := []api.MonitorInspectAsset{}

	// 1. Machines bound to this Agent (vm_host + ssh_host)
	hasLocalHost := false
	if (stratSet[monitorStrategyVMHost] || stratSet[monitorStrategySSHHost]) && h.store.tableExists(ctx, "inventory_machines") {
		hasTags := h.store.tableExists(ctx, "inventory_machine_tags")
		hasLogins := h.store.tableExists(ctx, "inventory_machine_login_accounts")
		rows, err := h.store.db.QueryContext(ctx, `SELECT asset_id, name, kind, address FROM inventory_machines
			WHERE cluster=? AND instance=? ORDER BY (asset_id=?) DESC, kind='vm' DESC, asset_id ASC LIMIT 32`,
			cluster, instance, instance)
		if err == nil {
			type mRow struct{ id, name, kind, addr string }
			var mList []mRow
			for rows.Next() {
				var m mRow
				if rows.Scan(&m.id, &m.name, &m.kind, &m.addr) == nil {
					mList = append(mList, m)
				}
			}
			rows.Close()
			for idx, m := range mList {
				var tags []string
				if hasTags {
					tRows, tErr := h.store.db.QueryContext(ctx, `SELECT tag FROM inventory_machine_tags WHERE asset_id=? ORDER BY tag`, m.id)
					if tErr == nil {
						for tRows.Next() {
							var tg string
							if tRows.Scan(&tg) == nil {
								tags = append(tags, tg)
							}
						}
						tRows.Close()
					}
				}
				sshUser := ""
				if hasLogins {
					_ = h.store.db.QueryRowContext(ctx, `SELECT username FROM inventory_machine_login_accounts WHERE asset_id=? ORDER BY username LIMIT 1`, m.id).Scan(&sshUser)
				}
				isSSH := false
				for _, tg := range tags {
					if tg == "ssh" || tg == "collector=ssh" {
						isSSH = true
					}
				}
				if idx == 0 && !isSSH && stratSet[monitorStrategyVMHost] {
					hasLocalHost = true
					assets = append(assets, api.MonitorInspectAsset{
						AssetID: m.id, AssetName: m.name, AssetKind: m.kind,
						Strategy: monitorStrategyVMHost, Address: m.addr, Tags: tags,
					})
				} else if m.addr != "" && stratSet[monitorStrategySSHHost] {
					assets = append(assets, api.MonitorInspectAsset{
						AssetID: m.id, AssetName: m.name, AssetKind: m.kind,
						Strategy: monitorStrategySSHHost, Address: m.addr, SSHUser: sshUser, Tags: tags,
					})
				}
			}
		}
	}
	if stratSet[monitorStrategyVMHost] && !hasLocalHost {
		assets = append(assets, api.MonitorInspectAsset{
			AssetID:   instance,
			AssetName: cluster + "/" + instance,
			AssetKind: "vm",
			Strategy:  monitorStrategyVMHost,
		})
	}
	if stratSet[monitorStrategySSHHost] {
		for _, extra := range explicitSSH {
			extra.Strategy = monitorStrategySSHHost
			if extra.AssetKind == "" {
				extra.AssetKind = "vm"
			}
			assets = append(assets, extra)
		}
	}

	// 2. K8s Cluster Strategy
	if stratSet[monitorStrategyK8sCluster] {
		addedK8s := false
		if h.store.tableExists(ctx, "cluster_inventory_sources") {
			var kID, kName string
			if err := h.store.db.QueryRowContext(ctx, `SELECT asset_id, name FROM cluster_inventory_sources WHERE cluster=? AND instance=? LIMIT 1`, cluster, instance).Scan(&kID, &kName); err == nil && kID != "" {
				addedK8s = true
				assets = append(assets, api.MonitorInspectAsset{
					AssetID: kID, AssetName: kName, AssetKind: "k8s", Strategy: monitorStrategyK8sCluster,
				})
			}
		}
		if !addedK8s {
			assets = append(assets, api.MonitorInspectAsset{
				AssetID: cluster + "-k8s", AssetName: cluster + " Kubernetes", AssetKind: "k8s", Strategy: monitorStrategyK8sCluster,
			})
		}
	}

	// 3. Container Runtime Strategy
	if stratSet[monitorStrategyContainerRuntime] {
		assets = append(assets, api.MonitorInspectAsset{
			AssetID: instance + "-containers", AssetName: instance + " 容器运行时", AssetKind: "container", Strategy: monitorStrategyContainerRuntime,
		})
	}

	// 4. App / Website & Database Strategies from inventory_resource_catalog
	hasCatalog := h.store.tableExists(ctx, "inventory_resource_catalog")
	if stratSet[monitorStrategyAppService] {
		if hasCatalog {
			rows, err := h.store.db.QueryContext(ctx, `SELECT name, details FROM inventory_resource_catalog WHERE kind='website' AND cluster=? AND instance=? ORDER BY id LIMIT 16`, cluster, instance)
			if err == nil {
				for rows.Next() {
					var name string
					var raw []byte
					if rows.Scan(&name, &raw) != nil {
						continue
					}
					var d map[string]string
					if json.Unmarshal(raw, &d) != nil {
						continue
					}
					scheme, domain, p := d["scheme"], d["domain"], d["path"]
					if (scheme != "http" && scheme != "https") || !catalogHost(domain) || !strings.HasPrefix(p, "/") {
						continue
					}
					aid := domain
					if !monitorID(aid) {
						aid = name
					}
					assets = append(assets, api.MonitorInspectAsset{
						AssetID: aid, AssetName: name, AssetKind: "app",
						Strategy: monitorStrategyAppService, URL: scheme + "://" + domain + p,
					})
				}
				rows.Close()
			}
		}
		// Local nginx stub_status probe (skipped cleanly if nginx is absent).
		assets = append(assets, api.MonitorInspectAsset{
			AssetID: "nginx", AssetName: "本机 Nginx", AssetKind: "app", Strategy: monitorStrategyAppService,
		})
	}

	if stratSet[monitorStrategyDatabaseService] && hasCatalog {
		rows, err := h.store.db.QueryContext(ctx, `SELECT name, details FROM inventory_resource_catalog WHERE kind='database' AND cluster=? AND instance=? ORDER BY id LIMIT 16`, cluster, instance)
		if err == nil {
			for rows.Next() {
				var name string
				var raw []byte
				if rows.Scan(&name, &raw) != nil {
					continue
				}
				var d map[string]string
				if json.Unmarshal(raw, &d) != nil {
					continue
				}
				port, _ := strconv.Atoi(d["port"])
				aid := name
				if !monitorID(aid) {
					aid = d["database"]
				}
				if aid == "" {
					aid = d["host"]
				}
				assets = append(assets, api.MonitorInspectAsset{
					AssetID: aid, AssetName: name, AssetKind: "database",
					Strategy: monitorStrategyDatabaseService,
					Engine:   d["engine"], Address: d["host"], Port: port, Database: d["database"],
				})
			}
			rows.Close()
		}
	}

	return assets
}

func (h *GatewayHub) ExecuteMonitorRun(ctx context.Context, plan monitorRunPlan) (monitorRunRecord, []monitorRunItemRecord, error) {
	started := time.Now().UTC()
	runID := "run_" + randomHex(8)
	plan.Strategies = normalizeMonitorStrategies(plan.Strategies)
	plan.Thresholds = normalizeMonitorThresholds(plan.Thresholds)
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
		CommandSummary: fmt.Sprintf("run=%s trigger=%s targets=%d strategies=%s", runID, plan.TriggerKind, len(targets), strings.Join(plan.Strategies, ",")),
	})

	var (
		mu           sync.Mutex
		allItems     []monitorRunItemRecord
		allSnapshots []monitorResourceSnapshot
		allSeries    []monitorSeriesBridgeEntry
		offlineCount int
	)

	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, tgt := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t monitorTargetRef) {
			defer wg.Done()
			defer func() { <-sem }()

			tStart := time.Now()
			var assets []api.MonitorInspectAsset
			if plan.RetestItems != nil {
				assets = plan.RetestItems[tunnelKey(t.Cluster, t.Instance)]
			} else {
				assets = h.buildAgentInspectAssets(ctx, t.Cluster, t.Instance, plan.Strategies, plan.ExplicitSSH)
			}
			if len(assets) == 0 {
				return
			}

			// Offline Agent check — never silently skipped!
			if h.getAgent(t.Cluster, t.Instance) == nil {
				nowMs := time.Now().UnixMilli()
				mu.Lock()
				offlineCount++
				allItems = append(allItems, monitorRunItemRecord{
					RunID:           runID,
					Cluster:         t.Cluster,
					Instance:        t.Instance,
					AssetID:         assets[0].AssetID,
					AssetName:       assets[0].AssetName,
					AssetKind:       assets[0].AssetKind,
					Strategy:        assets[0].Strategy,
					CollectorMethod: strategyCollectorMethod(assets[0].Strategy),
					Status:          "offline",
					Reason:          "target_offline",
					StartedAtMs:     tStart.UnixMilli(),
					FinishedAtMs:    nowMs,
					DurationMs:      time.Since(tStart).Milliseconds(),
					Checks: []api.MonitorInspectCheck{{
						ID: "agent.online", Name: "Agent 隧道连通性", Category: "availability",
						Status: "critical", Value: "离线", Threshold: "Agent 在线",
						Detail: fmt.Sprintf("目标 %s/%s 当前离线，无法下发巡检任务", t.Cluster, t.Instance),
					}},
				})
				allSnapshots = append(allSnapshots, monitorResourceSnapshot{
					Cluster: t.Cluster, Instance: t.Instance,
					AssetID: assets[0].AssetID, AssetName: assets[0].AssetName, AssetKind: assets[0].AssetKind,
					Strategy: assets[0].Strategy, CollectorMethod: strategyCollectorMethod(assets[0].Strategy),
					CollectedAtMs: nowMs, RunID: runID, Status: "offline", Tags: assets[0].Tags,
				})
				mu.Unlock()
				return
			}

			results, callErr := h.dispatchAgentMonitorInspect(ctx, plan.Auth, t, runID, plan.Strategies, assets, plan.Thresholds)
			nowMs := time.Now().UnixMilli()
			if callErr != nil {
				mu.Lock()
				allItems = append(allItems, monitorRunItemRecord{
					RunID:           runID,
					Cluster:         t.Cluster,
					Instance:        t.Instance,
					AssetID:         assets[0].AssetID,
					AssetName:       assets[0].AssetName,
					AssetKind:       assets[0].AssetKind,
					Strategy:        assets[0].Strategy,
					CollectorMethod: strategyCollectorMethod(assets[0].Strategy),
					Status:          "error",
					Reason:          sanitizeSecretText(callErr.Error()),
					StartedAtMs:     tStart.UnixMilli(),
					FinishedAtMs:    nowMs,
					DurationMs:      time.Since(tStart).Milliseconds(),
					Checks: []api.MonitorInspectCheck{{
						ID: "agent.dispatch", Name: "任务下发与执行", Category: "availability",
						Status: "critical", Detail: sanitizeSecretText(callErr.Error()),
					}},
				})
				mu.Unlock()
				return
			}

			mu.Lock()
			for _, r := range results {
				item := monitorRunItemRecord{
					RunID:           runID,
					Cluster:         t.Cluster,
					Instance:        t.Instance,
					AssetID:         r.AssetID,
					AssetName:       r.AssetName,
					AssetKind:       r.AssetKind,
					Strategy:        r.Strategy,
					CollectorMethod: r.CollectorMethod,
					Status:          r.Status,
					Reason:          r.Reason,
					StartedAtMs:     tStart.UnixMilli(),
					FinishedAtMs:    r.CollectedAtMs,
					DurationMs:      r.DurationMs,
					Metrics:         r.Metrics,
					Checks:          r.Checks,
					Evidence:        r.Evidence,
				}
				allItems = append(allItems, item)

				// Record resource snapshots for every non-skipped asset result so fleet resource monitor stays fresh.
				if r.Status != "skipped" {
					snapStatus := "ok"
					switch r.Status {
					case "warning", "critical", "error", "offline":
						snapStatus = r.Status
					}
					allSnapshots = append(allSnapshots, monitorResourceSnapshot{
						Cluster:           t.Cluster,
						Instance:          t.Instance,
						AssetID:           r.AssetID,
						AssetName:         r.AssetName,
						AssetKind:         r.AssetKind,
						Strategy:          r.Strategy,
						CollectorMethod:   r.CollectorMethod,
						CollectedAtMs:     r.CollectedAtMs,
						RunID:             runID,
						Status:            snapStatus,
						CPUCores:          r.Metrics.CPUCores,
						CPUTotalCores:     r.Metrics.CPUTotalCores,
						CPUPercent:        r.Metrics.CPUPercent,
						MemoryUsedBytes:   r.Metrics.MemoryUsedBytes,
						MemoryTotalBytes:  r.Metrics.MemoryTotalBytes,
						MemoryPercent:     r.Metrics.MemoryPercent,
						DiskUsedBytes:     r.Metrics.DiskUsedBytes,
						DiskTotalBytes:    r.Metrics.DiskTotalBytes,
						DiskPercent:       r.Metrics.DiskPercent,
						NetBytesPerSec:    r.Metrics.NetBytesPerSec,
						DiskIOPS:          r.Metrics.DiskIOPS,
						DiskLatencyMs:     r.Metrics.DiskLatencyMs,
						GPUPercent:        r.Metrics.GPUPercent,
						ResponseTimeMs:    r.Metrics.ResponseTimeMs,
						ErrorRate:         r.Metrics.ErrorRate,
						ActiveConnections: r.Metrics.ActiveConnections,
						RequestsPerSec:    r.Metrics.RequestsPerSec,
						UptimeSeconds:     r.Metrics.UptimeSeconds,
						Load1m:            r.Metrics.Load1m,
						OSInfo:            r.Metrics.OSInfo,
						KernelInfo:        r.Metrics.KernelInfo,
						Tags:              r.Tags,
						Details:           r.Details,
					})
				}

				for _, pt := range r.SeriesPoints {
					allSeries = append(allSeries, monitorSeriesBridgeEntry{Cluster: t.Cluster, Instance: t.Instance, Point: pt})
				}
			}
			mu.Unlock()
		}(tgt)
	}
	wg.Wait()

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
	if auditID > 0 {
		_ = h.store.FinishAudit(auditID, AuditFinish{
			Status:         "success",
			CommandSummary: fmt.Sprintf("run=%s status=%s assets=%d written=%d", run.ID, run.Status, run.TotalAssets, written),
		})
	}
	return run, allItems, nil
}

func (h *GatewayHub) dispatchAgentMonitorInspect(
	ctx context.Context,
	auth TokenAuth,
	target monitorTargetRef,
	runID string,
	strategies []string,
	assets []api.MonitorInspectAsset,
	thresholds api.MonitorInspectThresholds,
) ([]api.MonitorInspectAssetResult, error) {
	callCtx, cancel := context.WithTimeout(contextWithInternalAuditAuth(ctx, auth), 45*time.Second)
	defer cancel()

	out, err := h.RunInternalToolCall(callCtx, target.Cluster, target.Instance, monitorInspectTool, map[string]any{
		"session_id": "monitor-" + runID,
		"run_id":     runID,
		"strategies": strategies,
		"assets":     assets,
		"thresholds": thresholds,
	})
	if err != nil {
		// Graceful fallback for older Agents that do not yet register doops_monitor_inspect.
		if strings.Contains(err.Error(), "-32601") || strings.Contains(err.Error(), "Unknown tool") {
			return h.fallbackLegacyVMInspect(callCtx, target, runID, assets, thresholds)
		}
		return nil, err
	}

	var resp api.MonitorInspectResponse
	if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); jsonErr != nil {
		return nil, fmt.Errorf("invalid_monitor_inspect_response")
	}
	return resp.Results, nil
}

func (h *GatewayHub) fallbackLegacyVMInspect(
	ctx context.Context,
	target monitorTargetRef,
	runID string,
	assets []api.MonitorInspectAsset,
	th api.MonitorInspectThresholds,
) ([]api.MonitorInspectAssetResult, error) {
	out, err := h.RunInternalToolCall(ctx, target.Cluster, target.Instance, "doops_node_info", map[string]any{
		"session_id": "monitor-" + runID,
		"section":    "vm_metrics",
	})
	if err != nil {
		return nil, fmt.Errorf("agent_upgrade_required_for_monitor_inspect")
	}
	var snap agentVMSnapshot
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &snap) != nil {
		return nil, fmt.Errorf("invalid_legacy_vm_metrics")
	}
	at := snap.AtMs
	if at <= 0 {
		at = time.Now().UnixMilli()
	}
	aid := target.Instance
	name := target.Cluster + "/" + target.Instance
	if len(assets) > 0 {
		aid = assets[0].AssetID
		name = assets[0].AssetName
	}
	res := api.MonitorInspectAssetResult{
		AssetID:         aid,
		AssetName:       name,
		AssetKind:       "vm",
		Strategy:        monitorStrategyVMHost,
		CollectorMethod: "agent_local",
		CollectedAtMs:   at,
		Reason:          "legacy_agent_fallback",
		Metrics: api.MonitorInspectMetrics{
			CPUCores:        snap.CPUCores,
			KernelCPUCores:  snap.KernelCPUCores,
			MemoryUsedBytes: snap.MemoryUsedBytes,
			KernelSlabBytes: snap.KernelSlabBytes,
		},
		Checks: []api.MonitorInspectCheck{
			evaluateHostCPUMemCheck(nil, nil, snap.CPUCores, nil, th),
			{ID: "agent.version", Name: "Agent 策略引擎版本", Category: "runtime", Status: "warning", Detail: "旧版 Agent 仅支持基础主机指标回退，建议升级以启用完整 K8s/SSH/合规策略"},
		},
		Status: "warning",
	}
	return []api.MonitorInspectAssetResult{res}, nil
}
