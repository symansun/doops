package server

// B-13 (11-22 remediation ledger): the Agent and Gateway ship more than sixty
// time constants that grew one at a time. Several of them are the same value in
// three different files, a few only work because their ordering relative to a
// neighbouring timeout happens to hold, and nothing validates any of them, so a
// typo silently becomes a different budget instead of a startup failure.
//
// This file is step one: one table for the durations that matter operationally,
// loaded and validated once at process start. The modules that still own their
// constants (Helm, MCP, MySQL, Edge, monitor retention) are pinned to this table
// by TestTimeoutDefaultsMatchCodeConstants rather than rewired here, so this
// change cannot move a single value.
//
// The authoritative documentation is backend/docs/RUNTIME_LIMITS.md;
// TestTimeoutDefaultsMatchDoc fails when the two disagree in either direction.

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// timeoutDefaults is the resolved value of every timeout in the table below.
// The fields without an environment variable today (Helm termination grace and
// the MCP budgets) are carried so the derived budgets can be checked against
// each other.
type timeoutDefaults struct {
	ToolExecutionTimeout        time.Duration // Agent-side limit for one tool execution
	CompletedTaskMaxAge         time.Duration // how long a finished task record lingers
	ToolDeadlineMargin          time.Duration // how much earlier the Agent gives up than the Gateway
	HelmSettleReserve           time.Duration // tail of the MCP budget kept for SIGTERM and settle
	HelmSettleTimeout           time.Duration // history read plus pending-release unlock
	HelmTerminationGrace        time.Duration // Helm's window to record a terminal state
	HelmMaxWait                 time.Duration // --wait ceiling for one doops_helm call
	MCPToolTimeout              time.Duration // doagent's ceiling for one MCP tool call
	MCPCallSettle               time.Duration // margin between mcpToolCallDeadline and MCPToolTimeout
	AgentLease                  time.Duration // Agent lease; the heartbeat ping period is a third of it
	AgentHeartbeatFlush         time.Duration // heartbeat batch flush period
	TunnelReadTimeout           time.Duration // Agent-side read deadline on the Gateway tunnel (D43: never merged with the lease)
	EdgeDialTimeout             time.Duration // Edge dial and handshake budget
	EdgeWriteStall              time.Duration // how long one Edge write may stall before the scheduler parks the connection
	EdgeConnIDQuarantine        time.Duration // how long a released Edge connection id stays unusable
	EdgeHelloTimeout            time.Duration // deadline for the Edge hello exchange
	EdgeWSReadTimeout           time.Duration // idle read deadline on one Edge websocket (D43: never merged with the Agent tunnel)
	MySQLConnectTimeout         time.Duration // driver connect timeout
	MCPProbeTimeout             time.Duration // MCP server probe and HTTP client timeout
	CredentialDeliveryTimeout   time.Duration // model provider and registry credential delivery
	MonitorRetentionMinInterval time.Duration // throttle window for the post-run retention trigger
	PlatformLoadSampleRetention time.Duration // how long platform load samples are kept
	TargetQueueTimeout          time.Duration // how long a request waits for a busy target
	AuditRetentionInterval      time.Duration // how often audit retention runs
	ExpiredTokenSweepInterval   time.Duration // how often expired tokens are swept
	// B-13 step four: budgets that used to be private literals in the gateway
	// command, the offline reaper and the audit purge.
	OperationTimeout         time.Duration // one forwarded operation's maximum runtime
	DetachedTaskTimeout      time.Duration // detached background task lifetime
	TargetReconnectGrace     time.Duration // how long a request waits for a reconnecting Agent
	ShutdownDrainTimeout     time.Duration // SIGTERM drain window for running operations
	MonitorTick              time.Duration // due monitor schedule check period
	HTTPReadHeaderTimeout    time.Duration // HTTP header read deadline
	HTTPIdleTimeout          time.Duration // HTTP keep-alive idle deadline
	OfflineReaperInterval    time.Duration // offline instance prune period
	OfflineInstanceRetention time.Duration // how long an offline instance record stays
	AuditPurgeBatchPause     time.Duration // pause between audit purge batches
	// B-13 step five: the resume windows and the HTTP shutdown budget used to be
	// read through durationFromEnv, which silently fell back on a typo.
	ExecutionResumeGrace   time.Duration // how long a client waits for a reconnecting Agent
	ExecutionResumeWaitMax time.Duration // ceiling on one HTTP ?wait_ms request (not the record lifetime)
	ExecutionResumeTTL     time.Duration // how long a terminal execution record stays readable
	AgentExecutionTTL      time.Duration // Agent-side claim record lifetime
	HTTPShutdownTimeout    time.Duration // HTTP server shutdown budget after the drain window
}

