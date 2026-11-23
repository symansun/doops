package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/user/doops/agent/api"
)

func (gw *Gateway) handleToolMonitorInspect(call toolInvocation) {
	ctx, reqID, argBytes, writeJSON := call.ctx, call.reqID, call.args, call.writeJSON
	var params api.MonitorInspectParams
	if err := json.Unmarshal(argBytes, &params); err != nil {
		writeJSON(buildErrorResponse(reqID, -32602, "invalid monitor inspect params"))
		return
	}
	if len(params.Assets) == 0 {
		writeJSON(buildErrorResponse(reqID, -32602, "monitor inspect assets required"))
		return
	}
	if len(params.Assets) > 64 {
		params.Assets = params.Assets[:64]
	}
	thresholds := normalizeMonitorThresholds(params.Thresholds)
	runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	resp := api.MonitorInspectResponse{
		RunID:         params.RunID,
		CollectedAtMs: time.Now().UnixMilli(),
		Results:       make([]api.MonitorInspectAssetResult, 0, len(params.Assets)),
	}

	for _, asset := range params.Assets {
		if runCtx.Err() != nil {
			resp.Results = append(resp.Results, api.MonitorInspectAssetResult{
				AssetID:         asset.AssetID,
				AssetName:       asset.AssetName,
				AssetKind:       asset.AssetKind,
				Strategy:        asset.Strategy,
				CollectorMethod: strategyCollectorMethod(asset.Strategy),
				Status:          "error",
				Reason:          "monitor_inspect_timeout",
				CollectedAtMs:   time.Now().UnixMilli(),
				Tags:            asset.Tags,
			})
			continue
		}
		res := executeAgentMonitorStrategy(runCtx, asset, thresholds)
		res.Tags = asset.Tags
		if res.AssetName == "" {
			res.AssetName = asset.AssetName
		}
		if res.AssetName == "" {
			res.AssetName = res.AssetID
		}
		res.Evidence = sanitizeMonitorEvidence(res.Evidence)
		resp.Results = append(resp.Results, res)
	}

	encoded, err := json.Marshal(resp)
	if err != nil {
		writeJSON(buildErrorResponse(reqID, -32603, "encode monitor inspect response failed"))
		return
	}
	writeJSON(buildSuccessResponse(reqID, string(encoded)))
}

func strategyCollectorMethod(strategy string) string {
	switch {
	case strategy == "command":
		return "shell_command"
	case strategy == "custom_prompt":
		return "agent_prompt"
	case strings.HasPrefix(strategy, "skill:"):
		return "agent_skill"
	}
	for _, m := range monitorStrategyCatalog {
		if m.ID == strategy {
			return m.CollectorMethod
		}
	}
	return "agent_local"
}

func executeAgentMonitorStrategy(ctx context.Context, asset api.MonitorInspectAsset, th api.MonitorInspectThresholds) api.MonitorInspectAssetResult {
	started := time.Now()
	var res api.MonitorInspectAssetResult
	switch asset.Strategy {
	case monitorStrategyVMHost:
		res = executeVMHostStrategy(ctx, asset, th)
	case monitorStrategySSHHost:
		res = executeSSHHostStrategy(ctx, asset, th)
	case monitorStrategyK8sCluster:
		res = executeK8sClusterStrategy(ctx, asset, th)
	case monitorStrategyContainerRuntime:
		res = executeContainerRuntimeStrategy(ctx, asset, th)
	case monitorStrategyAppService:
		res = executeAppServiceStrategy(ctx, asset, th)
	case monitorStrategyDatabaseService:
		res = executeDatabaseServiceStrategy(ctx, asset, th)
	default:
		res = api.MonitorInspectAssetResult{
			AssetID:         asset.AssetID,
			AssetKind:       asset.AssetKind,
			Strategy:        asset.Strategy,
			CollectorMethod: "agent_local",
			Status:          "skipped",
			Reason:          "unsupported_strategy",
			CollectedAtMs:   time.Now().UnixMilli(),
		}
	}
	res.DurationMs = time.Since(started).Milliseconds()
	if res.CollectedAtMs == 0 {
		res.CollectedAtMs = time.Now().UnixMilli()
	}
	return res
}

// ---------------- Strategy 1: VM / Physical Host (Local Agent) ----------------

type hostGPUInfo struct {
	UUID        string   `json:"uuid"`
	Name        string   `json:"name,omitempty"`
	UtilPercent float64  `json:"util_percent"`
	MemUsedMiB  *float64 `json:"mem_used_mib,omitempty"`
	MemTotalMiB *float64 `json:"mem_total_mib,omitempty"`
	TempC       *float64 `json:"temp_c,omitempty"`
}

