package server

import (
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/user/doops/agent/api"
)

const (
	monitorInspectTool = "doops_monitor_inspect"

	monitorStrategyVMHost           = "vm_host"
	monitorStrategySSHHost          = "ssh_host"
	monitorStrategyK8sCluster       = "k8s_cluster"
	monitorStrategyContainerRuntime = "container_runtime"
	monitorStrategyAppService       = "app_service"
	monitorStrategyDatabaseService  = "database_service"
)

type monitorStrategyMeta struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	AssetKinds      []string `json:"asset_kinds"`
	CollectorMethod string   `json:"collector_method"`
	Checks          []string `json:"checks"`
}

var monitorStrategyCatalog = []monitorStrategyMeta{
	{
		ID:              monitorStrategyVMHost,
		Name:            "虚机 / 物理机本机策略",
		Description:     "通过 Agent 本机直采 CPU、内存、内核、磁盘容量与 I/O、网卡流量、GPU 及 M1–M5 系统合规与安全巡检。",
		AssetKinds:      []string{"vm", "physical"},
		CollectorMethod: "agent_local",
		Checks:          []string{"运行与重启(M1)", "CPU与内存水位(M2)", "磁盘容量", "磁盘I/O延迟", "可疑与僵尸进程(M3)", "内核配置合规(M4)", "登录与权限基线(M5)"},
	},
	{
		ID:              monitorStrategySSHHost,
		Name:            "SSH 远程主机策略",
		Description:     "由在线 Agent 经结构化只读 SSH（BatchMode）采集无 Agent 虚机/物理机的资源指标与系统巡检项。",
		AssetKinds:      []string{"vm", "physical"},
		CollectorMethod: "ssh",
		Checks:          []string{"SSH连通性", "运行与重启(M1)", "CPU与内存水位(M2)", "磁盘容量", "磁盘I/O", "僵尸进程与负载", "内核安全基线(M4)"},
	},
	{
		ID:              monitorStrategyK8sCluster,
		Name:            "Kubernetes 集群策略",
		Description:     "通过 Agent 内置 kubectl 采集 Node/Pod 资源、节点健康与压力状态、Pod 异常重启、控制器副本、PVC 及告警事件。",
		AssetKinds:      []string{"k8s"},
		CollectorMethod: "k8s_api",
		Checks:          []string{"节点健康与压力", "Pod运行态与重启", "控制器副本就绪", "PVC存储绑定", "集群Warning事件", "Node/Pod资源指标"},
	},
	{
		ID:              monitorStrategyContainerRuntime,
		Name:            "容器运行时策略",
		Description:     "自动探测本机 Docker / nerdctl / Podman 运行态容器 CPU、内存与内存限额占比，识别退出或异常容器。",
		AssetKinds:      []string{"container"},
		CollectorMethod: "agent_local",
		Checks:          []string{"运行时可用性", "容器退出/重启状态", "容器内存限额水位"},
	},
	{
		ID:              monitorStrategyAppService,
		Name:            "应用 / Web / API 策略",
		Description:     "采集本机 Nginx 并发与请求速率，并对登记网站端点执行可用性、响应时间、错误率及 HTTPS/TLS 证书与安全响应头巡检。",
		AssetKinds:      []string{"app"},
		CollectorMethod: "http_probe",
		Checks:          []string{"服务可用性(A1)", "响应时间与错误率", "HTTPS证书有效期与安全响应头(A4)", "Nginx并发与请求速率"},
	},
	{
		ID:              monitorStrategyDatabaseService,
		Name:            "数据库策略",
		Description:     "针对已登记数据库实例探测连通性与延迟，识别实例存活状态与只读巡检覆盖情况。",
		AssetKinds:      []string{"database"},
		CollectorMethod: "tcp_probe",
		Checks:          []string{"数据库实例连通性(A2)", "连接响应延迟"},
	},
}

var validMonitorStrategies = func() map[string]bool {
	m := make(map[string]bool, len(monitorStrategyCatalog))
	for _, s := range monitorStrategyCatalog {
		m[s.ID] = true
	}
	return m
}()