// timeoutSpec ties one table row to its environment variable, its bounds and the
// field it resolves into. Env is empty for values that take no override yet.
type timeoutSpec struct {
	Env         string
	Constant    string // the constant that still owns this value elsewhere
	Meaning     string
	Default     time.Duration
	Min         time.Duration
	Max         time.Duration
	BareSeconds bool // keep positiveEnvDuration's bare-number-is-seconds behaviour
	set         func(*timeoutDefaults, time.Duration)
}

func timeoutSpecs() []timeoutSpec {
	return []timeoutSpec{
		{
			Env:         ToolTimeoutEnv,
			Constant:    "DefaultToolExecutionTimeout",
			Meaning:     "bounds one Agent tool execution and caps an advertised Gateway timeout",
			Default:     30 * time.Minute,
			Min:         time.Second,
			Max:         24 * time.Hour,
			BareSeconds: true,
			set:         func(t *timeoutDefaults, d time.Duration) { t.ToolExecutionTimeout = d },
		},
		{
			Env:         "DOOPS_COMPLETED_TASK_MAX_AGE",
			Constant:    "defaultCompletedTaskMaxAge",
			Meaning:     "ages out finished task records",
			Default:     24 * time.Hour,
			Min:         time.Millisecond,
			Max:         30 * 24 * time.Hour,
			BareSeconds: true,
			set:         func(t *timeoutDefaults, d time.Duration) { t.CompletedTaskMaxAge = d },
		},
		{
			Env:      "DOOPS_TOOL_DEADLINE_MARGIN",
			Constant: "toolDeadlineMargin",
			Meaning:  "makes the Agent return a definite timeout before the Gateway gives up",
			Default:  5 * time.Second,
			Min:      time.Millisecond,
			Max:      time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.ToolDeadlineMargin = d },
		},
		{
			Env:      "DOOPS_HELM_SETTLE_RESERVE",
			Constant: "helmSettleReserve",
			Meaning:  "keeps room for the SIGTERM grace and the settle step at the end of the MCP budget",
			Default:  90 * time.Second,
			Min:      time.Second,
			Max:      30 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.HelmSettleReserve = d },
		},
		{
			Env:      "DOOPS_HELM_SETTLE_TIMEOUT",
			Constant: "helmSettleTimeout",
			Meaning:  "bounds reading release history and unlocking a pending release",
			Default:  45 * time.Second,
			Min:      time.Second,
			Max:      30 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.HelmSettleTimeout = d },
		},
		{
			Env:      "DOOPS_HELM_MAX_WAIT",
			Constant: "maxHelmWaitTimeout",
			Meaning:  "bounds the --wait of one doops_helm call",
			Default:  25 * time.Minute,
			Min:      time.Second,
			Max:      2 * time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.HelmMaxWait = d },
		},
		{
			Env:      "DOOPS_AGENT_LEASE",
			Constant: "AgentLease default",
			Meaning:  "leases an Agent; the heartbeat ping period is a third of it",
			Default:  90 * time.Second,
			Min:      time.Second,
			Max:      10 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.AgentLease = d },
		},
		{
			Env:      "DOOPS_AGENT_HEARTBEAT_FLUSH",
			Constant: "StartHeartbeatFlusher interval",
			Meaning:  "flushes in-memory heartbeats to the store",
			Default:  30 * time.Second,
			Min:      time.Second,
			Max:      10 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.AgentHeartbeatFlush = d },
		},
		{
			// D43 keeps this knob separate from DOOPS_AGENT_LEASE: the lease
			// drives ping, heartbeat flush, liveness and the reaper, while this
			// bounds how long the Agent may stay silent on one tunnel before the
			// read deadline ends it. The ordering between them is still checked
			// below, because a tunnel cut sooner than its own lease would drop a
			// connection the Gateway still counts as alive.
			Env:         "DOOPS_TUNNEL_READ_TIMEOUT",
			Constant:    "defaultAgentTunnelReadTimeout",
			Meaning:     "Agent-side read deadline on the Gateway tunnel; at least a third of the Agent lease (the ping period)",
			Default:     90 * time.Second,
			Min:         time.Second,
			Max:         10 * time.Minute,
			BareSeconds: true,
			set:         func(t *timeoutDefaults, d time.Duration) { t.TunnelReadTimeout = d },
		},
		{
			Env:      "DOOPS_EDGE_DIAL_TIMEOUT",
			Constant: "defaultEdgeDialTimeout",
			Meaning:  "bounds the Edge dial and handshake budget (edgeOpeningAckSlack is added on top)",
			Default:  5 * time.Second,
			Min:      100 * time.Millisecond,
			Max:      5 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.EdgeDialTimeout = d },
		},
		{
			Env:         "DOOPS_EDGE_WRITE_STALL",
			Constant:    "defaultEdgeWriteStall",
			Meaning:     "how long an Edge connection may make no write progress before the scheduler parks it",
			Default:     30 * time.Second,
			Min:         100 * time.Millisecond,
			Max:         10 * time.Minute,
			BareSeconds: true,
			set:         func(t *timeoutDefaults, d time.Duration) { t.EdgeWriteStall = d },
		},
		{
			Env:         "DOOPS_EDGE_CONN_ID_QUARANTINE",
			Constant:    "defaultEdgeConnIDQuarantine",
			Meaning:     "how long a released Edge connection id stays unusable, so an in-flight handshake cannot be answered by the new owner of the id",
			Default:     30 * time.Second,
			Min:         time.Second,
			Max:         10 * time.Minute,
			BareSeconds: true,
			set:         func(t *timeoutDefaults, d time.Duration) { t.EdgeConnIDQuarantine = d },
		},
		{
			Env:         "DOOPS_EDGE_HELLO_TIMEOUT",
			Constant:    "defaultEdgeHelloTimeout",
			Meaning:     "deadline for the Edge hello exchange after the websocket opened",
			Default:     5 * time.Second,
			Min:         100 * time.Millisecond,
			Max:         time.Minute,
			BareSeconds: true,
			set:         func(t *timeoutDefaults, d time.Duration) { t.EdgeHelloTimeout = d },
		},
		{
			Env:      "DOOPS_EDGE_WS_READ_TIMEOUT",
			Constant: "edgeWSReadTimeout",
			Meaning:  "idle read deadline on one Edge websocket; the Edge pings every agentWSPingInterval (5s), so this only catches a silent peer",
			Default:  20 * time.Second,
			Min:      time.Second,
			Max:      5 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.EdgeWSReadTimeout = d },
		},
		{
			Env:      "DOOPS_MYSQL_CONNECT_TIMEOUT",
			Constant: "cfg.Timeout literals in the MySQL stores",
			Meaning:  "bounds opening one MySQL connection",
			Default:  8 * time.Second,
			Min:      100 * time.Millisecond,
			Max:      2 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.MySQLConnectTimeout = d },
		},
		{
			Env:      "DOOPS_MCP_PROBE_TIMEOUT",
			Constant: "mcpProbeTimeout",
			Meaning:  "bounds probing an MCP server and the probe HTTP client",
			Default:  12 * time.Second,
			Min:      100 * time.Millisecond,
			Max:      5 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.MCPProbeTimeout = d },
		},
		{
			Env:      "DOOPS_CREDENTIAL_DELIVERY_TIMEOUT",
			Constant: "modelProviderDeliveryTimeout and registryAuthDeliveryTimeout",
			Meaning:  "bounds one model provider or registry credential delivery",
			Default:  3 * time.Minute,
			Min:      time.Second,
			Max:      30 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.CredentialDeliveryTimeout = d },
		},
		{
			Env:      "DOOPS_MONITOR_RETENTION_MIN_INTERVAL",
			Constant: "monitorRetentionMinInterval",
			Meaning:  "throttles the post-run monitor retention trigger",
			Default:  15 * time.Minute,
			Min:      time.Second,
			Max:      24 * time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.MonitorRetentionMinInterval = d },
		},
		{
			Env:      "DOOPS_PLATFORM_LOAD_SAMPLE_RETENTION",
			Constant: "30 * 24 * time.Hour in PruneOfflineAgentStatus",
			Meaning:  "ages out the platform load samples behind the resource trend chart",
			Default:  30 * 24 * time.Hour,
			Min:      time.Hour,
			Max:      365 * 24 * time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.PlatformLoadSampleRetention = d },
		},
		{
			Env:      "DOOPS_TARGET_QUEUE_TIMEOUT",
			Constant: "the -target-queue-timeout flag default and the hub fallback",
			Meaning:  "bounds how long a request waits for a busy target before it fails",
			Default:  10 * time.Minute,
			Min:      time.Second,
			Max:      24 * time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.TargetQueueTimeout = d },
		},
		{
			Env:      "DOOPS_AUDIT_RETENTION_INTERVAL",
			Constant: "AuditRetentionInterval",
			Meaning:  "how often the audit retention sweep runs (the window itself is -audit-retention)",
			Default:  6 * time.Hour,
			Min:      time.Second,
			Max:      24 * time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.AuditRetentionInterval = d },
		},
		{
			Env:      "DOOPS_EXPIRED_TOKEN_SWEEP_INTERVAL",
			Constant: "AuditRetentionInterval used by StartExpiredTokenCleanup",
			Meaning:  "how often expired session tokens are swept",
			Default:  6 * time.Hour,
			Min:      time.Second,
			Max:      24 * time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.ExpiredTokenSweepInterval = d },
		},
		{
			Env:      "DOOPS_OPERATION_TIMEOUT",
			Constant: "the -operation-timeout flag default and the hub fallback",
			Meaning:  "bounds one forwarded operation end to end",
			Default:  60 * time.Minute,
			Min:      time.Second,
			Max:      24 * time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.OperationTimeout = d },
		},
		{
			Env:      "DOOPS_DETACHED_TASK_TIMEOUT",
			Constant: "the -detached-task-timeout flag default",
			Meaning:  "bounds the lifetime of a detached background task (doops_bg detach)",
			Default:  24 * time.Hour,
			Min:      time.Second,
			Max:      30 * 24 * time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.DetachedTaskTimeout = d },
		},
		{
			Env:      "DOOPS_TARGET_RECONNECT_GRACE",
			Constant: "the -target-reconnect-grace flag default and the hub fallback",
			Meaning:  "how long a request waits for a reconnecting Agent before it fails",
			Default:  10 * time.Second,
			Min:      time.Second,
			Max:      5 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.TargetReconnectGrace = d },
		},
		{
			Env:      "DOOPS_SHUTDOWN_DRAIN_TIMEOUT",
			Constant: "the -shutdown-drain-timeout flag default",
			Meaning:  "how long SIGTERM waits for running operations before closing connections",
			Default:  2 * time.Minute,
			Min:      time.Second,
			Max:      30 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.ShutdownDrainTimeout = d },
		},
		{
			Env:      "DOOPS_MONITOR_TICK",
			Constant: "the -monitor-tick flag default",
			Meaning:  "how often due periodic monitor and inspection schedules are checked",
			Default:  15 * time.Second,
			Min:      time.Second,
			Max:      time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.MonitorTick = d },
		},
		{
			Env:      "DOOPS_HTTP_READ_HEADER_TIMEOUT",
			Constant: "ReadHeaderTimeout in gatewayHTTPServer",
			Meaning:  "bounds reading one HTTP request header block",
			Default:  10 * time.Second,
			Min:      time.Second,
			Max:      5 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.HTTPReadHeaderTimeout = d },
		},
		{
			Env:      "DOOPS_HTTP_IDLE_TIMEOUT",
			Constant: "IdleTimeout in gatewayHTTPServer",
			Meaning:  "bounds an idle HTTP keep-alive connection",
			Default:  2 * time.Minute,
			Min:      time.Second,
			Max:      time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.HTTPIdleTimeout = d },
		},
		{
			Env:      "DOOPS_OFFLINE_REAPER_INTERVAL",
			Constant: "the StartOfflineInstanceReaper interval and its fallback",
			Meaning:  "how often old offline instance records are pruned",
			Default:  time.Hour,
			Min:      time.Second,
			Max:      24 * time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.OfflineReaperInterval = d },
		},
		{
			Env:      "DOOPS_OFFLINE_INSTANCE_RETENTION",
			Constant: "OfflineInstanceRetention",
			Meaning:  "how long an offline instance record is kept before the reaper deletes it",
			Default:  14 * 24 * time.Hour,
			Min:      time.Second,
			Max:      365 * 24 * time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.OfflineInstanceRetention = d },
		},
		{
			Env:      "DOOPS_AUDIT_PURGE_BATCH_PAUSE",
			Constant: "AuditPurgeBatchPause",
			Meaning:  "pauses between audit purge batches so other writers can use the database",
			Default:  100 * time.Millisecond,
			Min:      time.Millisecond,
			Max:      time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.AuditPurgeBatchPause = d },
		},
		{
			Env:      "DOOPS_EXECUTION_RESUME_GRACE",
			Constant: "executionResumeGraceDefault",
			Meaning:  "how long a client waits for a reconnecting Agent before the call fails with a definite result",
			Default:  60 * time.Second,
			Min:      time.Second,
			Max:      10 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.ExecutionResumeGrace = d },
		},
		{
			// The ceiling on one blocking GET /v1/executions/<id>?wait_ms=N. It is
			// deliberately a separate knob from the grace: the grace says how
			// long a record keeps waiting for a reconnecting Agent, this says how
			// long one HTTP request may block before it answers "still running".
			Env:      "DOOPS_EXECUTION_RESUME_WAIT_MAX",
			Constant: "executionResumeWaitMax",
			Meaning:  "ceiling on one blocking ?wait_ms request; must not exceed executionResumeGrace",
			Default:  5 * time.Second,
			Min:      100 * time.Millisecond,
			Max:      10 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.ExecutionResumeWaitMax = d },
		},
		{
			Env:      "DOOPS_EXECUTION_RESUME_TTL",
			Constant: "executionResumeTTLDefault",
			Meaning:  "how long a terminal execution record stays readable after the execution ended",
			Default:  30 * time.Minute,
			Min:      time.Minute,
			Max:      24 * time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.ExecutionResumeTTL = d },
		},
		{
			Env:      "DOOPS_AGENT_EXECUTION_TTL",
			Constant: "agentExecutionTTLDefault",
			Meaning:  "how long an Agent keeps a finished execution claim so a reconnect can attach to it",
			Default:  30 * time.Minute,
			Min:      time.Minute,
			Max:      24 * time.Hour,
			set:      func(t *timeoutDefaults, d time.Duration) { t.AgentExecutionTTL = d },
		},
		{
			Env:      "DOOPS_HTTP_SHUTDOWN_TIMEOUT",
			Constant: "the srv.Shutdown budget in gatewaycmd",
			Meaning:  "after the drain window ends, how long the HTTP server gets to finish closing connections",
			Default:  10 * time.Second,
			Min:      100 * time.Millisecond,
			Max:      5 * time.Minute,
			set:      func(t *timeoutDefaults, d time.Duration) { t.HTTPShutdownTimeout = d },
		},
		{
			// No override yet: the constant below still owns the value and
			// TestTimeoutDefaultsKeepPreWiringValues keeps the table honest.
			Constant: "helmTerminationGrace",
			Meaning:  "Helm's window to record a terminal state after SIGTERM",
			Default:  30 * time.Second,
			set:      func(t *timeoutDefaults, d time.Duration) { t.HelmTerminationGrace = d },
		},
		{
			Constant: "doopsMCPToolTimeoutSeconds",
			Meaning:  "doagent's ceiling for one MCP tool call",
			Default:  1800 * time.Second,
			set:      func(t *timeoutDefaults, d time.Duration) { t.MCPToolTimeout = d },
		},
		{
			Constant: "mcpToolCallDeadline settle margin",
			Meaning:  "the settle margin subtracted from doagent's MCP ceiling",
			Default:  30 * time.Second,
			set:      func(t *timeoutDefaults, d time.Duration) { t.MCPCallSettle = d },
		},
	}
}