func executeVMHostStrategy(ctx context.Context, asset api.MonitorInspectAsset, th api.MonitorInspectThresholds) api.MonitorInspectAssetResult {
	res := api.MonitorInspectAssetResult{
		AssetID:         asset.AssetID,
		AssetName:       asset.AssetName,
		AssetKind:       asset.AssetKind,
		Strategy:        monitorStrategyVMHost,
		CollectorMethod: "agent_local",
		Checks:          []api.MonitorInspectCheck{},
		Details:         map[string]any{},
		Evidence:        map[string]any{},
		SeriesPoints:    []api.MonitorInspectSeriesPoint{},
	}
	if res.AssetKind == "" {
		res.AssetKind = "vm"
	}

	raw, err := collectAgentVMMetricsJSON(ctx)
	if err != nil {
		res.Status = "error"
		res.Reason = "vm_metrics_collect_failed"
		return res
	}
	var snap agentVMSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		res.Status = "error"
		res.Reason = "invalid_vm_metrics_snapshot"
		return res
	}
	at := snap.AtMs
	if at <= 0 {
		at = time.Now().UnixMilli()
	}
	res.CollectedAtMs = at

	totalCores := float64(runtime.NumCPU())
	res.Metrics.CPUTotalCores = monitorFloatPtr(totalCores)
	res.Metrics.CPUCores = snap.CPUCores
	res.Metrics.KernelCPUCores = snap.KernelCPUCores
	res.Metrics.MemoryUsedBytes = snap.MemoryUsedBytes
	res.Metrics.KernelSlabBytes = snap.KernelSlabBytes

	if snap.CPUCores != nil && totalCores > 0 {
		pct := (*snap.CPUCores / totalCores) * 100.0
		if pct > 100 {
			pct = 100
		}
		res.Metrics.CPUPercent = monitorFloatPtr(pct)
	}

	memTotal := readHostTotalMemoryBytes(ctx)
	if memTotal != nil && *memTotal > 0 {
		res.Metrics.MemoryTotalBytes = monitorFloatPtr(*memTotal)
		if snap.MemoryUsedBytes != nil {
			pct := (*snap.MemoryUsedBytes / *memTotal) * 100.0
			if pct > 100 {
				pct = 100
			}
			res.Metrics.MemoryPercent = monitorFloatPtr(pct)
		}
	}

	// Aggregate mount storage and detect worst mount utilization.
	var totalDisk, usedDisk float64
	maxMountPct := -1.0
	worstMount := ""
	mountDetails := make([]map[string]any, 0, len(snap.Mounts))
	for _, m := range snap.Mounts {
		totalDisk += m.TotalBytes
		usedDisk += m.UsedBytes
		var pct float64
		if m.TotalBytes > 0 {
			pct = (m.UsedBytes / m.TotalBytes) * 100.0
			if pct > maxMountPct {
				maxMountPct = pct
				worstMount = m.Mount
			}
		}
		mountDetails = append(mountDetails, map[string]any{
			"mount":       m.Mount,
			"total_bytes": m.TotalBytes,
			"used_bytes":  m.UsedBytes,
			"avail_bytes": m.AvailBytes,
			"used_pct":    math.Round(pct*10) / 10,
		})
	}
	if totalDisk > 0 {
		res.Metrics.DiskTotalBytes = monitorFloatPtr(totalDisk)
		res.Metrics.DiskUsedBytes = monitorFloatPtr(usedDisk)
		if maxMountPct >= 0 {
			res.Metrics.DiskPercent = monitorFloatPtr(maxMountPct)
		} else {
			res.Metrics.DiskPercent = monitorFloatPtr((usedDisk / totalDisk) * 100.0)
		}
	}
	res.Details["mounts"] = mountDetails

	// Network throughput aggregate.
	var netSum float64
	for _, n := range snap.Networks {
		netSum += n.BytesPerSec
	}
	if len(snap.Networks) > 0 {
		res.Metrics.NetBytesPerSec = monitorFloatPtr(netSum)
		res.Details["networks"] = snap.Networks
	}

	// Disk I/O aggregate + max latency.
	var iopsSum float64
	var maxLatency float64
	hasDiskIO := false
	hasLatency := false
	for _, d := range snap.Disks {
		if d.ReadIOPS != nil {
			iopsSum += *d.ReadIOPS
			hasDiskIO = true
		}
		if d.WriteIOPS != nil {
			iopsSum += *d.WriteIOPS
			hasDiskIO = true
		}
		if d.ReadLatencyMs != nil && *d.ReadLatencyMs >= maxLatency {
			maxLatency = *d.ReadLatencyMs
			hasLatency = true
		}
		if d.WriteLatencyMs != nil && *d.WriteLatencyMs >= maxLatency {
			maxLatency = *d.WriteLatencyMs
			hasLatency = true
		}
	}
	if hasDiskIO {
		res.Metrics.DiskIOPS = monitorFloatPtr(iopsSum)
	}
	if hasLatency {
		res.Metrics.DiskLatencyMs = monitorFloatPtr(maxLatency)
	}
	if len(snap.Disks) > 0 {
		res.Details["disks"] = snap.Disks
	}

	// NVIDIA GPUs (if available).
	gpus := collectLocalGPUs(ctx)
	if len(gpus) > 0 {
		var maxGPU float64
		for _, g := range gpus {
			if g.UtilPercent > maxGPU {
				maxGPU = g.UtilPercent
			}
			v := g.UtilPercent
			res.SeriesPoints = append(res.SeriesPoints, api.MonitorInspectSeriesPoint{
				Type: "machine", AssetID: asset.AssetID, Origin: "agent_host",
				Metric: "gpu", DeviceID: g.UUID, Unit: "%", AtMs: at, Status: "ok", Value: &v,
			})
		}
		res.Metrics.GPUPercent = monitorFloatPtr(maxGPU)
		res.Details["gpus"] = gpus
	}

	// OS, Kernel, Uptime, Load1m, Processes.
	osInfo, kernelInfo := readHostOSKernel(ctx)
	res.Metrics.OSInfo = osInfo
	res.Metrics.KernelInfo = kernelInfo
	uptimeSec, bootAt := readHostUptime(ctx)
	if uptimeSec != nil {
		res.Metrics.UptimeSeconds = monitorFloatPtr(*uptimeSec)
	}
	if load1 := readHostLoad1m(ctx); load1 != nil {
		res.Metrics.Load1m = monitorFloatPtr(*load1)
	}

	// Build time-series bridge points for asset_monitor_series.
	addPoint := func(kind, metric, device string, val *float64) {
		unit := monitorUnits[metric]
		if unit == "" || !validMonitorDevice(metric, device) || !monitorID(asset.AssetID) {
			return
		}
		pt := api.MonitorInspectSeriesPoint{
			Type: kind, AssetID: asset.AssetID, Origin: "agent_host",
			Metric: metric, DeviceID: device, Unit: unit, AtMs: at, Status: "missing",
		}
		if val != nil && monitorValidValue(metric, *val) {
			v := *val
			pt.Value = &v
			pt.Status = "ok"
		}
		res.SeriesPoints = append(res.SeriesPoints, pt)
	}
	addPoint("machine", "cpu", "", snap.CPUCores)
	addPoint("machine", "memory", "", snap.MemoryUsedBytes)
	if snap.KernelCPUCores != nil || snap.KernelSlabBytes != nil {
		addPoint("kernel", "cpu", "", snap.KernelCPUCores)
		addPoint("kernel", "memory", "", snap.KernelSlabBytes)
	}
	for _, n := range snap.Networks {
		v := n.BytesPerSec
		addPoint("machine", "network", n.DeviceID, &v)
	}
	for _, m := range snap.Mounts {
		tot, used, avail := m.TotalBytes, m.UsedBytes, m.AvailBytes
		addPoint("machine", "storage_total", m.Mount, &tot)
		addPoint("machine", "storage", m.Mount, &used)
		addPoint("machine", "storage_available", m.Mount, &avail)
	}
	for _, d := range snap.Disks {
		addPoint("machine", "disk_read_iops", d.DeviceID, d.ReadIOPS)
		addPoint("machine", "disk_write_iops", d.DeviceID, d.WriteIOPS)
		addPoint("machine", "disk_read_bytes_per_second", d.DeviceID, d.ReadBytesPerSec)
		addPoint("machine", "disk_write_bytes_per_second", d.DeviceID, d.WriteBytesPerSec)
		addPoint("machine", "disk_read_latency_ms", d.DeviceID, d.ReadLatencyMs)
		addPoint("machine", "disk_write_latency_ms", d.DeviceID, d.WriteLatencyMs)
	}

	// ---- Inspection Checks (M1 - M5 + Disk + Disk I/O) ----
	// M1: Uptime & reboot detection
	if uptimeSec != nil {
		st := "pass"
		if int64(*uptimeSec) < th.MinUptimeSeconds {
			st = "warning"
		}
		detail := fmt.Sprintf("运行时长 %.0f 秒", *uptimeSec)
		if bootAt != "" {
			detail += " · 启动于 " + bootAt
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "host.uptime", Name: "运行与重启 (M1)", Category: "availability",
			Status: st, Value: fmt.Sprintf("%.0fs", *uptimeSec),
			Threshold: fmt.Sprintf(">=%ds", th.MinUptimeSeconds), Detail: detail,
		})
	} else {
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "host.uptime", Name: "运行与重启 (M1)", Category: "availability",
			Status: "skipped", Detail: "无法读取运行时长",
		})
	}

	// M2: CPU & Memory watermarks
	res.Checks = append(res.Checks, evaluateHostCPUMemCheck(res.Metrics.CPUPercent, res.Metrics.MemoryPercent, res.Metrics.CPUCores, res.Metrics.CPUTotalCores, th))

	// Disk Capacity
	if maxMountPct >= 0 {
		st := "pass"
		if maxMountPct >= th.DiskCritPercent {
			st = "critical"
		} else if maxMountPct >= th.DiskWarnPercent {
			st = "warning"
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "host.disk_capacity", Name: "磁盘存储容量", Category: "resource",
			Status: st, Value: fmt.Sprintf("%s %.1f%%", worstMount, maxMountPct),
			Threshold: fmt.Sprintf("告警 %.0f%% / 严重 %.0f%%", th.DiskWarnPercent, th.DiskCritPercent),
			Detail:    fmt.Sprintf("共检查 %d 个挂载点，最高占用 %s (%.1f%%)", len(snap.Mounts), worstMount, maxMountPct),
		})
	} else {
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "host.disk_capacity", Name: "磁盘存储容量", Category: "resource",
			Status: "skipped", Detail: "未检测到挂载点容量数据",
		})
	}

	// Disk I/O Latency
	if hasDiskIO {
		st := "pass"
		if hasLatency && maxLatency >= th.DiskLatencyWarnMs {
			st = "warning"
		}
		val := fmt.Sprintf("%.1f ops/s", iopsSum)
		if hasLatency {
			val += fmt.Sprintf(" / 峰值延迟 %.1f ms", maxLatency)
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "host.disk_io", Name: "磁盘 I/O 与延迟", Category: "resource",
			Status: st, Value: val,
			Threshold: fmt.Sprintf("延迟 <%.0f ms", th.DiskLatencyWarnMs),
			Detail:    fmt.Sprintf("监控 %d 块物理磁盘", len(snap.Disks)),
		})
	} else {
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "host.disk_io", Name: "磁盘 I/O 与延迟", Category: "resource",
			Status: "skipped", Detail: "当前平台无 /proc/diskstats 物理块设备计数器",
		})
	}

	// M3: Processes (total, zombies, denied list)
	procTotal, zombies, deniedHits := inspectLocalProcesses(ctx, th.DeniedProcesses)
	if procTotal > 0 {
		st := "pass"
		if len(deniedHits) > 0 {
			st = "critical"
		} else if zombies > 5 {
			st = "warning"
		}
		detail := fmt.Sprintf("总进程 %d，僵尸进程 %d", procTotal, zombies)
		if len(deniedHits) > 0 {
			detail += fmt.Sprintf("，命中可疑进程: %s", strings.Join(deniedHits, ","))
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "host.processes", Name: "进程与可疑进程 (M3)", Category: "security",
			Status: st, Value: fmt.Sprintf("%d 进程 / %d 僵尸", procTotal, zombies),
			Threshold: "无黑名单进程、僵尸 <=5", Detail: detail,
		})
		res.Details["processes"] = map[string]any{"total": procTotal, "zombies": zombies, "denied_hits": deniedHits}
	}

	// M4: Linux Sysctl Security Baseline
	if runtime.GOOS == "linux" {
		sysctlCheck, sysctlMap := inspectLinuxSysctlBaseline()
		res.Checks = append(res.Checks, sysctlCheck)
		res.Details["sysctl"] = sysctlMap
		// M5: Auth & Privilege File Fingerprints
		authCheck, authEvidence := inspectLinuxAuthSecurity()
		res.Checks = append(res.Checks, authCheck)
		res.Evidence["auth_security"] = authEvidence
	} else {
		res.Checks = append(res.Checks,
			api.MonitorInspectCheck{ID: "host.sysctl_compliance", Name: "内核配置合规 (M4)", Category: "compliance", Status: "skipped", Detail: "仅适用于 Linux 宿主机"},
			api.MonitorInspectCheck{ID: "host.auth_security", Name: "登录与权限基线 (M5)", Category: "security", Status: "skipped", Detail: "仅适用于 Linux 宿主机"},
		)
	}

	res.Evidence["os"] = osInfo
	res.Evidence["kernel"] = kernelInfo
	res.Evidence["cpu_total_cores"] = totalCores
	res.Status = aggregateCheckStatus(res.Checks)
	if res.Status == "skipped" {
		res.Status = "pass"
	}
	return res
}

func evaluateHostCPUMemCheck(cpuPct, memPct, cpuCores, cpuTotal *float64, th api.MonitorInspectThresholds) api.MonitorInspectCheck {
	st := "pass"
	parts := []string{}
	if cpuPct != nil {
		parts = append(parts, fmt.Sprintf("CPU %.1f%%", *cpuPct))
		if *cpuPct >= th.CPUCritPercent {
			st = "critical"
		} else if *cpuPct >= th.CPUWarnPercent && st == "pass" {
			st = "warning"
		}
	} else if cpuCores != nil {
		parts = append(parts, fmt.Sprintf("CPU %.2f cores", *cpuCores))
	}
	if memPct != nil {
		parts = append(parts, fmt.Sprintf("内存 %.1f%%", *memPct))
		if *memPct >= th.MemCritPercent {
			st = "critical"
		} else if *memPct >= th.MemWarnPercent && st == "pass" {
			st = "warning"
		}
	}
	if len(parts) == 0 {
		return api.MonitorInspectCheck{
			ID: "host.cpu_memory", Name: "CPU 与内存水位 (M2)", Category: "resource",
			Status: "skipped", Detail: "未能读取 CPU 或内存样本",
		}
	}
	detail := strings.Join(parts, " · ")
	if cpuCores != nil && cpuTotal != nil {
		detail += fmt.Sprintf(" (%.2f / %.0f 核)", *cpuCores, *cpuTotal)
	}
	return api.MonitorInspectCheck{
		ID: "host.cpu_memory", Name: "CPU 与内存水位 (M2)", Category: "resource",
		Status: st, Value: strings.Join(parts, " / "),
		Threshold: fmt.Sprintf("CPU <%.0f%%/%.0f%% · 内存 <%.0f%%/%.0f%%", th.CPUWarnPercent, th.CPUCritPercent, th.MemWarnPercent, th.MemCritPercent),
		Detail:    detail,
	}
}

func readHostTotalMemoryBytes(ctx context.Context) *float64 {
	if runtime.GOOS == "linux" {
		raw, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				f := strings.Fields(line)
				if len(f) >= 2 {
					if kb, err := strconv.ParseUint(f[1], 10, 64); err == nil {
						v := float64(kb * 1024)
						return &v
					}
				}
			}
		}
		return nil
	}
	if out, err := exec.CommandContext(ctx, "sysctl", "-n", "hw.memsize").Output(); err == nil {
		if b, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64); err == nil && b > 0 {
			v := float64(b)
			return &v
		}
	}
	return nil
}

func readHostOSKernel(ctx context.Context) (string, string) {
	osName := runtime.GOOS
	if raw, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				osName = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
				break
			}
		}
	} else if runtime.GOOS == "darwin" {
		if out, err := exec.CommandContext(ctx, "sw_vers", "-productVersion").Output(); err == nil {
			osName = "macOS " + strings.TrimSpace(string(out))
		}
	}
	kernel := ""
	if out, err := exec.CommandContext(ctx, "uname", "-sr").Output(); err == nil {
		kernel = strings.TrimSpace(string(out))
	}
	return osName, kernel
}