var defaultMonitorStrategies = []string{
	monitorStrategyVMHost,
	monitorStrategySSHHost,
	monitorStrategyK8sCluster,
	monitorStrategyContainerRuntime,
	monitorStrategyAppService,
	monitorStrategyDatabaseService,
}

func defaultMonitorThresholds() api.MonitorInspectThresholds {
	return api.MonitorInspectThresholds{
		CPUWarnPercent:    80,
		CPUCritPercent:    90,
		MemWarnPercent:    80,
		MemCritPercent:    90,
		DiskWarnPercent:   85,
		DiskCritPercent:   95,
		DiskLatencyWarnMs: 50,
		LatencyWarnMs:     1000,
		ErrorRateWarnPct:  5,
		CertWarnDays:      14,
		MinUptimeSeconds:  300,
	}
}

func normalizeMonitorThresholds(in api.MonitorInspectThresholds) api.MonitorInspectThresholds {
	d := defaultMonitorThresholds()
	if in.CPUWarnPercent > 0 && in.CPUWarnPercent <= 100 {
		d.CPUWarnPercent = in.CPUWarnPercent
	}
	if in.CPUCritPercent > 0 && in.CPUCritPercent <= 100 {
		d.CPUCritPercent = in.CPUCritPercent
	}
	if d.CPUCritPercent < d.CPUWarnPercent {
		d.CPUCritPercent = d.CPUWarnPercent
	}
	if in.MemWarnPercent > 0 && in.MemWarnPercent <= 100 {
		d.MemWarnPercent = in.MemWarnPercent
	}
	if in.MemCritPercent > 0 && in.MemCritPercent <= 100 {
		d.MemCritPercent = in.MemCritPercent
	}
	if d.MemCritPercent < d.MemWarnPercent {
		d.MemCritPercent = d.MemWarnPercent
	}
	if in.DiskWarnPercent > 0 && in.DiskWarnPercent <= 100 {
		d.DiskWarnPercent = in.DiskWarnPercent
	}
	if in.DiskCritPercent > 0 && in.DiskCritPercent <= 100 {
		d.DiskCritPercent = in.DiskCritPercent
	}
	if d.DiskCritPercent < d.DiskWarnPercent {
		d.DiskCritPercent = d.DiskWarnPercent
	}
	if in.DiskLatencyWarnMs > 0 && in.DiskLatencyWarnMs <= 60000 {
		d.DiskLatencyWarnMs = in.DiskLatencyWarnMs
	}
	if in.LatencyWarnMs > 0 && in.LatencyWarnMs <= 60000 {
		d.LatencyWarnMs = in.LatencyWarnMs
	}
	if in.ErrorRateWarnPct > 0 && in.ErrorRateWarnPct <= 100 {
		d.ErrorRateWarnPct = in.ErrorRateWarnPct
	}
	if in.CertWarnDays > 0 && in.CertWarnDays <= 365 {
		d.CertWarnDays = in.CertWarnDays
	}
	if in.MinUptimeSeconds > 0 && in.MinUptimeSeconds <= 86400*365 {
		d.MinUptimeSeconds = in.MinUptimeSeconds
	}
	if len(in.DeniedProcesses) > 0 {
		clean := make([]string, 0, len(in.DeniedProcesses))
		for _, p := range in.DeniedProcesses {
			p = strings.TrimSpace(p)
			if p != "" && len(p) <= 64 && inventoryMetadata(p) && len(clean) < 32 {
				clean = append(clean, p)
			}
		}
		d.DeniedProcesses = clean
	}
	return d
}

func normalizeMonitorStrategies(list []string) []string {
	if len(list) == 0 {
		return append([]string(nil), defaultMonitorStrategies...)
	}
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, s := range list {
		s = strings.TrimSpace(s)
		if validMonitorStrategies[s] && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return append([]string(nil), defaultMonitorStrategies...)
	}
	return out
}