// defaultTimeouts is the table before any environment variable is applied.
func defaultTimeouts() timeoutDefaults {
	var t timeoutDefaults
	for _, spec := range timeoutSpecs() {
		spec.set(&t, spec.Default)
	}
	return t
}

// timeouts is the process-wide snapshot. It is resolved once, at package
// initialisation, so every request in this process sees the same budget and an
// invalid value stops the process instead of silently running on a default.
// Every runtime call site reads this snapshot; none keeps a private copy.
var timeouts = mustLoadTimeouts(os.Getenv)

// mcpToolTimeoutSeconds is doagent's MCP tool budget in whole seconds, the form
// the tool schema and the Agent-side server registry take. It is derived from
// the table, so raising the MCP tool timeout also moves the budget advertised
// to doagent instead of leaving the two out of step.
func mcpToolTimeoutSeconds() int {
	return int(timeouts.MCPToolTimeout / time.Second)
}

// mysqlPingTimeout bounds the initial ping of every MySQL opener. It shares the
// DOOPS_MYSQL_CONNECT_TIMEOUT knob on purpose: a ping that gives up sooner than
// the dial allowance reports "cannot connect" for a slow database the operator
// already permitted. The fallback only matters for a zeroed snapshot (tests);
// a zero deadline would fail the ping instantly.
func mysqlPingTimeout() time.Duration {
	if d := timeouts.MySQLConnectTimeout; d > 0 {
		return d
	}
	return defaultTimeouts().MySQLConnectTimeout
}