func readHostUptime(ctx context.Context) (*float64, string) {
	if runtime.GOOS == "linux" {
		raw, err := os.ReadFile("/proc/uptime")
		if err != nil {
			return nil, ""
		}
		f := strings.Fields(string(raw))
		if len(f) >= 1 {
			if sec, err := strconv.ParseFloat(f[0], 64); err == nil && sec >= 0 {
				boot := time.Now().Add(-time.Duration(sec) * time.Second).UTC().Format(time.RFC3339)
				return &sec, boot
			}
		}
		return nil, ""
	}
	if out, err := exec.CommandContext(ctx, "sysctl", "-n", "kern.boottime").Output(); err == nil {
		re := regexp.MustCompile(`sec\s*=\s*(\d+)`)
		if m := re.FindStringSubmatch(string(out)); m != nil {
			if secEpoch, err := strconv.ParseInt(m[1], 10, 64); err == nil && secEpoch > 0 {
				up := time.Since(time.Unix(secEpoch, 0)).Seconds()
				if up >= 0 {
					return &up, time.Unix(secEpoch, 0).UTC().Format(time.RFC3339)
				}
			}
		}
	}
	return nil, ""
}

func readHostLoad1m(ctx context.Context) *float64 {
	if runtime.GOOS == "linux" {
		raw, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return nil
		}
		f := strings.Fields(string(raw))
		if len(f) >= 1 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil && v >= 0 {
				return &v
			}
		}
		return nil
	}
	if out, err := exec.CommandContext(ctx, "sysctl", "-n", "vm.loadavg").Output(); err == nil {
		s := strings.Trim(strings.TrimSpace(string(out)), "{}")
		f := strings.Fields(s)
		if len(f) >= 1 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil && v >= 0 {
				return &v
			}
		}
	}
	return nil
}

func collectLocalGPUs(ctx context.Context) []hostGPUInfo {
	bin, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil
	}
	gCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(gCtx, bin,
		"--query-gpu=uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	var gpus []hostGPUInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) < 3 {
			continue
		}
		uuid := strings.TrimSpace(parts[0])
		if !monitorGPUUUID.MatchString(uuid) {
			continue
		}
		util, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
		if err != nil || util < 0 || util > 100 {
			continue
		}
		row := hostGPUInfo{UUID: uuid, Name: strings.TrimSpace(parts[1]), UtilPercent: util}
		if len(parts) >= 5 {
			if mu, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64); err == nil && mu >= 0 {
				row.MemUsedMiB = &mu
			}
			if mt, err := strconv.ParseFloat(strings.TrimSpace(parts[4]), 64); err == nil && mt > 0 {
				row.MemTotalMiB = &mt
			}
		}
		if len(parts) >= 6 {
			if tc, err := strconv.ParseFloat(strings.TrimSpace(parts[5]), 64); err == nil {
				row.TempC = &tc
			}
		}
		gpus = append(gpus, row)
	}
	return gpus
}

func inspectLocalProcesses(ctx context.Context, denied []string) (total int, zombies int, hits []string) {
	deniedSet := make(map[string]bool, len(denied))
	for _, d := range denied {
		deniedSet[strings.ToLower(d)] = true
	}
	hitSet := make(map[string]bool)
	if runtime.GOOS == "linux" {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return 0, 0, nil
		}
		for _, e := range entries {
			if !e.IsDir() || len(e.Name()) == 0 || e.Name()[0] < '0' || e.Name()[0] > '9' {
				continue
			}
			total++
			if raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat")); err == nil {
				if idx := strings.LastIndexByte(string(raw), ')'); idx >= 0 && idx+2 < len(raw) {
					fields := strings.Fields(string(raw[idx+1:]))
					if len(fields) > 0 && fields[0] == "Z" {
						zombies++
					}
				}
			}
			if len(deniedSet) > 0 {
				if comm, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm")); err == nil {
					name := strings.TrimSpace(string(comm))
					if deniedSet[strings.ToLower(name)] {
						hitSet[name] = true
					}
				}
			}
		}
	} else {
		pCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		out, err := exec.CommandContext(pCtx, "ps", "-A", "-o", "state=,comm=").Output()
		if err != nil {
			return 0, 0, nil
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			total++
			if strings.HasPrefix(f[0], "Z") {
				zombies++
			}
			if len(deniedSet) > 0 {
				base := filepath.Base(f[1])
				if deniedSet[strings.ToLower(base)] {
					hitSet[base] = true
				}
			}
		}
	}
	for k := range hitSet {
		hits = append(hits, k)
	}
	sort.Strings(hits)
	return total, zombies, hits
}

var defaultLinuxSysctlBaseline = map[string]int{
	"kernel.randomize_va_space": 2,
	"fs.protected_hardlinks":    1,
	"fs.protected_symlinks":     1,
	"kernel.kptr_restrict":      1,
}

func inspectLinuxSysctlBaseline() (api.MonitorInspectCheck, map[string]any) {
	details := make(map[string]any)
	var mismatches []string
	checked := 0
	for key, expected := range defaultLinuxSysctlBaseline {
		p := filepath.Join(append([]string{"/proc/sys"}, strings.Split(key, ".")...)...)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		actual, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			continue
		}
		checked++
		details[key] = map[string]int{"actual": actual, "expected": expected}
		if key == "kernel.kptr_restrict" {
			if actual < expected {
				mismatches = append(mismatches, fmt.Sprintf("%s=%d(期望>=%d)", key, actual, expected))
			}
		} else if actual != expected {
			mismatches = append(mismatches, fmt.Sprintf("%s=%d(期望=%d)", key, actual, expected))
		}
	}
	sort.Strings(mismatches)
	if checked == 0 {
		return api.MonitorInspectCheck{
			ID: "host.sysctl_compliance", Name: "内核配置合规 (M4)", Category: "compliance",
			Status: "skipped", Detail: "无法读取 /proc/sys 内核参数",
		}, details
	}
	st := "pass"
	detail := fmt.Sprintf("已核查 %d 项内核安全基线，全部合规", checked)
	if len(mismatches) > 0 {
		st = "warning"
		detail = fmt.Sprintf("已核查 %d 项，偏离 %d 项：%s", checked, len(mismatches), strings.Join(mismatches, "; "))
	}
	return api.MonitorInspectCheck{
		ID: "host.sysctl_compliance", Name: "内核配置合规 (M4)", Category: "compliance",
		Status: st, Value: fmt.Sprintf("%d/%d 合规", checked-len(mismatches), checked),
		Threshold: "全部基线符合", Detail: detail,
	}, details
}

func inspectLinuxAuthSecurity() (api.MonitorInspectCheck, map[string]any) {
	evidence := map[string]any{}
	hashes := map[string]string{}
	for _, f := range []string{"/etc/passwd", "/etc/group"} {
		if raw, err := os.ReadFile(f); err == nil {
			sum := sha256.Sum256(raw)
			hashes[f] = hex.EncodeToString(sum[:8])
		}
	}
	evidence["account_file_hashes"] = hashes

	// Scan tail of standard Linux auth log (non-privileged if readable) for recent failed SSH logins.
	failedLogins := -1
	logSource := ""
	for _, logPath := range []string{"/var/log/auth.log", "/var/log/secure"} {
		f, err := os.Open(logPath)
		if err != nil {
			continue
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			continue
		}
		const maxTail = 256 * 1024
		start := int64(0)
		if info.Size() > maxTail {
			start = info.Size() - maxTail
		}
		_, _ = f.Seek(start, io.SeekStart)
		buf, _ := io.ReadAll(io.LimitReader(f, maxTail))
		f.Close()
		logSource = logPath
		failedLogins = 0
		for _, line := range strings.Split(string(buf), "\n") {
			if strings.Contains(line, "Failed password") || strings.Contains(line, "authentication failure") || strings.Contains(line, "Invalid user") {
				failedLogins++
			}
		}
		break
	}
	evidence["auth_log_source"] = logSource
	if failedLogins >= 0 {
		evidence["recent_failed_logins"] = failedLogins
		st := "pass"
		if failedLogins >= 50 {
			st = "critical"
		} else if failedLogins >= 10 {
			st = "warning"
		}
		return api.MonitorInspectCheck{
			ID: "host.auth_security", Name: "登录与权限基线 (M5)", Category: "security",
			Status: st, Value: fmt.Sprintf("失败登录 %d 次", failedLogins),
			Threshold: "<10 告警 / <50 严重",
			Detail:    fmt.Sprintf("已核对账户文件指纹并扫描 %s 尾部日志，失败认证 %d 次", logSource, failedLogins),
		}, evidence
	}
	return api.MonitorInspectCheck{
		ID: "host.auth_security", Name: "登录与权限基线 (M5)", Category: "security",
		Status: "pass", Value: fmt.Sprintf("已记录 %d 个账户文件指纹", len(hashes)),
		Threshold: "账户基线可核验",
		Detail:    "认证日志无读取权限或未启用，已记录 /etc/passwd 与 /etc/group 基线指纹",
	}, evidence
}

// ---------------- Strategy 2: Remote Host via Structured Read-Only SSH ----------------

// Compile-time constant read-only POSIX probe script. Never interpolates user input.
const fixedReadOnlySSHProbeScript = `
set -u
echo "===OS==="
if [ -r /etc/os-release ]; then grep -m1 '^PRETTY_NAME=' /etc/os-release | sed 's/^PRETTY_NAME=//;s/"//g'; else uname -s; fi
echo "===KERNEL==="
uname -sr 2>/dev/null || true
echo "===NPROC==="
nproc 2>/dev/null || grep -c '^processor' /proc/cpuinfo 2>/dev/null || echo 1
echo "===UPTIME==="
cat /proc/uptime 2>/dev/null || true
echo "===LOAD==="
cat /proc/loadavg 2>/dev/null || true
echo "===CPU0==="
head -n1 /proc/stat 2>/dev/null || true
echo "===NET0==="
for d in /sys/class/net/*; do n=$(basename "$d"); [ "$n" = "lo" ] && continue; [ -r "$d/statistics/rx_bytes" ] && echo "$n $(cat "$d/statistics/rx_bytes") $(cat "$d/statistics/tx_bytes")"; done 2>/dev/null || true
sleep 1
echo "===CPU1==="
head -n1 /proc/stat 2>/dev/null || true
echo "===NET1==="
for d in /sys/class/net/*; do n=$(basename "$d"); [ "$n" = "lo" ] && continue; [ -r "$d/statistics/rx_bytes" ] && echo "$n $(cat "$d/statistics/rx_bytes") $(cat "$d/statistics/tx_bytes")"; done 2>/dev/null || true
echo "===MEM==="
grep -E '^(MemTotal|MemAvailable|Slab):' /proc/meminfo 2>/dev/null || true
echo "===DF==="
df -P -k 2>/dev/null | tail -n +2 || true
echo "===PS==="
ps -eo stat= 2>/dev/null | awk '{t++; if(substr($1,1,1)=="Z") z++} END{printf "%d %d\n", t+0, z+0}'
echo "===SYSCTL==="
for k in kernel/randomize_va_space fs/protected_hardlinks fs/protected_symlinks kernel/kptr_restrict; do [ -r "/proc/sys/$k" ] && echo "$k $(cat "/proc/sys/$k")"; done 2>/dev/null || true
`

var sshUsernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

func executeSSHHostStrategy(ctx context.Context, asset api.MonitorInspectAsset, th api.MonitorInspectThresholds) api.MonitorInspectAssetResult {
	res := api.MonitorInspectAssetResult{
		AssetID:         asset.AssetID,
		AssetName:       asset.AssetName,
		AssetKind:       asset.AssetKind,
		Strategy:        monitorStrategySSHHost,
		CollectorMethod: "ssh",
		Checks:          []api.MonitorInspectCheck{},
		Details:         map[string]any{},
		Evidence:        map[string]any{},
		SeriesPoints:    []api.MonitorInspectSeriesPoint{},
	}
	if res.AssetKind == "" {
		res.AssetKind = "vm"
	}
	addr := strings.TrimSpace(asset.Address)
	if addr == "" || !catalogHost(addr) {
		res.Status = "skipped"
		res.Reason = "ssh_address_missing"
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "ssh.connectivity", Name: "SSH 连通性", Category: "availability",
			Status: "skipped", Detail: "资产未配置有效主机地址",
		})
		return res
	}
	hostSpec := addr
	if user := strings.TrimSpace(asset.SSHUser); user != "" {
		if !sshUsernamePattern.MatchString(user) {
			res.Status = "error"
			res.Reason = "invalid_ssh_user"
			return res
		}
		hostSpec = user + "@" + addr
	}
	opts := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=8",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if asset.Port > 0 && asset.Port <= 65535 {
		opts = append(opts, "-p", strconv.Itoa(asset.Port))
	}

	sshCtx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	cmd, stdoutPipe, stderrPipe, err := executeStructuredSSHCommand(sshCtx, api.SSHParams{
		Host:    hostSpec,
		Options: opts,
		Command: []string{"sh", "-c", fixedReadOnlySSHProbeScript},
	})
	if err != nil {
		res.Status = "error"
		res.Reason = "ssh_start_failed"
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "ssh.connectivity", Name: "SSH 连通性", Category: "availability",
			Status: "critical", Detail: sanitizeSecretText(err.Error()),
		})
		return res
	}
	var outBuf, errBuf bytes.Buffer
	doneOut := make(chan struct{})
	go func() {
		_, _ = io.Copy(&outBuf, io.LimitReader(stdoutPipe, 512*1024))
		close(doneOut)
	}()
	_, _ = io.Copy(&errBuf, io.LimitReader(stderrPipe, 64*1024))
	<-doneOut
	waitErr := cmd.Wait()
	if waitErr != nil {
		res.Status = "error"
		res.Reason = "ssh_exec_failed"
		errMsg := strings.TrimSpace(sanitizeSecretText(errBuf.String()))
		if errMsg == "" {
			errMsg = waitErr.Error()
		}
		if len(errMsg) > 240 {
			errMsg = errMsg[:240]
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "ssh.connectivity", Name: "SSH 连通性", Category: "availability",
			Status: "critical", Detail: fmt.Sprintf("SSH 采集失败 (%s): %s", addr, errMsg),
		})
		return res
	}

	at := time.Now().UnixMilli()
	res.CollectedAtMs = at
	res.Checks = append(res.Checks, api.MonitorInspectCheck{
		ID: "ssh.connectivity", Name: "SSH 连通性", Category: "availability",
		Status: "pass", Value: addr, Detail: "结构化只读 SSH 采集成功",
	})

	sections := parseTaggedSections(outBuf.String())
	res.Metrics.OSInfo = strings.TrimSpace(sections["OS"])
	res.Metrics.KernelInfo = strings.TrimSpace(sections["KERNEL"])

	nproc := 1.0
	if n, err := strconv.ParseFloat(strings.TrimSpace(sections["NPROC"]), 64); err == nil && n > 0 {
		nproc = n
	}
	res.Metrics.CPUTotalCores = monitorFloatPtr(nproc)

	// Uptime & Load
	if f := strings.Fields(sections["UPTIME"]); len(f) >= 1 {
		if up, err := strconv.ParseFloat(f[0], 64); err == nil && up >= 0 {
			res.Metrics.UptimeSeconds = monitorFloatPtr(up)
			st := "pass"
			if int64(up) < th.MinUptimeSeconds {
				st = "warning"
			}
			res.Checks = append(res.Checks, api.MonitorInspectCheck{
				ID: "host.uptime", Name: "运行与重启 (M1)", Category: "availability",
				Status: st, Value: fmt.Sprintf("%.0fs", up),
				Threshold: fmt.Sprintf(">=%ds", th.MinUptimeSeconds),
				Detail:    fmt.Sprintf("远程主机运行时长 %.0f 秒", up),
			})
		}
	}
	if f := strings.Fields(sections["LOAD"]); len(f) >= 1 {
		if l1, err := strconv.ParseFloat(f[0], 64); err == nil && l1 >= 0 {
			res.Metrics.Load1m = monitorFloatPtr(l1)
		}
	}

	// CPU delta
	c0, ok0 := parseProcStatLine(sections["CPU0"])
	c1, ok1 := parseProcStatLine(sections["CPU1"])
	if ok0 && ok1 && c1.total > c0.total && c1.idle >= c0.idle {
		dtTotal := float64(c1.total - c0.total)
		dtBusy := dtTotal - float64(c1.idle-c0.idle)
		pct := (dtBusy / dtTotal) * 100.0
		cores := (dtBusy / dtTotal) * nproc
		res.Metrics.CPUPercent = monitorFloatPtr(pct)
		res.Metrics.CPUCores = monitorFloatPtr(cores)
		if c1.kernel >= c0.kernel {
			kc := (float64(c1.kernel-c0.kernel) / dtTotal) * nproc
			res.Metrics.KernelCPUCores = monitorFloatPtr(kc)
		}
	}

	// Memory
	memKV := map[string]uint64{}
	for _, line := range strings.Split(sections["MEM"], "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			if v, err := strconv.ParseUint(f[1], 10, 64); err == nil {
				memKV[strings.TrimSuffix(f[0], ":")] = v * 1024
			}
		}
	}
	if total, ok := memKV["MemTotal"]; ok && total > 0 {
		t := float64(total)
		res.Metrics.MemoryTotalBytes = monitorFloatPtr(t)
		if avail, okA := memKV["MemAvailable"]; okA && total >= avail {
			used := float64(total - avail)
			res.Metrics.MemoryUsedBytes = monitorFloatPtr(used)
			res.Metrics.MemoryPercent = monitorFloatPtr((used / t) * 100.0)
		}
	}
	if slab, ok := memKV["Slab"]; ok {
		s := float64(slab)
		res.Metrics.KernelSlabBytes = monitorFloatPtr(s)
	}
	res.Checks = append(res.Checks, evaluateHostCPUMemCheck(res.Metrics.CPUPercent, res.Metrics.MemoryPercent, res.Metrics.CPUCores, res.Metrics.CPUTotalCores, th))

	// Network delta (1s sleep in script)
	n0 := parseSSHNetLines(sections["NET0"])
	n1 := parseSSHNetLines(sections["NET1"])
	var netRows []agentNetSample
	var netSum float64
	for dev, v1 := range n1 {
		if v0, ok := n0[dev]; ok && v1 >= v0 && !isIgnoredNIC(dev) {
			bps := float64(v1 - v0)
			netSum += bps
			netRows = append(netRows, agentNetSample{DeviceID: dev, BytesPerSec: bps})
		}
	}
	sort.Slice(netRows, func(i, j int) bool { return netRows[i].DeviceID < netRows[j].DeviceID })
	if len(netRows) > 0 {
		res.Metrics.NetBytesPerSec = monitorFloatPtr(netSum)
		res.Details["networks"] = netRows
	}

	// Mounts from df -P -k
	var totDisk, usedDisk float64
	maxMountPct := -1.0
	worstMount := ""
	mounts := []map[string]any{}
	seenMount := map[string]bool{}
	for _, line := range strings.Split(sections["DF"], "\n") {
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		fsName := f[0]
		mp := f[len(f)-1]
		if !strings.HasPrefix(mp, "/") || seenMount[mp] || strings.HasPrefix(fsName, "tmpfs") || strings.HasPrefix(fsName, "devtmpfs") || fsName == "overlay" || strings.HasPrefix(mp, "/snap/") {
			continue
		}
		totK, err1 := strconv.ParseFloat(f[1], 64)
		usedK, err2 := strconv.ParseFloat(f[2], 64)
		availK, err3 := strconv.ParseFloat(f[3], 64)
		if err1 != nil || err2 != nil || err3 != nil || totK <= 0 {
			continue
		}
		seenMount[mp] = true
		totB, usedB, availB := totK*1024, usedK*1024, availK*1024
		pct := (usedB / totB) * 100.0
		totDisk += totB
		usedDisk += usedB
		if pct > maxMountPct {
			maxMountPct = pct
			worstMount = mp
		}
		mounts = append(mounts, map[string]any{
			"mount": mp, "total_bytes": totB, "used_bytes": usedB, "avail_bytes": availB, "used_pct": math.Round(pct*10) / 10,
		})
		if monitorID(asset.AssetID) && validMonitorDevice("storage", mp) {
			tCopy, uCopy, aCopy := totB, usedB, availB
			res.SeriesPoints = append(res.SeriesPoints,
				api.MonitorInspectSeriesPoint{Type: "machine", AssetID: asset.AssetID, Origin: "agent_host", Metric: "storage_total", DeviceID: mp, Unit: "bytes", AtMs: at, Status: "ok", Value: &tCopy},
				api.MonitorInspectSeriesPoint{Type: "machine", AssetID: asset.AssetID, Origin: "agent_host", Metric: "storage", DeviceID: mp, Unit: "bytes", AtMs: at, Status: "ok", Value: &uCopy},
				api.MonitorInspectSeriesPoint{Type: "machine", AssetID: asset.AssetID, Origin: "agent_host", Metric: "storage_available", DeviceID: mp, Unit: "bytes", AtMs: at, Status: "ok", Value: &aCopy},
			)
		}
	}
	if totDisk > 0 {
		res.Metrics.DiskTotalBytes = monitorFloatPtr(totDisk)
		res.Metrics.DiskUsedBytes = monitorFloatPtr(usedDisk)
		res.Metrics.DiskPercent = monitorFloatPtr(maxMountPct)
		st := "pass"
		if maxMountPct >= th.DiskCritPercent {
			st = "critical"
		} else if maxMountPct >= th.DiskWarnPercent {
			st = "warning"
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "host.disk_capacity", Name: "磁盘存储容量", Category: "resource",
			Status: st, Value: fmt.Sprintf("%s %.1f%%", worstMount, maxMountPct),
			Threshold: fmt.Sprintf("告警 %.0f%% / 严重 %.0f%%", th.DiskWarnPercent, th.DiskCritPercent),
			Detail:    fmt.Sprintf("通过 SSH 检查 %d 个挂载点，最高占用 %s (%.1f%%)", len(mounts), worstMount, maxMountPct),
		})
	}
	res.Details["mounts"] = mounts

	// Processes
	if f := strings.Fields(sections["PS"]); len(f) == 2 {
		tot, _ := strconv.Atoi(f[0])
		zomb, _ := strconv.Atoi(f[1])
		st := "pass"
		if zomb > 5 {
			st = "warning"
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "host.processes", Name: "进程与僵尸进程 (M3)", Category: "security",
			Status: st, Value: fmt.Sprintf("%d 进程 / %d 僵尸", tot, zomb),
			Threshold: "僵尸 <=5", Detail: fmt.Sprintf("远程主机总进程 %d，僵尸进程 %d", tot, zomb),
		})
	}

	// Sysctl baseline
	var mismatches []string
	checkedSysctl := 0
	for _, line := range strings.Split(sections["SYSCTL"], "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		key := strings.ReplaceAll(f[0], "/", ".")
		expected, ok := defaultLinuxSysctlBaseline[key]
		if !ok {
			continue
		}
		actual, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		checkedSysctl++
		if (key == "kernel.kptr_restrict" && actual < expected) || (key != "kernel.kptr_restrict" && actual != expected) {
			mismatches = append(mismatches, fmt.Sprintf("%s=%d", key, actual))
		}
	}
	if checkedSysctl > 0 {
		st := "pass"
		detail := fmt.Sprintf("核查 %d 项内核基线全部合规", checkedSysctl)
		if len(mismatches) > 0 {
			st = "warning"
			detail = fmt.Sprintf("偏离 %d/%d 项：%s", len(mismatches), checkedSysctl, strings.Join(mismatches, ", "))
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "host.sysctl_compliance", Name: "内核配置合规 (M4)", Category: "compliance",
			Status: st, Value: fmt.Sprintf("%d/%d 合规", checkedSysctl-len(mismatches), checkedSysctl),
			Threshold: "全部基线符合", Detail: detail,
		})
	}

	// Bridge CPU / Memory / Network to asset_monitor_series
	if monitorID(asset.AssetID) {
		if res.Metrics.CPUCores != nil {
			v := *res.Metrics.CPUCores
			res.SeriesPoints = append(res.SeriesPoints, api.MonitorInspectSeriesPoint{
				Type: "machine", AssetID: asset.AssetID, Origin: "agent_host", Metric: "cpu", Unit: "cores", AtMs: at, Status: "ok", Value: &v,
			})
		}
		if res.Metrics.MemoryUsedBytes != nil {
			v := *res.Metrics.MemoryUsedBytes
			res.SeriesPoints = append(res.SeriesPoints, api.MonitorInspectSeriesPoint{
				Type: "machine", AssetID: asset.AssetID, Origin: "agent_host", Metric: "memory", Unit: "bytes", AtMs: at, Status: "ok", Value: &v,
			})
		}
		for _, nr := range netRows {
			v := nr.BytesPerSec
			res.SeriesPoints = append(res.SeriesPoints, api.MonitorInspectSeriesPoint{
				Type: "machine", AssetID: asset.AssetID, Origin: "agent_host", Metric: "network", DeviceID: nr.DeviceID, Unit: "bytes/s", AtMs: at, Status: "ok", Value: &v,
			})
		}
	}

	res.Evidence["ssh_host"] = addr
	res.Status = aggregateCheckStatus(res.Checks)
	return res
}