func normalizeSkillSlugs(list []string) []string {
	if len(list) == 0 {
		return []string{}
	}
	seen := make(map[string]bool, len(list))
	out := make([]string, 0, len(list))
	for _, raw := range list {
		slug := strings.ToLower(strings.TrimSpace(raw))
		if skillSlugPattern.MatchString(slug) && !seen[slug] && len(out) < 32 {
			seen[slug] = true
			out = append(out, slug)
		}
	}
	return out
}

type monitorTargetRef struct {
	Cluster  string `json:"cluster"`
	Instance string `json:"instance"`
}

type monitorSeriesBridgeEntry struct {
	Cluster  string
	Instance string
	Point    api.MonitorInspectSeriesPoint
}

type monitorScheduleRecord struct {
	ID              string                       `json:"id"`
	Name            string                       `json:"name"`
	Description     string                       `json:"description"`
	Enabled         bool                         `json:"enabled"`
	ScopeMode       string                       `json:"scope_mode"` // all_online | targets | tags
	Targets         []monitorTargetRef           `json:"targets"`
	Tags            []string                     `json:"tags"`
	Strategies      []string                     `json:"strategies"`
	Skills          []string                     `json:"skills,omitempty"`
	Thresholds      api.MonitorInspectThresholds `json:"thresholds"`
	IntervalSeconds int                          `json:"interval_seconds"`
	NextRunAtMs     int64                        `json:"next_run_at_ms"`
	LastRunAtMs     int64                        `json:"last_run_at_ms"`
	LastRunID       string                       `json:"last_run_id,omitempty"`
	LastStatus      string                       `json:"last_status,omitempty"`
	CreatedBy       string                       `json:"created_by,omitempty"`
	CreatedAt       string                       `json:"created_at"`
	UpdatedAt       string                       `json:"updated_at"`
}

type monitorRunRecord struct {
	ID             string `json:"id"`
	ScheduleID     string `json:"schedule_id,omitempty"`
	ScheduleName   string `json:"schedule_name,omitempty"`
	TriggerKind    string `json:"trigger_kind"` // scheduled | manual | retest
	TriggeredBy    string `json:"triggered_by,omitempty"`
	Status         string `json:"status"` // running | success | warning | critical | partial | failed
	TotalTargets   int    `json:"total_targets"`
	TotalAssets    int    `json:"total_assets"`
	PassedChecks   int    `json:"passed_checks"`
	WarningChecks  int    `json:"warning_checks"`
	FailedChecks   int    `json:"failed_checks"`
	SkippedChecks  int    `json:"skipped_checks"`
	OfflineTargets int    `json:"offline_targets"`
	MetricsWritten int    `json:"metrics_written"`
	StartedAtMs    int64  `json:"started_at_ms"`
	FinishedAtMs   int64  `json:"finished_at_ms"`
	DurationMs     int64  `json:"duration_ms"`
	Summary        string `json:"summary,omitempty"`
}

type monitorRunItemRecord struct {
	ID              int64                     `json:"id"`
	RunID           string                    `json:"run_id"`
	Cluster         string                    `json:"cluster"`
	Instance        string                    `json:"instance"`
	AssetID         string                    `json:"asset_id"`
	AssetName       string                    `json:"asset_name"`
	AssetKind       string                    `json:"asset_kind"`
	Strategy        string                    `json:"strategy"`
	CollectorMethod string                    `json:"collector_method"`
	Status          string                    `json:"status"`
	Reason          string                    `json:"reason,omitempty"`
	StartedAtMs     int64                     `json:"started_at_ms"`
	FinishedAtMs    int64                     `json:"finished_at_ms"`
	DurationMs      int64                     `json:"duration_ms"`
	Metrics         api.MonitorInspectMetrics `json:"metrics"`
	Checks          []api.MonitorInspectCheck `json:"checks"`
	Evidence        map[string]any            `json:"evidence,omitempty"`
}