// executionResumeGrace, executionResumeTTL and agentExecutionTTL are the resume
// windows as the current process resolved them. The fallback only matters for a
// zeroed snapshot (tests); a zero window would make the caller give up instantly.
func executionResumeGrace() time.Duration {
	if d := timeouts.ExecutionResumeGrace; d > 0 {
		return d
	}
	return defaultTimeouts().ExecutionResumeGrace
}

func executionResumeTTL() time.Duration {
	if d := timeouts.ExecutionResumeTTL; d > 0 {
		return d
	}
	return defaultTimeouts().ExecutionResumeTTL
}

func agentExecutionTTL() time.Duration {
	if d := timeouts.AgentExecutionTTL; d > 0 {
		return d
	}
	return defaultTimeouts().AgentExecutionTTL
}

// The accessors below are the only way packages outside server may read a table
// value. The gateway command needs them for the flag default and the background
// loop periods it starts.
func AgentHeartbeatFlush() time.Duration { return timeouts.AgentHeartbeatFlush }

func TargetQueueTimeout() time.Duration { return timeouts.TargetQueueTimeout }

func AuditRetentionInterval() time.Duration { return timeouts.AuditRetentionInterval }

func ExpiredTokenSweepInterval() time.Duration { return timeouts.ExpiredTokenSweepInterval }