func parseTaggedSections(raw string) map[string]string {
	out := map[string]string{}
	cur := ""
	var buf strings.Builder
	flush := func() {
		if cur != "" {
			out[cur] = strings.TrimSpace(buf.String())
		}
		buf.Reset()
	}
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "===") && strings.HasSuffix(t, "===") && len(t) > 6 {
			flush()
			cur = strings.TrimSuffix(strings.TrimPrefix(t, "==="), "===")
			continue
		}
		if cur != "" {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	flush()
	return out
}

func parseProcStatLine(line string) (linuxCPUTicks, bool) {
	fields := strings.Fields(line)
	if len(fields) < 8 || fields[0] != "cpu" {
		return linuxCPUTicks{}, false
	}
	vals := make([]uint64, len(fields)-1)
	var total uint64
	for i, f := range fields[1:] {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return linuxCPUTicks{}, false
		}
		vals[i] = v
		total += v
	}
	return linuxCPUTicks{total: total, idle: vals[3] + vals[4], kernel: vals[2] + vals[5] + vals[6]}, true
}

func parseSSHNetLines(raw string) map[string]uint64 {
	out := map[string]uint64{}
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		rx, e1 := strconv.ParseUint(f[1], 10, 64)
		tx, e2 := strconv.ParseUint(f[2], 10, 64)
		if e1 == nil && e2 == nil {
			out[f[0]] = rx + tx
		}
	}
	return out
}

// ---------------- Strategy 3: Kubernetes Cluster Strategy ----------------

func runKubectlReadOnly(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	bin, err := exec.LookPath("kubectl")
	if err != nil {
		return nil, fmt.Errorf("kubectl_not_found")
	}
	kCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return exec.CommandContext(kCtx, bin, args...).CombinedOutput()
}