type monitorResourceSnapshot struct {
	ID                int64          `json:"id,omitempty"`
	Cluster           string         `json:"cluster"`
	Instance          string         `json:"instance"`
	AssetID           string         `json:"asset_id"`
	AssetName         string         `json:"asset_name"`
	AssetKind         string         `json:"asset_kind"`
	Strategy          string         `json:"strategy"`
	CollectorMethod   string         `json:"collector_method"`
	CollectedAtMs     int64          `json:"collected_at_ms"`
	RunID             string         `json:"run_id,omitempty"`
	Status            string         `json:"status"`
	Online            bool           `json:"online"`
	Stale             bool           `json:"stale"`
	CPUCores          *float64       `json:"cpu_cores,omitempty"`
	CPUTotalCores     *float64       `json:"cpu_total_cores,omitempty"`
	CPUPercent        *float64       `json:"cpu_percent,omitempty"`
	MemoryUsedBytes   *float64       `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes  *float64       `json:"memory_total_bytes,omitempty"`
	MemoryPercent     *float64       `json:"memory_percent,omitempty"`
	DiskUsedBytes     *float64       `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes    *float64       `json:"disk_total_bytes,omitempty"`
	DiskPercent       *float64       `json:"disk_percent,omitempty"`
	NetBytesPerSec    *float64       `json:"net_bytes_per_sec,omitempty"`
	DiskIOPS          *float64       `json:"disk_iops,omitempty"`
	DiskLatencyMs     *float64       `json:"disk_latency_ms,omitempty"`
	GPUPercent        *float64       `json:"gpu_percent,omitempty"`
	ResponseTimeMs    *float64       `json:"response_time_ms,omitempty"`
	ErrorRate         *float64       `json:"error_rate,omitempty"`
	ActiveConnections *float64       `json:"active_connections,omitempty"`
	RequestsPerSec    *float64       `json:"requests_per_sec,omitempty"`
	UptimeSeconds     *float64       `json:"uptime_seconds,omitempty"`
	Load1m            *float64       `json:"load_1m,omitempty"`
	OSInfo            string         `json:"os_info,omitempty"`
	KernelInfo        string         `json:"kernel_info,omitempty"`
	Tags              []string       `json:"tags"`
	Details           map[string]any `json:"details,omitempty"`
}

type monitorOverviewSummary struct {
	TotalAssets       int     `json:"total_assets"`
	OnlineAgents      int     `json:"online_agents"`
	OfflineAgents     int     `json:"offline_agents"`
	CoveredAssets     int     `json:"covered_assets"`
	ProbeCoveragePct  float64 `json:"probe_coverage_pct"`
	HealthyAssets     int     `json:"healthy_assets"`
	WarningAssets     int     `json:"warning_assets"`
	CriticalAssets    int     `json:"critical_assets"`
	OfflineAssets     int     `json:"offline_assets"`
	AvgCPUPercent     float64 `json:"avg_cpu_percent"`
	AvgMemoryPercent  float64 `json:"avg_memory_percent"`
	AvgDiskPercent    float64 `json:"avg_disk_percent"`
	TotalCPUCores     float64 `json:"total_cpu_cores"`
	UsedCPUCores      float64 `json:"used_cpu_cores"`
	TotalMemoryBytes  float64 `json:"total_memory_bytes"`
	UsedMemoryBytes   float64 `json:"used_memory_bytes"`
	TotalDiskBytes    float64 `json:"total_disk_bytes"`
	UsedDiskBytes     float64 `json:"used_disk_bytes"`
	ActiveSchedules     int     `json:"active_schedules"`
	TotalRuns           int     `json:"total_runs"`
	OpenThresholdAlerts int     `json:"open_threshold_alerts"`
	LastCollectedAtMs   int64   `json:"last_collected_at_ms"`
}

