package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
)

// --- JSON-RPC / MCP Protocol Types ---

type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      interface{}     `json:"id,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// ShellParams 用于 doops_shell 命令执行。
type ShellParams struct {
	SessionID string `json:"session_id"`
	Command   string `json:"command"`
}

// SSHParams 用于通过 argv 安全代理出站 SSH。
// Options 只能是连接旗标，不能携带位置参数或改写目标的配置。
// Command 中的每个元素都是远端命令的一个 argv，不能合并成一个待二次解析的字符串。
type SSHParams struct {
	SessionID string   `json:"session_id"`
	Options   []string `json:"options,omitempty"`
	Host      string   `json:"host"`
	Command   []string `json:"command,omitempty"`
}

// ToolCallParams 用于 MCP tools/call 标准入参。
// TimeoutMS 只在 Agent 声明 toolDeadline 能力时由 Gateway 填写：它表示 Gateway
// 愿意等待本次调用的上限，Agent 必须早于该时刻给出终态回复。
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	TimeoutMS int64           `json:"timeout_ms,omitempty"`
}

// FileWriteParams 用于 doops_file_write 远程文件写入。
type FileWriteParams struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
	Content   string `json:"content"` // Base64 or raw string
}

// FileReadParams 用于 doops_file_read 查看远程小文本文件。
type FileReadParams struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
}

type WorkspaceReadParams struct {
	SessionID string `json:"session_id"`
	Operation string `json:"operation"`
	Path      string `json:"path,omitempty"`
	Query     string `json:"query,omitempty"`
	Ref       string `json:"ref,omitempty"`
}

// KubectlParams 用于 doops_kubectl K8s 管理。
type KubectlParams struct {
	SessionID string `json:"session_id"`
	Command   string `json:"command"` // kubectl subcommand, e.g. "get pods -A"
}

// K8SParams is the structured request accepted by doops_k8s.
type K8SParams struct {
	SessionID string `json:"session_id"`
	Operation string `json:"operation"`
	Kind      string `json:"kind,omitempty"`
	Name      string `json:"name,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Container string `json:"container,omitempty"`
	Output    string `json:"output,omitempty"`
	Timeout   string `json:"timeout,omitempty"`
	Tail      int    `json:"tail,omitempty"`
	Replicas  *int   `json:"replicas,omitempty"`
	// Read-side filters.
	Selector      string `json:"selector,omitempty"`
	AllNamespaces bool   `json:"all_namespaces,omitempty"`
	Previous      bool   `json:"previous,omitempty"`
	Since         string `json:"since,omitempty"`
}

// HelmParams is the structured request accepted by doops_helm.
type HelmParams struct {
	SessionID      string                     `json:"session_id"`
	Operation      string                     `json:"operation"`
	Release        string                     `json:"release"`
	AssetSource    string                     `json:"asset_source,omitempty"`
	Chart          string                     `json:"chart,omitempty"`
	Namespace      string                     `json:"namespace"`
	ValueFiles     []string                   `json:"value_files,omitempty"`
	SetStrings     map[string]HelmScalarValue `json:"set_strings,omitempty"`
	Revision       int                        `json:"revision,omitempty"`
	Timeout        string                     `json:"timeout,omitempty"`
	Atomic         bool                       `json:"atomic,omitempty"`
	TakeOwnership  bool                       `json:"take_ownership,omitempty"`
	setStringKinds map[string]helmScalarKind
}

// HelmScalarValue accepts a JSON string, number or boolean for a Helm value and
// keeps its string form for command construction. HelmParams retains the JSON
// kind so native numbers and booleans can use Helm's typed --set-json flag.
// Objects and arrays are rejected: they have no single string representation.
type HelmScalarValue string