// B-13 step four: the gateway command and the offline reaper read the rest of
// the start-up budgets through these accessors, so no flag default or loop
// period stays a private literal.
func OperationTimeout() time.Duration { return timeouts.OperationTimeout }

func DetachedTaskTimeout() time.Duration { return timeouts.DetachedTaskTimeout }

func TargetReconnectGrace() time.Duration { return timeouts.TargetReconnectGrace }

func ShutdownDrainTimeout() time.Duration { return timeouts.ShutdownDrainTimeout }

func MonitorTick() time.Duration { return timeouts.MonitorTick }

func HTTPReadHeaderTimeout() time.Duration { return timeouts.HTTPReadHeaderTimeout }

func HTTPIdleTimeout() time.Duration { return timeouts.HTTPIdleTimeout }

func OfflineReaperInterval() time.Duration { return timeouts.OfflineReaperInterval }

func OfflineInstanceRetention() time.Duration { return timeouts.OfflineInstanceRetention }

// HTTPShutdownTimeout is the budget for closing the HTTP server after the drain
// window ended; it is not the drain window itself.
func HTTPShutdownTimeout() time.Duration {
	if d := timeouts.HTTPShutdownTimeout; d > 0 {
		return d
	}
	return defaultTimeouts().HTTPShutdownTimeout
}

