package server

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"strings"
	"time"
)

// bridgeNativeMonitorCollectToSnapshots projects native POST /v1/assets/monitor
// action=collect samples into monitor_resource_snapshots inside the same
// transaction so fleet-level Resource Monitoring and Situational Awareness stay
// in sync with per-target granular series without one-way drift.
func bridgeNativeMonitorCollectToSnapshots(
	ctx context.Context,
	tx *sql.Tx,
	req monitorRequest,
	mode string,
	samples []monitorInput,
	runtimeName string,
) error {
	var hasSnapshots bool
	if err := tx.QueryRowContext(ctx, gatewayTableExistsSQL(gatewayDialect()), "monitor_resource_snapshots").Scan(&hasSnapshots); err != nil {
		return err
	}
	if !hasSnapshots {
		return nil
	}
	switch mode {
	case "host":
		return bridgeNativeHostSnapshot(ctx, tx, req, samples)
	case "apps":
		return bridgeNativeAppSnapshots(ctx, tx, req, samples)
	case "containers":
		return bridgeNativeContainerSnapshot(ctx, tx, req, samples, runtimeName)
	case "k8s_pods":
		return bridgeNativeK8sPodsSnapshot(ctx, tx, req, samples)
	default:
		return nil
	}
}

func bridgeNativeHostSnapshot(ctx context.Context, tx *sql.Tx, req monitorRequest, samples []monitorInput) error {
	assetID := req.Instance
	var collectedAt int64
	var cpuCores, memUsed *float64
	type mountAcc struct {
		mount           string
		total, used, av float64
		hasTot, hasUsed bool
		hasAvail        bool
	}
	type diskAcc struct {
		dev                                      string
		rIOPS, wIOPS, rBps, wBps, rLatMs, wLatMs *float64
	}
	mounts := map[string]*mountAcc{}
	disks := map[string]*diskAcc{}
	nets := map[string]float64{}
	hasSample := false

	for _, s := range samples {
		if s.Type != "machine" {
			continue
		}
		if s.AssetID != "" {
			assetID = s.AssetID
		}
		if s.At > collectedAt {
			collectedAt = s.At
		}
		if s.Status != "ok" || s.Value == nil {
			continue
		}
		v := *s.Value
		hasSample = true
		switch s.Metric {
		case "cpu":
			cp := v
			cpuCores = &cp
		case "memory":
			cp := v
			memUsed = &cp
		case "network":
			if s.DeviceID != "" {
				nets[s.DeviceID] = v
			}
		case "storage", "storage_total", "storage_available":
			if s.DeviceID == "" {
				continue
			}
			m := mounts[s.DeviceID]
			if m == nil {
				m = &mountAcc{mount: s.DeviceID}
				mounts[s.DeviceID] = m
			}
			switch s.Metric {
			case "storage":
				m.used, m.hasUsed = v, true
			case "storage_total":
				m.total, m.hasTot = v, true
			case "storage_available":
				m.av, m.hasAvail = v, true
			}
		case "disk_read_iops", "disk_write_iops", "disk_read_bytes_per_second", "disk_write_bytes_per_second", "disk_read_latency_ms", "disk_write_latency_ms":
			if s.DeviceID == "" {
				continue
			}
			d := disks[s.DeviceID]
			if d == nil {
				d = &diskAcc{dev: s.DeviceID}
				disks[s.DeviceID] = d
			}
			cp := v
			switch s.Metric {
			case "disk_read_iops":
				d.rIOPS = &cp
			case "disk_write_iops":
				d.wIOPS = &cp
			case "disk_read_bytes_per_second":
				d.rBps = &cp
			case "disk_write_bytes_per_second":
				d.wBps = &cp
			case "disk_read_latency_ms":
				d.rLatMs = &cp
			case "disk_write_latency_ms":
				d.wLatMs = &cp
			}
		}
	}
	if !hasSample {
		return nil
	}
	if collectedAt <= 0 {
		collectedAt = time.Now().UnixMilli()
	}

	assetKind := "vm"
	assetName := assetID
	tags := []string{}
	var cpuTotalCores, memTotalBytes *float64
	var hasMachines bool
	if err := tx.QueryRowContext(ctx, gatewayTableExistsSQL(gatewayDialect()), "inventory_machines").Scan(&hasMachines); err != nil {
		return err
	}
	if hasMachines {
		var name, kind string
		var cpuCnt sql.NullInt64
		var memGiB sql.NullFloat64
		if err := tx.QueryRowContext(ctx,
			`SELECT name, kind, cpu_count, memory_gib FROM inventory_machines WHERE asset_id=? LIMIT 1`,
			assetID).Scan(&name, &kind, &cpuCnt, &memGiB); err == nil {
			if strings.TrimSpace(name) != "" {
				assetName = name
			}
			if kind == "physical" || kind == "vm" {
				assetKind = kind
			}
			if cpuCnt.Valid && cpuCnt.Int64 > 0 {
				tot := float64(cpuCnt.Int64)
				cpuTotalCores = &tot
			}
			if memGiB.Valid && memGiB.Float64 > 0 {
				tot := memGiB.Float64 * 1024 * 1024 * 1024
				memTotalBytes = &tot
			}
		}
		var hasMachineTags bool
		if err := tx.QueryRowContext(ctx, gatewayTableExistsSQL(gatewayDialect()), "inventory_machine_tags").Scan(&hasMachineTags); err == nil && hasMachineTags {
			if tagRows, err := tx.QueryContext(ctx, `SELECT tag FROM inventory_machine_tags WHERE asset_id=? ORDER BY tag`, assetID); err == nil {
				for tagRows.Next() {
					var tag string
					if tagRows.Scan(&tag) == nil && strings.TrimSpace(tag) != "" {
						tags = append(tags, tag)
					}
				}
				tagRows.Close()
			}
		}
	}
	// Fall back to the target's latest host snapshot capacity if CMDB specs are absent
	// so native host sampling still produces CPU%/Memory% utilization and trend points.
	if cpuTotalCores == nil || memTotalBytes == nil {
		var prevCPU, prevMem sql.NullFloat64
		if err := tx.QueryRowContext(ctx, `
			SELECT cpu_total_cores, memory_total_bytes
			FROM monitor_resource_snapshots
			WHERE cluster=? AND instance=?
			  AND (asset_kind IN ('vm', 'physical') OR (asset_kind='skill' AND (strategy='skill:inspect-vm-host' OR asset_id='inspect-vm-host')))
			  AND (cpu_total_cores IS NOT NULL OR memory_total_bytes IS NOT NULL)
			ORDER BY collected_at_ms DESC, id DESC
			LIMIT 1`, req.Cluster, req.Instance).Scan(&prevCPU, &prevMem); err == nil {
			if cpuTotalCores == nil && prevCPU.Valid && prevCPU.Float64 > 0 {
				v := prevCPU.Float64
				cpuTotalCores = &v
			}
			if memTotalBytes == nil && prevMem.Valid && prevMem.Float64 > 0 {
				v := prevMem.Float64
				memTotalBytes = &v
			}
		}
	}

	snap := monitorResourceSnapshot{
		Cluster:          req.Cluster,
		Instance:         req.Instance,
		AssetID:          assetID,
		AssetName:        assetName,
		AssetKind:        assetKind,
		Strategy:         "agent_host",
		CollectorMethod:  "agent_host",
		CollectedAtMs:    collectedAt,
		Status:           "ok",
		CPUCores:         cpuCores,
		CPUTotalCores:    cpuTotalCores,
		MemoryUsedBytes:  memUsed,
		MemoryTotalBytes: memTotalBytes,
		Tags:             tags,
		Details:          map[string]any{"source": "asset_monitor_collect"},
	}
	if cpuCores != nil && cpuTotalCores != nil && *cpuTotalCores > 0 {
		pct := math.Min(100, math.Max(0, (*cpuCores / *cpuTotalCores)*100))
		snap.CPUPercent = &pct
	}
	if memUsed != nil && memTotalBytes != nil && *memTotalBytes > 0 {
		pct := math.Min(100, math.Max(0, (*memUsed / *memTotalBytes)*100))
		snap.MemoryPercent = &pct
	}
	if len(mounts) > 0 {
		keys := make([]string, 0, len(mounts))
		for k := range mounts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var totalSum, usedSum, peakPct float64
		hasPeak := false
		mountRows := make([]map[string]any, 0, len(keys))
		for _, k := range keys {
			m := mounts[k]
			row := map[string]any{"mount": m.mount}
			if m.hasTot {
				row["total_bytes"] = m.total
				totalSum += m.total
			}
			if m.hasUsed {
				row["used_bytes"] = m.used
				usedSum += m.used
			}
			if m.hasAvail {
				row["avail_bytes"] = m.av
			}
			if m.hasTot && m.total > 0 && m.hasUsed {
				pct := math.Min(100, math.Max(0, (m.used/m.total)*100))
				row["used_pct"] = pct
				if !hasPeak || pct > peakPct {
					peakPct = pct
					hasPeak = true
				}
			}
			mountRows = append(mountRows, row)
		}
		if totalSum > 0 {
			snap.DiskTotalBytes = &totalSum
		}
		if usedSum > 0 || totalSum > 0 {
			snap.DiskUsedBytes = &usedSum
		}
		if hasPeak {
			snap.DiskPercent = &peakPct
		}
		snap.Details["mounts"] = mountRows
	}
	if len(nets) > 0 {
		keys := make([]string, 0, len(nets))
		for k := range nets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var netSum float64
		netRows := make([]map[string]any, 0, len(keys))
		for _, k := range keys {
			netSum += nets[k]
			netRows = append(netRows, map[string]any{"device_id": k, "bytes_per_sec": nets[k]})
		}
		snap.NetBytesPerSec = &netSum
		snap.Details["networks"] = netRows
	}
	if len(disks) > 0 {
		keys := make([]string, 0, len(disks))
		for k := range disks {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var iopsSum, maxLat float64
		hasIOPS, hasLat := false, false
		diskRows := make([]map[string]any, 0, len(keys))
		for _, k := range keys {
			d := disks[k]
			row := map[string]any{"device_id": d.dev}
			if d.rIOPS != nil {
				row["read_iops"] = *d.rIOPS
				iopsSum += *d.rIOPS
				hasIOPS = true
			}
			if d.wIOPS != nil {
				row["write_iops"] = *d.wIOPS
				iopsSum += *d.wIOPS
				hasIOPS = true
			}
			if d.rBps != nil {
				row["read_bytes_per_sec"] = *d.rBps
			}
			if d.wBps != nil {
				row["write_bytes_per_sec"] = *d.wBps
			}
			if d.rLatMs != nil {
				row["read_latency_ms"] = *d.rLatMs
				if !hasLat || *d.rLatMs > maxLat {
					maxLat = *d.rLatMs
					hasLat = true
				}
			}
			if d.wLatMs != nil {
				row["write_latency_ms"] = *d.wLatMs
				if !hasLat || *d.wLatMs > maxLat {
					maxLat = *d.wLatMs
					hasLat = true
				}
			}
			diskRows = append(diskRows, row)
		}
		if hasIOPS {
			snap.DiskIOPS = &iopsSum
		}
		if hasLat {
			snap.DiskLatencyMs = &maxLat
		}
		snap.Details["disks"] = diskRows
	}
	snap.Status = nativeHostSnapshotStatus(snap.CPUPercent, snap.MemoryPercent, snap.DiskPercent)
	return insertMonitorResourceSnapshot(ctx, tx, "", snap)
}

func nativeHostSnapshotStatus(cpuPct, memPct, diskPct *float64) string {
	peak := 0.0
	for _, p := range []*float64{cpuPct, memPct, diskPct} {
		if p != nil && *p > peak {
			peak = *p
		}
	}
	if peak >= 95 {
		return "critical"
	}
	if peak >= 85 {
		return "warning"
	}
	return "ok"
}

func bridgeNativeAppSnapshots(ctx context.Context, tx *sql.Tx, req monitorRequest, samples []monitorInput) error {
	type appAcc struct {
		assetID     string
		collectedAt int64
		respMs      *float64
		errRate     *float64
		activeConn  *float64
		rps         *float64
	}
	apps := map[string]*appAcc{}
	for _, s := range samples {
		if s.Type != "app" || s.AssetID == "" || s.Status != "ok" || s.Value == nil {
			continue
		}
		a := apps[s.AssetID]
		if a == nil {
			a = &appAcc{assetID: s.AssetID}
			apps[s.AssetID] = a
		}
		if s.At > a.collectedAt {
			a.collectedAt = s.At
		}
		cp := *s.Value
		switch s.Metric {
		case "response_time_ms":
			a.respMs = &cp
		case "error_rate":
			a.errRate = &cp
		case "active_connections":
			a.activeConn = &cp
		case "requests":
			a.rps = &cp
		}
	}
	keys := make([]string, 0, len(apps))
	for k := range apps {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a := apps[k]
		if a.collectedAt <= 0 {
			a.collectedAt = time.Now().UnixMilli()
		}
		status := "ok"
		if a.errRate != nil {
			if *a.errRate >= 100 {
				status = "critical"
			} else if *a.errRate > 0 {
				status = "warning"
			}
		}
		snap := monitorResourceSnapshot{
			Cluster:           req.Cluster,
			Instance:          req.Instance,
			AssetID:           a.assetID,
			AssetName:         a.assetID,
			AssetKind:         "app",
			Strategy:          "app_probe",
			CollectorMethod:   "app_probe",
			CollectedAtMs:     a.collectedAt,
			Status:            status,
			ResponseTimeMs:    a.respMs,
			ErrorRate:         a.errRate,
			ActiveConnections: a.activeConn,
			RequestsPerSec:    a.rps,
			Tags:              []string{},
			Details:           map[string]any{"source": "asset_monitor_collect"},
		}
		if err := insertMonitorResourceSnapshot(ctx, tx, "", snap); err != nil {
			return err
		}
	}
	return nil
}

func bridgeNativeContainerSnapshot(ctx context.Context, tx *sql.Tx, req monitorRequest, samples []monitorInput, runtimeName string) error {
	type containerAcc struct {
		name             string
		cpu, mem, memLim *float64
	}
	byName := map[string]*containerAcc{}
	var collectedAt int64
	for _, s := range samples {
		if s.Type != "container" || s.AssetID == "" {
			continue
		}
		if s.At > collectedAt {
			collectedAt = s.At
		}
		if s.Status != "ok" || s.Value == nil {
			continue
		}
		c := byName[s.AssetID]
		if c == nil {
			c = &containerAcc{name: s.AssetID}
			byName[s.AssetID] = c
		}
		cp := *s.Value
		switch s.Metric {
		case "cpu":
			c.cpu = &cp
		case "memory":
			c.mem = &cp
		case "memory_limit":
			c.memLim = &cp
		}
	}
	if len(byName) == 0 {
		return nil
	}
	if collectedAt <= 0 {
		collectedAt = time.Now().UnixMilli()
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	var cpuSum, memSum, memLimSum float64
	hasCPU, hasMem, hasLim := false, false, false
	rows := make([]map[string]any, 0, len(names))
	for _, name := range names {
		c := byName[name]
		row := map[string]any{"name": c.name}
		if c.cpu != nil {
			row["cpu_cores"] = *c.cpu
			cpuSum += *c.cpu
			hasCPU = true
		}
		if c.mem != nil {
			row["memory_bytes"] = *c.mem
			memSum += *c.mem
			hasMem = true
		}
		if c.memLim != nil {
			row["memory_limit_bytes"] = *c.memLim
			memLimSum += *c.memLim
			hasLim = true
		}
		rows = append(rows, row)
	}
	label := req.Instance + " 容器"
	if runtimeName != "" {
		label += " (" + runtimeName + ")"
	}
	snap := monitorResourceSnapshot{
		Cluster:         req.Cluster,
		Instance:        req.Instance,
		AssetID:         req.Instance + ":containers",
		AssetName:       label,
		AssetKind:       "container",
		Strategy:        "container_stats",
		CollectorMethod: "container_stats",
		CollectedAtMs:   collectedAt,
		Status:          "ok",
		Tags:            []string{},
		Details: map[string]any{
			"source":     "asset_monitor_collect",
			"runtime":    runtimeName,
			"containers": rows,
		},
	}
	if hasCPU {
		snap.CPUCores = &cpuSum
	}
	if hasMem {
		snap.MemoryUsedBytes = &memSum
	}
	if hasLim && memLimSum > 0 {
		snap.MemoryTotalBytes = &memLimSum
		if hasMem {
			pct := math.Min(100, math.Max(0, (memSum/memLimSum)*100))
			snap.MemoryPercent = &pct
		}
	}
	return insertMonitorResourceSnapshot(ctx, tx, "", snap)
}

func bridgeNativeK8sPodsSnapshot(ctx context.Context, tx *sql.Tx, req monitorRequest, samples []monitorInput) error {
	if req.Namespace == "" {
		return nil
	}
	var collectedAt int64
	var cpuSum, memSum float64
	hasCPU, hasMem := false, false
	pods := map[string]bool{}
	for _, s := range samples {
		if s.Type != "app" || s.Namespace != req.Namespace {
			continue
		}
		if s.At > collectedAt {
			collectedAt = s.At
		}
		if s.AssetID != "" {
			pods[s.AssetID] = true
		}
		if s.Status != "ok" || s.Value == nil {
			continue
		}
		switch s.Metric {
		case "cpu":
			cpuSum += *s.Value
			hasCPU = true
		case "memory":
			memSum += *s.Value
			hasMem = true
		}
	}
	if len(pods) == 0 {
		return nil
	}
	if collectedAt <= 0 {
		collectedAt = time.Now().UnixMilli()
	}
	snap := monitorResourceSnapshot{
		Cluster:         req.Cluster,
		Instance:        req.Instance,
		AssetID:         "k8s-ns:" + req.Namespace,
		AssetName:       req.Cluster + "/" + req.Namespace + " Pods",
		AssetKind:       "app",
		Strategy:        "k8s_top",
		CollectorMethod: "k8s_api",
		CollectedAtMs:   collectedAt,
		Status:          "ok",
		Tags:            []string{},
		Details: map[string]any{
			"source":    "asset_monitor_collect",
			"namespace": req.Namespace,
			"pod_count": len(pods),
		},
	}
	if hasCPU {
		snap.CPUCores = &cpuSum
	}
	if hasMem {
		snap.MemoryUsedBytes = &memSum
	}
	return insertMonitorResourceSnapshot(ctx, tx, "", snap)
}