func executeK8sClusterStrategy(ctx context.Context, asset api.MonitorInspectAsset, th api.MonitorInspectThresholds) api.MonitorInspectAssetResult {
	res := api.MonitorInspectAssetResult{
		AssetID:         asset.AssetID,
		AssetName:       asset.AssetName,
		AssetKind:       "k8s",
		Strategy:        monitorStrategyK8sCluster,
		CollectorMethod: "k8s_api",
		Checks:          []api.MonitorInspectCheck{},
		Details:         map[string]any{},
		Evidence:        map[string]any{},
		SeriesPoints:    []api.MonitorInspectSeriesPoint{},
	}

	nodesRaw, err := runKubectlReadOnly(ctx, 10*time.Second, "get", "nodes", "-o", "json")
	if err != nil {
		if strings.Contains(err.Error(), "kubectl_not_found") {
			res.Status = "skipped"
			res.Reason = "kubectl_not_installed"
			res.Checks = append(res.Checks, api.MonitorInspectCheck{
				ID: "k8s.nodes", Name: "K8s 节点健康", Category: "k8s",
				Status: "skipped", Detail: "当前 Agent 未安装 kubectl 或非 K8s 环境",
			})
			return res
		}
		res.Status = "error"
		res.Reason = "k8s_api_unreachable"
		detailMsg := trimTail(sanitizeSecretText(string(nodesRaw)), 200)
		if detailMsg == "" {
			detailMsg = trimTail(sanitizeSecretText(err.Error()), 200)
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "k8s.nodes", Name: "K8s 节点健康", Category: "k8s",
			Status: "critical", Detail: "无法访问 Kubernetes API: " + detailMsg,
		})
		return res
	}

	at := time.Now().UnixMilli()
	res.CollectedAtMs = at

	// 1. Parse Nodes
	var nodeList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
				Capacity map[string]string `json:"capacity"`
				NodeInfo struct {
					KubeletVersion string `json:"kubeletVersion"`
					OSImage        string `json:"osImage"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	_ = json.Unmarshal(nodesRaw, &nodeList)
	totalNodes := len(nodeList.Items)
	readyNodes := 0
	var notReadyNames, pressureNames []string
	var clusterCapCPU, clusterCapMem float64
	nodeRows := make([]map[string]any, 0, totalNodes)
	for _, n := range nodeList.Items {
		ready := false
		pressures := []string{}
		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready = true
			}
			if (c.Type == "MemoryPressure" || c.Type == "DiskPressure" || c.Type == "PIDPressure") && c.Status == "True" {
				pressures = append(pressures, c.Type)
			}
		}
		if ready {
			readyNodes++
		} else {
			notReadyNames = append(notReadyNames, n.Metadata.Name)
		}
		if len(pressures) > 0 {
			pressureNames = append(pressureNames, n.Metadata.Name+"("+strings.Join(pressures, ",")+")")
		}
		if c, ok := parseK8sCPUQuantity(n.Status.Capacity["cpu"]); ok {
			clusterCapCPU += c
		}
		if m, ok := parseK8sMemQuantity(n.Status.Capacity["memory"]); ok {
			clusterCapMem += m
		}
		if res.Metrics.OSInfo == "" && n.Status.NodeInfo.KubeletVersion != "" {
			res.Metrics.OSInfo = "K8s " + n.Status.NodeInfo.KubeletVersion
		}
		nodeRows = append(nodeRows, map[string]any{
			"name":      n.Metadata.Name,
			"ready":     ready,
			"pressures": pressures,
			"version":   n.Status.NodeInfo.KubeletVersion,
		})
	}
	if clusterCapCPU > 0 {
		res.Metrics.CPUTotalCores = monitorFloatPtr(clusterCapCPU)
	}
	if clusterCapMem > 0 {
		res.Metrics.MemoryTotalBytes = monitorFloatPtr(clusterCapMem)
	}
	nodeStatus := "pass"
	if len(notReadyNames) > 0 {
		nodeStatus = "critical"
	} else if len(pressureNames) > 0 {
		nodeStatus = "warning"
	}
	nodeDetail := fmt.Sprintf("就绪 %d / %d 节点", readyNodes, totalNodes)
	if len(notReadyNames) > 0 {
		nodeDetail += "；NotReady: " + strings.Join(notReadyNames, ", ")
	}
	if len(pressureNames) > 0 {
		nodeDetail += "；节点压力: " + strings.Join(pressureNames, ", ")
	}
	res.Checks = append(res.Checks, api.MonitorInspectCheck{
		ID: "k8s.nodes", Name: "K8s 节点健康与压力", Category: "k8s",
		Status: nodeStatus, Value: fmt.Sprintf("%d/%d Ready", readyNodes, totalNodes),
		Threshold: "全部节点 Ready 且无压力", Detail: nodeDetail,
	})
	res.Details["nodes"] = nodeRows

	// 2. Parse Pods across all namespaces (or scoped namespace)
	podArgs := []string{"get", "pods", "-A", "-o", "json"}
	if asset.Namespace != "" {
		podArgs = []string{"get", "pods", "-n", asset.Namespace, "-o", "json"}
	}
	var totalPods, runningPods, abnormalPods, highRestarts int
	abnormalPodList := []map[string]any{}
	if podsRaw, err := runKubectlReadOnly(ctx, 10*time.Second, podArgs...); err == nil {
		var podList struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Status struct {
					Phase             string `json:"phase"`
					ContainerStatuses []struct {
						Name         string `json:"name"`
						Ready        bool   `json:"ready"`
						RestartCount int    `json:"restartCount"`
						State        struct {
							Waiting *struct {
								Reason string `json:"reason"`
							} `json:"waiting"`
							Terminated *struct {
								Reason string `json:"reason"`
							} `json:"terminated"`
						} `json:"state"`
					} `json:"containerStatuses"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal(podsRaw, &podList) == nil {
			totalPods = len(podList.Items)
			for _, p := range podList.Items {
				if p.Status.Phase == "Succeeded" {
					continue
				}
				restarts := 0
				badReason := ""
				for _, cs := range p.Status.ContainerStatuses {
					restarts += cs.RestartCount
					if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "ContainerCreating" {
						badReason = cs.State.Waiting.Reason
					} else if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
						badReason = "OOMKilled"
					}
				}
				if p.Status.Phase == "Running" && badReason == "" {
					runningPods++
				} else {
					abnormalPods++
					if badReason == "" {
						badReason = p.Status.Phase
					}
					if len(abnormalPodList) < 30 {
						abnormalPodList = append(abnormalPodList, map[string]any{
							"namespace": p.Metadata.Namespace,
							"name":      p.Metadata.Name,
							"phase":     p.Status.Phase,
							"reason":    badReason,
							"restarts":  restarts,
						})
					}
				}
				if restarts >= 5 {
					highRestarts++
				}
			}
		}
		podSt := "pass"
		if abnormalPods > 0 {
			podSt = "critical"
		} else if highRestarts > 0 {
			podSt = "warning"
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "k8s.pods", Name: "K8s Pod 运行态与重启", Category: "k8s",
			Status: podSt, Value: fmt.Sprintf("正常 %d / 异常 %d (总 %d)", runningPods, abnormalPods, totalPods),
			Threshold: "无异常 Pod、高重启=0",
			Detail:    fmt.Sprintf("总 Pod %d，Running %d，异常 %d，高重启(>=5次) %d", totalPods, runningPods, abnormalPods, highRestarts),
		})
		res.Details["abnormal_pods"] = abnormalPodList
		res.Details["pod_summary"] = map[string]int{"total": totalPods, "running": runningPods, "abnormal": abnormalPods, "high_restarts": highRestarts}
	}

	// 3. Deployments & StatefulSets replica health
	wlArgs := []string{"get", "deployments,statefulsets", "-A", "-o", "json"}
	if asset.Namespace != "" {
		wlArgs = []string{"get", "deployments,statefulsets", "-n", asset.Namespace, "-o", "json"}
	}
	if wlRaw, err := runKubectlReadOnly(ctx, 10*time.Second, wlArgs...); err == nil {
		var wlList struct {
			Items []struct {
				Kind     string `json:"kind"`
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Spec struct {
					Replicas *int `json:"replicas"`
				} `json:"spec"`
				Status struct {
					ReadyReplicas int `json:"readyReplicas"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal(wlRaw, &wlList) == nil {
			degraded := []string{}
			for _, w := range wlList.Items {
				want := 1
				if w.Spec.Replicas != nil {
					want = *w.Spec.Replicas
				}
				if want > 0 && w.Status.ReadyReplicas < want {
					degraded = append(degraded, fmt.Sprintf("%s/%s(%d/%d)", w.Metadata.Namespace, w.Metadata.Name, w.Status.ReadyReplicas, want))
				}
			}
			st := "pass"
			detail := fmt.Sprintf("检查 %d 个控制器，副本全部就绪", len(wlList.Items))
			if len(degraded) > 0 {
				st = "warning"
				if len(degraded) > 10 {
					degraded = degraded[:10]
				}
				detail = fmt.Sprintf("副本未就绪控制器：%s", strings.Join(degraded, ", "))
			}
			res.Checks = append(res.Checks, api.MonitorInspectCheck{
				ID: "k8s.workloads", Name: "控制器副本就绪", Category: "k8s",
				Status: st, Value: fmt.Sprintf("%d/%d 正常", len(wlList.Items)-len(degraded), len(wlList.Items)),
				Threshold: "readyReplicas == replicas", Detail: detail,
			})
		}
	}

	// 4. PVC status
	pvcArgs := []string{"get", "pvc", "-A", "-o", "json"}
	if asset.Namespace != "" {
		pvcArgs = []string{"get", "pvc", "-n", asset.Namespace, "-o", "json"}
	}
	if pvcRaw, err := runKubectlReadOnly(ctx, 8*time.Second, pvcArgs...); err == nil {
		var pvcList struct {
			Items []struct {
				Metadata struct {
					Name      string `json:"name"`
					Namespace string `json:"namespace"`
				} `json:"metadata"`
				Status struct {
					Phase string `json:"phase"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal(pvcRaw, &pvcList) == nil {
			unbound := []string{}
			for _, p := range pvcList.Items {
				if p.Status.Phase != "Bound" {
					unbound = append(unbound, fmt.Sprintf("%s/%s(%s)", p.Metadata.Namespace, p.Metadata.Name, p.Status.Phase))
				}
			}
			st := "pass"
			if len(unbound) > 0 {
				st = "warning"
			}
			res.Checks = append(res.Checks, api.MonitorInspectCheck{
				ID: "k8s.pvc", Name: "PVC 存储绑定", Category: "k8s",
				Status: st, Value: fmt.Sprintf("%d/%d Bound", len(pvcList.Items)-len(unbound), len(pvcList.Items)),
				Threshold: "全部 PVC Bound", Detail: fmt.Sprintf("总 PVC %d，未绑定 %d", len(pvcList.Items), len(unbound)),
			})
		}
	}

	// 5. kubectl top nodes & pods (metrics-server)
	if topNodesRaw, err := runKubectlReadOnly(ctx, 8*time.Second, "top", "nodes", "--no-headers"); err == nil {
		var usedCPU, usedMem float64
		count := 0
		for _, line := range strings.Split(strings.TrimSpace(string(topNodesRaw)), "\n") {
			f := strings.Fields(line)
			if len(f) < 5 {
				continue
			}
			if c, ok := parseK8sCPUQuantity(f[1]); ok {
				usedCPU += c
			}
			if m, ok := parseK8sMemQuantity(f[3]); ok {
				usedMem += m
			}
			count++
		}
		if count > 0 {
			res.Metrics.CPUCores = monitorFloatPtr(usedCPU)
			res.Metrics.MemoryUsedBytes = monitorFloatPtr(usedMem)
			if clusterCapCPU > 0 {
				res.Metrics.CPUPercent = monitorFloatPtr((usedCPU / clusterCapCPU) * 100.0)
			}
			if clusterCapMem > 0 {
				res.Metrics.MemoryPercent = monitorFloatPtr((usedMem / clusterCapMem) * 100.0)
			}
			res.Checks = append(res.Checks, api.MonitorInspectCheck{
				ID: "k8s.metrics", Name: "K8s 资源指标 (metrics-server)", Category: "resource",
				Status: "pass", Value: fmt.Sprintf("%.2f cores / %.1f MiB", usedCPU, usedMem/1048576),
				Detail: fmt.Sprintf("成功采集 %d 个节点的实时 CPU 与内存占用", count),
			})
		}
	} else {
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "k8s.metrics", Name: "K8s 资源指标 (metrics-server)", Category: "resource",
			Status: "skipped", Detail: "metrics_server_unavailable：集群未部署 metrics-server 或无权访问 top 指标",
		})
	}

	// Also bridge Pod metrics into asset_monitor_series when top pods succeeds.
	topPodArgs := []string{"top", "pods", "-A", "--no-headers"}
	if asset.Namespace != "" {
		topPodArgs = []string{"top", "pods", "-n", asset.Namespace, "--no-headers"}
	}
	if topPodsRaw, err := runKubectlReadOnly(ctx, 8*time.Second, topPodArgs...); err == nil {
		podMetricsCount := 0
		for _, line := range strings.Split(strings.TrimSpace(string(topPodsRaw)), "\n") {
			f := strings.Fields(line)
			var ns, pod, cpuRaw, memRaw string
			if asset.Namespace == "" && len(f) >= 4 {
				ns, pod, cpuRaw, memRaw = f[0], f[1], f[2], f[3]
			} else if asset.Namespace != "" && len(f) >= 3 {
				ns, pod, cpuRaw, memRaw = asset.Namespace, f[0], f[1], f[2]
			} else {
				continue
			}
			if !monitorID(pod) || !inventoryNamespacePattern.MatchString(ns) || podMetricsCount >= 128 {
				continue
			}
			podMetricsCount++
			if c, ok := parseK8sCPUQuantity(cpuRaw); ok {
				v := c
				res.SeriesPoints = append(res.SeriesPoints, api.MonitorInspectSeriesPoint{
					Type: "app", AssetID: pod, Namespace: ns, Origin: "k8s_top",
					Metric: "cpu", Unit: "cores", AtMs: at, Status: "ok", Value: &v,
				})
			}
			if m, ok := parseK8sMemQuantity(memRaw); ok {
				v := m
				res.SeriesPoints = append(res.SeriesPoints, api.MonitorInspectSeriesPoint{
					Type: "app", AssetID: pod, Namespace: ns, Origin: "k8s_top",
					Metric: "memory", Unit: "bytes", AtMs: at, Status: "ok", Value: &v,
				})
			}
		}
	}

	// 6. Recent Warning Events
	evtArgs := []string{"get", "events", "-A", "--field-selector", "type=Warning", "-o", "json"}
	if asset.Namespace != "" {
		evtArgs = []string{"get", "events", "-n", asset.Namespace, "--field-selector", "type=Warning", "-o", "json"}
	}
	if evtRaw, err := runKubectlReadOnly(ctx, 8*time.Second, evtArgs...); err == nil {
		var evtList struct {
			Items []struct {
				Reason         string `json:"reason"`
				LastTimestamp  string `json:"lastTimestamp"`
				EventTime      string `json:"eventTime"`
				InvolvedObject struct {
					Kind      string `json:"kind"`
					Namespace string `json:"namespace"`
					Name      string `json:"name"`
				} `json:"involvedObject"`
			} `json:"items"`
		}
		if json.Unmarshal(evtRaw, &evtList) == nil {
			cutoff := time.Now().Add(-15 * time.Minute)
			recent := 0
			samples := []string{}
			for _, ev := range evtList.Items {
				tsStr := ev.LastTimestamp
				if tsStr == "" {
					tsStr = ev.EventTime
				}
				if ts, err := time.Parse(time.RFC3339, tsStr); err == nil && ts.After(cutoff) {
					recent++
					if len(samples) < 8 {
						samples = append(samples, fmt.Sprintf("%s/%s:%s", ev.InvolvedObject.Kind, ev.InvolvedObject.Name, ev.Reason))
					}
				}
			}
			st := "pass"
			if recent >= 10 {
				st = "warning"
			}
			detail := fmt.Sprintf("近 15 分钟 Warning 事件 %d 条", recent)
			if len(samples) > 0 {
				detail += "（" + strings.Join(samples, ", ") + "）"
			}
			res.Checks = append(res.Checks, api.MonitorInspectCheck{
				ID: "k8s.events", Name: "集群 Warning 事件", Category: "k8s",
				Status: st, Value: fmt.Sprintf("%d 条/15m", recent),
				Threshold: "<10 条", Detail: detail,
			})
		}
	}

	res.Status = aggregateCheckStatus(res.Checks)
	if res.Status == "skipped" {
		res.Status = "pass"
	}
	return res
}