func (v *HelmScalarValue) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("helm set value is empty")
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		*v = HelmScalarValue(s)
	case 't', 'f':
		var b bool
		if err := json.Unmarshal(trimmed, &b); err != nil {
			return err
		}
		*v = HelmScalarValue(strconv.FormatBool(b))
	default:
		if string(trimmed) == "null" {
			return fmt.Errorf("helm set value must be a string, number or boolean")
		}
		var n json.Number
		if err := json.Unmarshal(trimmed, &n); err != nil {
			return fmt.Errorf("helm set value must be a string, number or boolean")
		}
		*v = HelmScalarValue(n.String())
	}
	return nil
}

type helmScalarKind uint8

const (
	helmScalarString helmScalarKind = iota
	helmScalarNumber
	helmScalarBoolean
)

func helmScalarKindForJSON(data []byte) (helmScalarKind, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return 0, fmt.Errorf("helm set value is empty")
	}
	switch trimmed[0] {
	case '"':
		return helmScalarString, nil
	case 't', 'f':
		var value bool
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return 0, err
		}
		return helmScalarBoolean, nil
	default:
		if string(trimmed) == "null" {
			return 0, fmt.Errorf("helm set value must be a string, number or boolean")
		}
		var value json.Number
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return 0, fmt.Errorf("helm set value must be a string, number or boolean")
		}
		return helmScalarNumber, nil
	}
}

func (p *HelmParams) UnmarshalJSON(data []byte) error {
	type helmParamsAlias HelmParams
	var wire struct {
		*helmParamsAlias
		SetStrings map[string]json.RawMessage `json:"set_strings,omitempty"`
	}
	wire.helmParamsAlias = (*helmParamsAlias)(p)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid helm request")
	}

	p.SetStrings = nil
	p.setStringKinds = nil
	if len(wire.SetStrings) == 0 {
		return nil
	}
	p.SetStrings = make(map[string]HelmScalarValue, len(wire.SetStrings))
	p.setStringKinds = make(map[string]helmScalarKind, len(wire.SetStrings))
	for key, raw := range wire.SetStrings {
		var value HelmScalarValue
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
		kind, err := helmScalarKindForJSON(raw)
		if err != nil {
			return err
		}
		p.SetStrings[key] = value
		p.setStringKinds[key] = kind
	}
	return nil
}

func (p HelmParams) HelmSetFlag(key string) string {
	switch p.setStringKinds[key] {
	case helmScalarNumber, helmScalarBoolean:
		return "--set-json"
	default:
		return "--set-string"
	}
}

// WebParams is the structured request accepted by doops_web.
type WebParams struct {
	SessionID   string   `json:"session_id"`
	Operation   string   `json:"operation"`
	RuntimeMode string   `json:"runtime_mode,omitempty"`
	SiteID      string   `json:"site_id,omitempty"`
	Domain      string   `json:"domain,omitempty"`
	DomainBase  string   `json:"domain_base,omitempty"`
	HostIP      string   `json:"host_ip,omitempty"`
	Provider    string   `json:"provider,omitempty"`
	Dist        string   `json:"dist,omitempty"`
	ConfigFile  string   `json:"config_file,omitempty"`
	Config      string   `json:"config,omitempty"`
	SnippetFile string   `json:"snippet_file,omitempty"`
	Snippet     string   `json:"snippet,omitempty"`
	SnippetName string   `json:"snippet_name,omitempty"`
	TLSCert     string   `json:"tls_cert,omitempty"`
	TLSKey      string   `json:"tls_key,omitempty"`
	HealthURLs  []string `json:"health_urls,omitempty"`
	Proxies     []string `json:"proxies,omitempty"`
	Upstreams   []string `json:"upstreams,omitempty"`
	Wildcard    *bool    `json:"wildcard,omitempty"`
}