// tunnelReadTimeout is the Agent-side read deadline on one Gateway tunnel. A
// zeroed snapshot (tests) falls back to the table default; a zero deadline would
// close every tunnel immediately.
func tunnelReadTimeout() time.Duration {
	if d := timeouts.TunnelReadTimeout; d > 0 {
		return d
	}
	return defaultTimeouts().TunnelReadTimeout
}

// executionResumeWaitBudget is the ceiling on one blocking ?wait_ms request.
// It bounds a single HTTP call, not the lifetime of the record being polled.
func executionResumeWaitBudget() time.Duration {
	if d := timeouts.ExecutionResumeWaitMax; d > 0 {
		return d
	}
	return defaultTimeouts().ExecutionResumeWaitMax
}

func mustLoadTimeouts(getenv func(string) string) timeoutDefaults {
	resolved, err := loadTimeouts(getenv)
	if err != nil {
		log.Fatalf("%v", err)
	}
	return resolved
}

// loadTimeouts resolves the whole table from getenv. It reports every invalid
// value instead of stopping at the first one, then checks the ordering
// constraints that used to live only in comments.
func loadTimeouts(getenv func(string) string) (timeoutDefaults, error) {
	resolved := defaultTimeouts()
	var errs []error
	for _, spec := range timeoutSpecs() {
		if spec.Env == "" {
			continue
		}
		raw := strings.TrimSpace(getenv(spec.Env))
		if raw == "" {
			continue
		}
		value, err := parseTimeoutValue(raw, spec.BareSeconds)
		if err != nil {
			errs = append(errs, &timeoutConfigError{
				Env: spec.Env, Value: raw, Reason: err.Error(), Min: spec.Min, Max: spec.Max, Meaning: spec.Meaning,
			})
			continue
		}
		if value < spec.Min || value > spec.Max {
			errs = append(errs, &timeoutConfigError{
				Env: spec.Env, Value: raw, Reason: "is outside the allowed range", Min: spec.Min, Max: spec.Max, Meaning: spec.Meaning,
			})
			continue
		}
		spec.set(&resolved, value)
	}
	if len(errs) > 0 {
		return resolved, errors.Join(errs...)
	}
	if err := validateTimeoutOrder(resolved); err != nil {
		return resolved, err
	}
	return resolved, nil
}