func parseK8sCPUQuantity(raw string) (float64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "<unknown>" {
		return 0, false
	}
	if n, ok := strings.CutSuffix(s, "m"); ok {
		v, err := strconv.ParseFloat(n, 64)
		return v / 1000.0, err == nil && v >= 0
	}
	if n, ok := strings.CutSuffix(s, "n"); ok {
		v, err := strconv.ParseFloat(n, 64)
		return v / 1e9, err == nil && v >= 0
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil && v >= 0
}

func parseK8sMemQuantity(raw string) (float64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "<unknown>" {
		return 0, false
	}
	units := []struct {
		suffix string
		factor float64
	}{
		{"Ti", 1099511627776}, {"Gi", 1073741824}, {"Mi", 1048576}, {"Ki", 1024},
		{"T", 1e12}, {"G", 1e9}, {"M", 1e6}, {"K", 1e3},
	}
	for _, u := range units {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			v, err := strconv.ParseFloat(n, 64)
			return v * u.factor, err == nil && v >= 0
		}
	}
	return parseContainerByteQuantity(s)
}

// ---------------- Strategy 4: Container Runtime Strategy ----------------

func executeContainerRuntimeStrategy(ctx context.Context, asset api.MonitorInspectAsset, th api.MonitorInspectThresholds) api.MonitorInspectAssetResult {
	res := api.MonitorInspectAssetResult{
		AssetID:         asset.AssetID,
		AssetName:       asset.AssetName,
		AssetKind:       "container",
		Strategy:        monitorStrategyContainerRuntime,
		CollectorMethod: "agent_local",
		Checks:          []api.MonitorInspectCheck{},
		Details:         map[string]any{},
		Evidence:        map[string]any{},
		SeriesPoints:    []api.MonitorInspectSeriesPoint{},
	}

	raw, err := collectAgentContainerMetricsJSON(ctx)
	if err != nil {
		res.Status = "skipped"
		res.Reason = "container_runtime_unavailable"
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "container.runtime", Name: "容器运行时可用性", Category: "runtime",
			Status: "skipped", Detail: "当前节点未检测到可用的 Docker / nerdctl / Podman 运行时",
		})
		return res
	}

	var snap agentContainerSnapshot
	if json.Unmarshal([]byte(raw), &snap) != nil {
		res.Status = "error"
		res.Reason = "invalid_container_snapshot"
		return res
	}
	at := snap.AtMs
	if at <= 0 {
		at = time.Now().UnixMilli()
	}
	res.CollectedAtMs = at
	res.Evidence["runtime"] = snap.Runtime
	res.Details["runtime"] = snap.Runtime
	res.Details["containers"] = snap.Containers

	var cpuSum, memSum, memLimitSum float64
	highMemContainers := []string{}
	for _, c := range snap.Containers {
		if c.CPUCores != nil {
			cpuSum += *c.CPUCores
		}
		if c.MemoryBytes != nil {
			memSum += *c.MemoryBytes
		}
		if c.MemoryLimitBytes != nil && *c.MemoryLimitBytes > 0 {
			memLimitSum += *c.MemoryLimitBytes
			if c.MemoryBytes != nil {
				pct := (*c.MemoryBytes / *c.MemoryLimitBytes) * 100.0
				if pct >= th.MemWarnPercent {
					highMemContainers = append(highMemContainers, fmt.Sprintf("%s(%.1f%%)", c.Name, pct))
				}
			}
		}
		if monitorID(c.Name) {
			for _, m := range []struct {
				metric string
				val    *float64
			}{
				{"cpu", c.CPUCores},
				{"memory", c.MemoryBytes},
				{"memory_limit", c.MemoryLimitBytes},
			} {
				pt := api.MonitorInspectSeriesPoint{
					Type: "container", AssetID: c.Name, Origin: "container_stats",
					Metric: m.metric, Unit: monitorUnits[m.metric], AtMs: at, Status: "missing",
				}
				if m.val != nil && monitorValidValue(m.metric, *m.val) {
					v := *m.val
					pt.Value = &v
					pt.Status = "ok"
				}
				res.SeriesPoints = append(res.SeriesPoints, pt)
			}
		}
	}

	res.Metrics.CPUCores = monitorFloatPtr(cpuSum)
	res.Metrics.MemoryUsedBytes = monitorFloatPtr(memSum)
	if memLimitSum > 0 {
		res.Metrics.MemoryTotalBytes = monitorFloatPtr(memLimitSum)
		res.Metrics.MemoryPercent = monitorFloatPtr((memSum / memLimitSum) * 100.0)
	}
	res.Metrics.OSInfo = snap.Runtime

	res.Checks = append(res.Checks, api.MonitorInspectCheck{
		ID: "container.runtime", Name: "容器运行时与数量", Category: "runtime",
		Status: "pass", Value: fmt.Sprintf("%s · %d 运行中", snap.Runtime, len(snap.Containers)),
		Detail: fmt.Sprintf("累计 CPU %.2f cores，内存 %.1f MiB", cpuSum, memSum/1048576),
	})

	memSt := "pass"
	memDetail := "所有运行容器内存占比均在阈值内"
	if len(highMemContainers) > 0 {
		memSt = "warning"
		memDetail = "内存占限额偏高容器：" + strings.Join(highMemContainers, ", ")
	}
	res.Checks = append(res.Checks, api.MonitorInspectCheck{
		ID: "container.memory_limit", Name: "容器内存限额水位", Category: "resource",
		Status: memSt, Value: fmt.Sprintf("超限 %d 个", len(highMemContainers)),
		Threshold: fmt.Sprintf("<%.0f%% 内存限额", th.MemWarnPercent), Detail: memDetail,
	})

	res.Status = aggregateCheckStatus(res.Checks)
	return res
}

// ---------------- Strategy 5: App / Web / API Strategy ----------------