// AgentPromptParams 用于 doops_agent_prompt 代理执行。
type AgentPromptParams struct {
	SessionID          string                   `json:"session_id"`
	Instruction        string                   `json:"instruction"`
	Attachments        []AgentPromptAttachment  `json:"attachments,omitempty"`
	Model              string                   `json:"model,omitempty"`
	Mode               string                   `json:"mode,omitempty"`
	ResponseFormat     string                   `json:"response_format,omitempty"`
	EventFormat        string                   `json:"event_format,omitempty"` // Empty or "acp"; validated by the prompt bridge.
	Operation          string                   `json:"operation,omitempty"`
	Skill              string                   `json:"skill,omitempty"`
	CredentialMode     string                   `json:"credential_mode,omitempty"`
	CredentialRefs     []string                 `json:"credential_refs,omitempty"`
	CredentialBindings []AgentCredentialBinding `json:"credential_bindings,omitempty"`
	// A dosql prompt carrying SQL is executed verbatim by the Agent without the
	// model; the instruction is only an audit description.
	SQL             string `json:"sql,omitempty"`
	SQLVerification string `json:"sql_verification,omitempty"`
	SQLDatabase     string `json:"sql_database,omitempty"`
	SQLDryRun       bool   `json:"sql_dry_run,omitempty"`
	// SQLCredential names one database credential bound to the target (resource
	// name or credential ID). The Gateway then binds only that credential.
	SQLCredential string `json:"sql_credential,omitempty"`
	// SQLCopyFrom names the source database; SQLDatabase is the destination.
	// The Agent copies SQLCopyTables between the two bound databases itself.
	SQLCopyFrom   string   `json:"sql_copy_from,omitempty"`
	SQLCopyTables []string `json:"sql_copy_tables,omitempty"`
}

// AgentPromptAttachment is a browser-uploaded conversation document. The
// Gateway forwards it to doagent's native attachment/upload RPC before the
// prompt is admitted; the public tool never exposes a workspace path.
type AgentPromptAttachment struct {
	Filename   string `json:"filename"`
	MediaType  string `json:"media_type"`
	Base64Data string `json:"base64_data"`
}

// AgentCredentialBinding exposes only materialization metadata to doagent.
// Secret payloads remain inside the Gateway-to-Agent control plane.
type AgentCredentialBinding struct {
	Name            string   `json:"name"`
	Use             string   `json:"use"`
	Namespace       string   `json:"namespace"`
	ResourceName    string   `json:"resource_name"`
	SecretType      string   `json:"secret_type"`
	Keys            []string `json:"keys,omitempty"`
	ResourceVersion string   `json:"resource_version"`
	Digest          string   `json:"digest,omitempty"`
	CredentialID    string   `json:"credential_id,omitempty"`
	GrantID         string   `json:"grant_id,omitempty"`
	VersionID       string   `json:"version_id,omitempty"`
	HelmRelease     string   `json:"helm_release,omitempty"`
	HelmChart       string   `json:"helm_chart,omitempty"`
	ContextKind     string   `json:"context_kind,omitempty"`
	ContextSocket   string   `json:"context_socket,omitempty"`
	DatabaseAssetID string   `json:"database_asset_id,omitempty"`
	EnvironmentID   string   `json:"environment_id,omitempty"`
	DatabaseEngine  string   `json:"database_engine,omitempty"`
	DatabaseName    string   `json:"database_name,omitempty"`
}