// validateTimeoutOrder asserts the inequalities that keep one budget inside the
// next. Every failure names both constants, because the operator reading the
// startup log has to know which pair they broke.
func validateTimeoutOrder(t timeoutDefaults) error {
	helmDeadline := t.MCPToolTimeout - t.HelmSettleReserve
	mcpDeadline := t.MCPToolTimeout - t.MCPCallSettle

	switch {
	case t.HelmTerminationGrace+t.HelmSettleTimeout > t.HelmSettleReserve:
		return &timeoutConfigError{Relation: fmt.Sprintf(
			"helmSettleReserve (DOOPS_HELM_SETTLE_RESERVE=%s) must be at least helmTerminationGrace (%s) + helmSettleTimeout (DOOPS_HELM_SETTLE_TIMEOUT=%s) = %s, otherwise the SIGTERM grace and the settle step do not fit in the reserve",
			formatTimeoutValue(t.HelmSettleReserve), formatTimeoutValue(t.HelmTerminationGrace), formatTimeoutValue(t.HelmSettleTimeout), formatTimeoutValue(t.HelmTerminationGrace+t.HelmSettleTimeout))}
	case t.HelmMaxWait+t.HelmSettleReserve > helmDeadline:
		return &timeoutConfigError{Relation: fmt.Sprintf(
			"helmMaxWait (DOOPS_HELM_MAX_WAIT=%s) + helmSettleReserve (DOOPS_HELM_SETTLE_RESERVE=%s) = %s must fit inside helmToolDeadline (%s = doagent MCP budget %s - reserve %s), otherwise the settle step is cut off by the tool timeout",
			formatTimeoutValue(t.HelmMaxWait), formatTimeoutValue(t.HelmSettleReserve), formatTimeoutValue(t.HelmMaxWait+t.HelmSettleReserve), formatTimeoutValue(helmDeadline), formatTimeoutValue(t.MCPToolTimeout), formatTimeoutValue(t.HelmSettleReserve))}
	case helmDeadline > mcpDeadline:
		return &timeoutConfigError{Relation: fmt.Sprintf(
			"helmToolDeadline (%s = doagent MCP budget %s - reserve %s) must not exceed mcpToolCallDeadline (%s = %s - %s), otherwise a Helm call outlives the tool budget it runs in",
			formatTimeoutValue(helmDeadline), formatTimeoutValue(t.MCPToolTimeout), formatTimeoutValue(t.HelmSettleReserve), formatTimeoutValue(mcpDeadline), formatTimeoutValue(t.MCPToolTimeout), formatTimeoutValue(t.MCPCallSettle))}
	case mcpDeadline > t.ToolExecutionTimeout:
		return &timeoutConfigError{Relation: fmt.Sprintf(
			"mcpToolCallDeadline (%s) must not exceed DOOPS_TOOL_TIMEOUT (%s), otherwise the Agent stops the tool call before its own MCP budget ends",
			formatTimeoutValue(mcpDeadline), formatTimeoutValue(t.ToolExecutionTimeout))}
	case t.ToolDeadlineMargin >= t.ToolExecutionTimeout || t.ToolDeadlineMargin >= mcpDeadline:
		return &timeoutConfigError{Relation: fmt.Sprintf(
			"toolDeadlineMargin (DOOPS_TOOL_DEADLINE_MARGIN=%s) must be smaller than DOOPS_TOOL_TIMEOUT (%s) and mcpToolCallDeadline (%s), otherwise the Agent deadline is not earlier than the Gateway's",
			formatTimeoutValue(t.ToolDeadlineMargin), formatTimeoutValue(t.ToolExecutionTimeout), formatTimeoutValue(mcpDeadline))}
	case t.ExecutionResumeGrace > t.ExecutionResumeTTL:
		return &timeoutConfigError{Relation: fmt.Sprintf(
			"executionResumeGrace (DOOPS_EXECUTION_RESUME_GRACE=%s) must not exceed executionResumeTTL (DOOPS_EXECUTION_RESUME_TTL=%s), otherwise the Gateway stops waiting before the record it is waiting on has even expired; a longer TTL is the only way to wait longer",
			formatTimeoutValue(t.ExecutionResumeGrace), formatTimeoutValue(t.ExecutionResumeTTL))}
	case t.TunnelReadTimeout < t.AgentLease/3:
		return &timeoutConfigError{Relation: fmt.Sprintf(
			"tunnelReadTimeout (DOOPS_TUNNEL_READ_TIMEOUT=%s) must be at least the heartbeat ping period (DOOPS_AGENT_LEASE=%s / 3 = %s), because the read deadline would otherwise cut a healthy tunnel between two of the Gateway's pings",
			formatTimeoutValue(t.TunnelReadTimeout), formatTimeoutValue(t.AgentLease), formatTimeoutValue(t.AgentLease/3))}
	case t.EdgeConnIDQuarantine < t.EdgeDialTimeout+edgeOpeningAckSlack:
		return &timeoutConfigError{Relation: fmt.Sprintf(
			"edgeConnIDQuarantine (DOOPS_EDGE_CONN_ID_QUARANTINE=%s) must be at least edgeDialTimeout (DOOPS_EDGE_DIAL_TIMEOUT=%s) + edgeOpeningAckSlack (%s) = %s, otherwise a released connection id is handed out again while the old opening handshake for it is still in flight",
			formatTimeoutValue(t.EdgeConnIDQuarantine), formatTimeoutValue(t.EdgeDialTimeout), formatTimeoutValue(edgeOpeningAckSlack), formatTimeoutValue(t.EdgeDialTimeout+edgeOpeningAckSlack))}
	case t.EdgeWSReadTimeout < agentWSPingInterval:
		return &timeoutConfigError{Relation: fmt.Sprintf(
			"edgeWSReadTimeout (DOOPS_EDGE_WS_READ_TIMEOUT=%s) must be at least the Edge ping interval (agentWSPingInterval = %s), otherwise a healthy Edge session is cut between two pings",
			formatTimeoutValue(t.EdgeWSReadTimeout), formatTimeoutValue(agentWSPingInterval))}
	case t.ExecutionResumeWaitMax > t.ExecutionResumeGrace:
		return &timeoutConfigError{Relation: fmt.Sprintf(
			"executionResumeWaitMax (DOOPS_EXECUTION_RESUME_WAIT_MAX=%s) must not exceed executionResumeGrace (DOOPS_EXECUTION_RESUME_GRACE=%s), otherwise one ?wait_ms request blocks longer than the record it waits on keeps waiting for a reconnecting Agent; the client would sit past the point where the answer is already decided",
			formatTimeoutValue(t.ExecutionResumeWaitMax), formatTimeoutValue(t.ExecutionResumeGrace))}
	case t.AgentHeartbeatFlush > t.AgentLease/3:
		return &timeoutConfigError{Relation: fmt.Sprintf(
			"agentHeartbeatFlush (DOOPS_AGENT_HEARTBEAT_FLUSH=%s) must not exceed a third of the Agent lease (DOOPS_AGENT_LEASE=%s), because the heartbeat ping period is lease/3 and a slower flush would lose a touch between pings",
			formatTimeoutValue(t.AgentHeartbeatFlush), formatTimeoutValue(t.AgentLease))}
	}
	return nil
}