type monitorOpenThresholdAlert struct {
	Cluster         string  `json:"cluster"`
	Instance        string  `json:"instance"`
	AssetType       string  `json:"type"`
	AssetID         string  `json:"asset_id"`
	Namespace       string  `json:"namespace,omitempty"`
	Origin          string  `json:"origin"`
	Metric          string  `json:"metric"`
	DeviceID        string  `json:"device_id,omitempty"`
	Threshold       float64 `json:"threshold"`
	DurationSeconds int     `json:"duration_seconds"`
	OpenedMs        int64   `json:"opened_ms"`
}

type monitorTrendBucket struct {
	StartMs          int64    `json:"start_ms"`
	EndMs            int64    `json:"end_ms"`
	Samples          int      `json:"samples"`
	AvgCPUPercent    *float64 `json:"avg_cpu_percent,omitempty"`
	AvgMemoryPercent *float64 `json:"avg_memory_percent,omitempty"`
	AvgDiskPercent   *float64 `json:"avg_disk_percent,omitempty"`
	MaxCPUPercent    *float64 `json:"max_cpu_percent,omitempty"`
	MaxMemoryPercent *float64 `json:"max_memory_percent,omitempty"`
}

type monitorOverviewResponse struct {
	Scope      string                    `json:"scope"`
	Range      string                    `json:"range"`
	FromMs     int64                     `json:"from_ms"`
	ToMs       int64                     `json:"to_ms"`
	Summary    monitorOverviewSummary    `json:"summary"`
	ByKind     map[string]int            `json:"by_kind"`
	Resources  []monitorResourceSnapshot `json:"resources"`
	TopCPU     []monitorResourceSnapshot `json:"top_cpu"`
	TopMemory  []monitorResourceSnapshot `json:"top_memory"`
	TopDisk    []monitorResourceSnapshot `json:"top_disk"`
	Trends          []monitorTrendBucket        `json:"trends"`
	ThresholdAlerts []monitorOpenThresholdAlert `json:"threshold_alerts"`
	RecentRuns      []monitorRunRecord          `json:"recent_runs"`
	Strategies      []monitorStrategyMeta       `json:"strategies"`
}

type monitorHistoryResponse struct {
	Cluster   string                    `json:"cluster"`
	Instance  string                    `json:"instance"`
	AssetKind string                    `json:"asset_kind"`
	AssetID   string                    `json:"asset_id"`
	Range     string                    `json:"range"`
	FromMs    int64                     `json:"from_ms"`
	ToMs      int64                     `json:"to_ms"`
	Latest    *monitorResourceSnapshot  `json:"latest,omitempty"`
	Points    []monitorResourceSnapshot `json:"points"`
}

func monitorFloatPtr(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	c := math.Round(v*1000) / 1000
	return &c
}

func sanitizeMonitorEvidence(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	// Redact any accidental secret material before persisting.
	clean := sanitizeSecretText(string(raw))
	if len(clean) > 32768 {
		clean = clean[:32768]
		return map[string]any{"truncated": true, "preview": clean}
	}
	var out map[string]any
	if json.Unmarshal([]byte(clean), &out) != nil {
		return map[string]any{"redacted_text": clean}
	}
	return out
}

func monitorCheckStatusRank(status string) int {
	switch status {
	case "critical":
		return 4
	case "error":
		return 3
	case "offline":
		return 3
	case "warning":
		return 2
	case "pass":
		return 1
	default:
		return 0
	}
}

func aggregateCheckStatus(checks []api.MonitorInspectCheck) string {
	best := "skipped"
	bestRank := 0
	for _, c := range checks {
		r := monitorCheckStatusRank(c.Status)
		if r > bestRank {
			bestRank = r
			best = c.Status
		}
	}
	return best
}

func monitorEngineRangeDuration(raw string) (string, time.Duration, int, bool) {
	switch strings.TrimSpace(raw) {
	case "", "1h":
		return "1h", time.Hour, 12, true
	case "6h":
		return "6h", 6 * time.Hour, 18, true
	case "24h":
		return "24h", 24 * time.Hour, 24, true
	case "7d":
		return "7d", 7 * 24 * time.Hour, 28, true
	default:
		return "", 0, 0, false
	}
}