// GitCloneParams clones a configured repository into a session workspace.
type GitCloneParams struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
	Branch    string `json:"branch"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	Directory string `json:"directory,omitempty"`
}

// --- 异步任务相关类型 (doops_bg / doops_task_status) ---

// BgExecParams 用于 doops_bg 后台异步任务提交。
// 命令通过 setsid 在独立进程组中运行，不受 PTY session 生命周期影响。
type BgExecParams struct {
	SessionID string `json:"session_id"` // 工作区会话
	Command   string `json:"command"`    // Shell 命令
	LogPath   string `json:"log_path"`   // 可选: 自定义日志路径，默认在 session 工作区内
}

// TaskStatusParams 用于 doops_task_status 查询异步任务状态。
type TaskStatusParams struct {
	TaskID string `json:"task_id"` // 任务 ID
	Lines  int    `json:"lines"`   // 可选: 返回最后 N 行日志，默认 30
}

// TaskInfo 描述一个异步任务的状态。
type TaskInfo struct {
	TaskID   string `json:"task_id"`
	PID      int    `json:"pid"`
	Status   string `json:"status"` // "running" | "done" | "failed"
	ExitCode int    `json:"exit_code"`
	LogPath  string `json:"log_path"`
	LogTail  string `json:"log_tail"` // 最后 N 行日志
}

type AppProbeTarget struct {
	AssetID string `json:"asset_id"`
	URL     string `json:"url"`
}

// NodeInfoParams 用于 doops_node_info 查看节点信息
type NodeInfoParams struct {
	SessionID string           `json:"session_id,omitempty"`
	Section   string           `json:"section,omitempty"`
	Targets   []AppProbeTarget `json:"targets,omitempty"`
}

// MonitorInspectAsset describes one asset dispatched by Gateway to an Agent for
// read-only metric extraction and strategy-based inspection. Never carries secret payloads.
type MonitorInspectAsset struct {
	AssetID   string   `json:"asset_id"`
	AssetName string   `json:"asset_name,omitempty"`
	AssetKind string   `json:"asset_kind"` // vm | physical | k8s | container | app | database
	Strategy  string   `json:"strategy"`   // vm_host | ssh_host | k8s_cluster | container_runtime | app_service | database_service
	Address   string   `json:"address,omitempty"`
	Port      int      `json:"port,omitempty"`
	SSHUser   string   `json:"ssh_user,omitempty"`
	URL       string   `json:"url,omitempty"`
	Engine    string   `json:"engine,omitempty"`
	Database  string   `json:"database,omitempty"`
	Namespace string   `json:"namespace,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// MonitorInspectThresholds defines numeric thresholds applied during an inspection run.
type MonitorInspectThresholds struct {
	CPUWarnPercent    float64  `json:"cpu_warn_percent,omitempty"`
	CPUCritPercent    float64  `json:"cpu_crit_percent,omitempty"`
	MemWarnPercent    float64  `json:"mem_warn_percent,omitempty"`
	MemCritPercent    float64  `json:"mem_crit_percent,omitempty"`
	DiskWarnPercent   float64  `json:"disk_warn_percent,omitempty"`
	DiskCritPercent   float64  `json:"disk_crit_percent,omitempty"`
	DiskLatencyWarnMs float64  `json:"disk_latency_warn_ms,omitempty"`
	LatencyWarnMs     float64  `json:"latency_warn_ms,omitempty"`
	ErrorRateWarnPct  float64  `json:"error_rate_warn_pct,omitempty"`
	CertWarnDays      int      `json:"cert_warn_days,omitempty"`
	MinUptimeSeconds  int64    `json:"min_uptime_seconds,omitempty"`
	DeniedProcesses   []string `json:"denied_processes,omitempty"`
}

// MonitorInspectParams is the Gateway -> Agent RPC payload for doops_monitor_inspect.
type MonitorInspectParams struct {
	SessionID  string                   `json:"session_id"`
	RunID      string                   `json:"run_id"`
	Strategies []string                 `json:"strategies,omitempty"`
	Assets     []MonitorInspectAsset    `json:"assets"`
	Thresholds MonitorInspectThresholds `json:"thresholds,omitempty"`
}