func executeAppServiceStrategy(ctx context.Context, asset api.MonitorInspectAsset, th api.MonitorInspectThresholds) api.MonitorInspectAssetResult {
	res := api.MonitorInspectAssetResult{
		AssetID:         asset.AssetID,
		AssetName:       asset.AssetName,
		AssetKind:       "app",
		Strategy:        monitorStrategyAppService,
		CollectorMethod: "http_probe",
		Checks:          []api.MonitorInspectCheck{},
		Details:         map[string]any{},
		Evidence:        map[string]any{},
		SeriesPoints:    []api.MonitorInspectSeriesPoint{},
	}
	at := time.Now().UnixMilli()
	res.CollectedAtMs = at

	// Local nginx stub_status probe when asset has no explicit URL.
	if strings.TrimSpace(asset.URL) == "" {
		loopClient := &http.Client{Timeout: 1500 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		act0, req0, ok0 := readLocalNginxStubStatus(ctx, loopClient)
		if !ok0 {
			res.Status = "skipped"
			res.Reason = "no_app_endpoints"
			res.Checks = append(res.Checks, api.MonitorInspectCheck{
				ID: "app.nginx_status", Name: "本机 Nginx / 应用探测", Category: "availability",
				Status: "skipped", Detail: "未登记网站端点且本机无 nginx stub_status",
			})
			return res
		}
		t0 := time.Now()
		select {
		case <-ctx.Done():
		case <-time.After(250 * time.Millisecond):
		}
		dt := time.Since(t0).Seconds()
		act1, req1, ok1 := readLocalNginxStubStatus(ctx, loopClient)
		active := act0
		var rps *float64
		if ok1 {
			active = act1
			if dt > 0 && req1 >= req0 {
				v := float64(req1-req0) / dt
				rps = &v
			}
		}
		res.Metrics.ActiveConnections = monitorFloatPtr(active)
		if rps != nil {
			res.Metrics.RequestsPerSec = monitorFloatPtr(*rps)
		}
		aCopy := active
		res.SeriesPoints = append(res.SeriesPoints, api.MonitorInspectSeriesPoint{
			Type: "app", AssetID: "nginx", Origin: "app_probe", Metric: "active_connections", Unit: "connections", AtMs: at, Status: "ok", Value: &aCopy,
		})
		if rps != nil {
			rCopy := *rps
			res.SeriesPoints = append(res.SeriesPoints, api.MonitorInspectSeriesPoint{
				Type: "app", AssetID: "nginx", Origin: "app_probe", Metric: "requests", Unit: "req/s", AtMs: at, Status: "ok", Value: &rCopy,
			})
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "app.nginx_status", Name: "Nginx 并发与请求速率", Category: "availability",
			Status: "pass", Value: fmt.Sprintf("并发 %.0f", active),
			Detail: fmt.Sprintf("本机 nginx stub_status 正常，当前活跃连接 %.0f", active),
		})
		res.Status = "pass"
		return res
	}

	parsed, err := url.Parse(strings.TrimSpace(asset.URL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		res.Status = "error"
		res.Reason = "invalid_probe_url"
		return res
	}
	dialer := &net.Dialer{Timeout: 4 * time.Second}
	client := &http.Client{
		Timeout:       6 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			DialContext: func(dCtx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(dCtx, host)
				if err != nil || len(ips) == 0 {
					return nil, fmt.Errorf("lookup failed")
				}
				for _, ipAddr := range ips {
					if isDisallowedProbeIP(ipAddr.IP) {
						return nil, fmt.Errorf("disallowed probe address")
					}
				}
				return dialer.DialContext(dCtx, network, net.JoinHostPort(ips[0].IP.String(), port))
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		res.Status = "error"
		res.Reason = "invalid_probe_request"
		return res
	}
	t0 := time.Now()
	resp, doErr := client.Do(req)
	elapsedMs := float64(time.Since(t0).Microseconds()) / 1000.0
	if doErr != nil {
		errPct := 100.0
		res.Metrics.ErrorRate = monitorFloatPtr(errPct)
		if monitorID(asset.AssetID) {
			res.SeriesPoints = append(res.SeriesPoints, api.MonitorInspectSeriesPoint{
				Type: "app", AssetID: asset.AssetID, Origin: "app_probe", Metric: "error_rate", Unit: "%", AtMs: at, Status: "ok", Value: &errPct,
			})
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "app.availability", Name: "服务可用性 (A1)", Category: "availability",
			Status: "critical", Value: "不可达", Threshold: "HTTP 2xx/3xx",
			Detail: sanitizeSecretText(doErr.Error()),
		})
		res.Status = "critical"
		res.Reason = "http_probe_failed"
		return res
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()

	errPct := 0.0
	if resp.StatusCode >= 400 {
		errPct = 100.0
	}
	res.Metrics.ResponseTimeMs = monitorFloatPtr(elapsedMs)
	res.Metrics.ErrorRate = monitorFloatPtr(errPct)
	if monitorID(asset.AssetID) {
		eCopy, rCopy := errPct, elapsedMs
		res.SeriesPoints = append(res.SeriesPoints,
			api.MonitorInspectSeriesPoint{Type: "app", AssetID: asset.AssetID, Origin: "app_probe", Metric: "response_time_ms", Unit: "ms", AtMs: at, Status: "ok", Value: &rCopy},
			api.MonitorInspectSeriesPoint{Type: "app", AssetID: asset.AssetID, Origin: "app_probe", Metric: "error_rate", Unit: "%", AtMs: at, Status: "ok", Value: &eCopy},
		)
	}

	availSt := "pass"
	if resp.StatusCode >= 500 {
		availSt = "critical"
	} else if resp.StatusCode >= 400 {
		availSt = "warning"
	}
	res.Checks = append(res.Checks, api.MonitorInspectCheck{
		ID: "app.availability", Name: "服务可用性 (A1)", Category: "availability",
		Status: availSt, Value: fmt.Sprintf("HTTP %d", resp.StatusCode),
		Threshold: "HTTP < 400", Detail: fmt.Sprintf("探测 %s 返回 HTTP %d，耗时 %.1f ms", parsed.Host, resp.StatusCode, elapsedMs),
	})

	latSt := "pass"
	if elapsedMs >= th.LatencyWarnMs*2 {
		latSt = "critical"
	} else if elapsedMs >= th.LatencyWarnMs {
		latSt = "warning"
	}
	res.Checks = append(res.Checks, api.MonitorInspectCheck{
		ID: "app.latency", Name: "应用响应时间", Category: "resource",
		Status: latSt, Value: fmt.Sprintf("%.1f ms", elapsedMs),
		Threshold: fmt.Sprintf("<%.0f ms", th.LatencyWarnMs),
		Detail:    fmt.Sprintf("HTTP GET 响应耗时 %.1f ms", elapsedMs),
	})

	// Web security headers & TLS cert check (A4)
	if parsed.Scheme == "https" {
		issues := []string{}
		certDays := -1
		if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
			cert := resp.TLS.PeerCertificates[0]
			certDays = int(time.Until(cert.NotAfter).Hours() / 24)
			res.Details["tls_expires_at"] = cert.NotAfter.UTC().Format(time.RFC3339)
			res.Details["tls_days_remaining"] = certDays
			if certDays <= 0 {
				issues = append(issues, "证书已过期")
			} else if certDays <= th.CertWarnDays {
				issues = append(issues, fmt.Sprintf("证书剩余 %d 天", certDays))
			}
		}
		if !strings.Contains(strings.ToLower(resp.Header.Get("Strict-Transport-Security")), "max-age=") {
			issues = append(issues, "缺少 HSTS")
		}
		if strings.ToLower(strings.TrimSpace(resp.Header.Get("X-Content-Type-Options"))) != "nosniff" {
			issues = append(issues, "缺少 nosniff")
		}
		secSt := "pass"
		if certDays >= 0 && certDays <= 0 {
			secSt = "critical"
		} else if len(issues) > 0 {
			secSt = "warning"
		}
		val := "HTTPS 合规"
		if certDays >= 0 {
			val = fmt.Sprintf("证书剩余 %d 天", certDays)
		}
		detail := "TLS 证书有效且包含 HSTS 与 nosniff 安全响应头"
		if len(issues) > 0 {
			detail = strings.Join(issues, "；")
		}
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "app.web_security", Name: "Web 安全与证书 (A4)", Category: "security",
			Status: secSt, Value: val,
			Threshold: fmt.Sprintf("证书 >%d 天 + HSTS + nosniff", th.CertWarnDays), Detail: detail,
		})
	} else {
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "app.web_security", Name: "Web 安全与证书 (A4)", Category: "security",
			Status: "warning", Value: "明文 HTTP", Threshold: "HTTPS",
			Detail: "端点使用 HTTP 明文协议，未启用 HTTPS",
		})
	}

	res.Details["url"] = parsed.Scheme + "://" + parsed.Host + parsed.Path
	res.Details["status_code"] = resp.StatusCode
	res.Status = aggregateCheckStatus(res.Checks)
	return res
}

// ---------------- Strategy 6: Database Strategy ----------------

func executeDatabaseServiceStrategy(ctx context.Context, asset api.MonitorInspectAsset, th api.MonitorInspectThresholds) api.MonitorInspectAssetResult {
	res := api.MonitorInspectAssetResult{
		AssetID:         asset.AssetID,
		AssetName:       asset.AssetName,
		AssetKind:       "database",
		Strategy:        monitorStrategyDatabaseService,
		CollectorMethod: "tcp_probe",
		Checks:          []api.MonitorInspectCheck{},
		Details:         map[string]any{},
		Evidence:        map[string]any{},
		SeriesPoints:    []api.MonitorInspectSeriesPoint{},
	}
	at := time.Now().UnixMilli()
	res.CollectedAtMs = at

	host := strings.TrimSpace(asset.Address)
	port := asset.Port
	if host == "" || port <= 0 || port > 65535 || !catalogHost(host) {
		res.Status = "skipped"
		res.Reason = "database_endpoint_missing"
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "db.connectivity", Name: "数据库连通性 (A2)", Category: "database",
			Status: "skipped", Detail: "未登记数据库主机或端口",
		})
		return res
	}

	dCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(dCtx, host)
	if err != nil || len(ips) == 0 {
		res.Status = "critical"
		res.Reason = "db_dns_failed"
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "db.connectivity", Name: "数据库连通性 (A2)", Category: "database",
			Status: "critical", Value: "不可达", Detail: fmt.Sprintf("无法解析数据库地址 %s", host),
		})
		return res
	}
	for _, ipAddr := range ips {
		if isDisallowedProbeIP(ipAddr.IP) {
			res.Status = "error"
			res.Reason = "disallowed_db_address"
			return res
		}
	}

	t0 := time.Now()
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.DialContext(dCtx, "tcp", net.JoinHostPort(ips[0].IP.String(), strconv.Itoa(port)))
	elapsedMs := float64(time.Since(t0).Microseconds()) / 1000.0
	if err != nil {
		errPct := 100.0
		res.Metrics.ErrorRate = monitorFloatPtr(errPct)
		res.Status = "critical"
		res.Reason = "db_unreachable"
		res.Checks = append(res.Checks, api.MonitorInspectCheck{
			ID: "db.connectivity", Name: "数据库连通性 (A2)", Category: "database",
			Status: "critical", Value: "连接失败", Threshold: "TCP 可达",
			Detail: fmt.Sprintf("%s (%s:%d) 连接失败: %s", asset.Engine, host, port, sanitizeSecretText(err.Error())),
		})
		return res
	}
	_ = conn.Close()

	errPct := 0.0
	res.Metrics.ResponseTimeMs = monitorFloatPtr(elapsedMs)
	res.Metrics.ErrorRate = monitorFloatPtr(errPct)
	res.Metrics.OSInfo = asset.Engine
	res.Details["engine"] = asset.Engine
	res.Details["host"] = host
	res.Details["port"] = port
	res.Details["database"] = asset.Database

	if monitorID(asset.AssetID) {
		rCopy, eCopy := elapsedMs, errPct
		res.SeriesPoints = append(res.SeriesPoints,
			api.MonitorInspectSeriesPoint{Type: "database", AssetID: asset.AssetID, Origin: "app_probe", Metric: "response_time_ms", Unit: "ms", AtMs: at, Status: "ok", Value: &rCopy},
			api.MonitorInspectSeriesPoint{Type: "database", AssetID: asset.AssetID, Origin: "app_probe", Metric: "error_rate", Unit: "%", AtMs: at, Status: "ok", Value: &eCopy},
		)
	}

	res.Checks = append(res.Checks, api.MonitorInspectCheck{
		ID: "db.connectivity", Name: "数据库实例连通性 (A2)", Category: "database",
		Status: "pass", Value: "在线", Threshold: "TCP 可达",
		Detail: fmt.Sprintf("%s %s:%d/%s 连通正常，建连耗时 %.1f ms", asset.Engine, host, port, asset.Database, elapsedMs),
	})
	latSt := "pass"
	if elapsedMs >= th.LatencyWarnMs {
		latSt = "warning"
	}
	res.Checks = append(res.Checks, api.MonitorInspectCheck{
		ID: "db.latency", Name: "数据库建连延迟", Category: "database",
		Status: latSt, Value: fmt.Sprintf("%.1f ms", elapsedMs),
		Threshold: fmt.Sprintf("<%.0f ms", th.LatencyWarnMs),
		Detail:    fmt.Sprintf("TCP 握手耗时 %.1f ms", elapsedMs),
	})

	res.Status = aggregateCheckStatus(res.Checks)
	return res
}

// Keep unused syscall import guarded across platforms if needed.
var _ = syscall.Statfs