// timeoutConfigError is a startup failure with the variable, the value given,
// the legal range and what the value bounds. Silence is not an option here: a
// wrong value that quietly falls back to the default is exactly the failure mode
// this table exists to remove.
type timeoutConfigError struct {
	Env      string
	Value    string
	Reason   string
	Min      time.Duration
	Max      time.Duration
	Meaning  string
	Relation string
}

func (e *timeoutConfigError) Error() string {
	if e.Relation != "" {
		return "gateway_timeout_config_invalid: " + e.Relation
	}
	return fmt.Sprintf("gateway_timeout_config_invalid: %s=%q %s; allowed range %s..%s (%s)",
		e.Env, e.Value, e.Reason, formatTimeoutValue(e.Min), formatTimeoutValue(e.Max), e.Meaning)
}

// parseTimeoutValue accepts a Go duration, plus, for the variables that were
// always read through positiveEnvDuration, a bare number of seconds.
func parseTimeoutValue(raw string, bareSeconds bool) (time.Duration, error) {
	if d, err := time.ParseDuration(raw); err == nil {
		return d, nil
	}
	if !bareSeconds {
		return 0, fmt.Errorf("is not a Go duration such as 30m or 90s")
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("is not a Go duration such as 30m or 90s, nor a whole number of seconds")
	}
	return time.Duration(seconds) * time.Second, nil
}

// formatTimeoutValue renders a duration the way the documentation writes it:
// 30m, 90s, 24h, 29m30s. Only the error text and the doc comparison use it.
func formatTimeoutValue(d time.Duration) string {
	switch {
	case d == 0:
		return "0s"
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	case d%time.Second == 0:
		return fmt.Sprintf("%ds", int64(d/time.Second))
	default:
		return d.String()
	}
}