// MonitorInspectCheck records one structured inspection verdict.
type MonitorInspectCheck struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Category  string `json:"category"` // availability | resource | security | compliance | runtime | database | k8s
	Status    string `json:"status"`   // pass | warning | critical | skipped
	Value     string `json:"value,omitempty"`
	Threshold string `json:"threshold,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// MonitorInspectMetrics holds normalized scalar metrics extracted for one asset.
type MonitorInspectMetrics struct {
	CPUCores          *float64 `json:"cpu_cores,omitempty"`
	CPUTotalCores     *float64 `json:"cpu_total_cores,omitempty"`
	CPUPercent        *float64 `json:"cpu_percent,omitempty"`
	KernelCPUCores    *float64 `json:"kernel_cpu_cores,omitempty"`
	MemoryUsedBytes   *float64 `json:"memory_used_bytes,omitempty"`
	MemoryTotalBytes  *float64 `json:"memory_total_bytes,omitempty"`
	MemoryPercent     *float64 `json:"memory_percent,omitempty"`
	KernelSlabBytes   *float64 `json:"kernel_slab_bytes,omitempty"`
	DiskUsedBytes     *float64 `json:"disk_used_bytes,omitempty"`
	DiskTotalBytes    *float64 `json:"disk_total_bytes,omitempty"`
	DiskPercent       *float64 `json:"disk_percent,omitempty"`
	NetBytesPerSec    *float64 `json:"net_bytes_per_sec,omitempty"`
	DiskIOPS          *float64 `json:"disk_iops,omitempty"`
	DiskLatencyMs     *float64 `json:"disk_latency_ms,omitempty"`
	GPUPercent        *float64 `json:"gpu_percent,omitempty"`
	ResponseTimeMs    *float64 `json:"response_time_ms,omitempty"`
	ErrorRate         *float64 `json:"error_rate,omitempty"`
	ActiveConnections *float64 `json:"active_connections,omitempty"`
	RequestsPerSec    *float64 `json:"requests_per_sec,omitempty"`
	UptimeSeconds     *float64 `json:"uptime_seconds,omitempty"`
	Load1m            *float64 `json:"load_1m,omitempty"`
	OSInfo            string   `json:"os_info,omitempty"`
	KernelInfo        string   `json:"kernel_info,omitempty"`
}

// MonitorInspectSeriesPoint represents a granular metric point to bridge into asset_monitor_series.
type MonitorInspectSeriesPoint struct {
	Type      string   `json:"type"`
	AssetID   string   `json:"asset_id"`
	Namespace string   `json:"namespace,omitempty"`
	Origin    string   `json:"origin"`
	Metric    string   `json:"metric"`
	DeviceID  string   `json:"device_id,omitempty"`
	Unit      string   `json:"unit"`
	AtMs      int64    `json:"at_ms"`
	Status    string   `json:"status"` // ok | missing | not_applicable
	Value     *float64 `json:"value"`
}

// MonitorInspectAssetResult is the Agent result for one asset + strategy execution.
type MonitorInspectAssetResult struct {
	AssetID         string                      `json:"asset_id"`
	AssetName       string                      `json:"asset_name,omitempty"`
	AssetKind       string                      `json:"asset_kind"`
	Strategy        string                      `json:"strategy"`
	CollectorMethod string                      `json:"collector_method"` // agent_local | ssh | k8s_api | http_probe | dosql | tcp_probe
	Status          string                      `json:"status"`           // pass | warning | critical | error | skipped
	Reason          string                      `json:"reason,omitempty"`
	CollectedAtMs   int64                       `json:"collected_at_ms"`
	DurationMs      int64                       `json:"duration_ms"`
	Tags            []string                    `json:"tags,omitempty"`
	Metrics         MonitorInspectMetrics       `json:"metrics"`
	Checks          []MonitorInspectCheck       `json:"checks"`
	Details         map[string]any              `json:"details,omitempty"`
	Evidence        map[string]any              `json:"evidence,omitempty"`
	SeriesPoints    []MonitorInspectSeriesPoint `json:"series_points,omitempty"`
}

// MonitorInspectResponse is the JSON payload returned by the Agent from doops_monitor_inspect.
type MonitorInspectResponse struct {
	RunID         string                      `json:"run_id"`
	CollectedAtMs int64                       `json:"collected_at_ms"`
	Results       []MonitorInspectAssetResult `json:"results"`
}
