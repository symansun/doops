package server

import (
	"context"
	"database/sql"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/user/doops/agent/pkg/release"
)

type platformMonitorRangeSpec struct {
	name    string
	window  time.Duration
	buckets int
}

var platformMonitorRanges = map[string]platformMonitorRangeSpec{
	"1h":  {name: "1h", window: time.Hour, buckets: 12},
	"24h": {name: "24h", window: 24 * time.Hour, buckets: 24},
	"7d":  {name: "7d", window: 7 * 24 * time.Hour, buckets: 14},
	"30d": {name: "30d", window: 30 * 24 * time.Hour, buckets: 30},
}

type platformMonitorClusterOption struct {
	Name   string `json:"name"`
	Online int    `json:"online"`
	Total  int    `json:"total"`
}

type platformMonitorVersionCount struct {
	ReleaseCommit string `json:"release_commit"`
	Count         int    `json:"count"`
}

type platformMonitorAgentSummary struct {
	Online               int                           `json:"online"`
	Active               int                           `json:"active"`
	Busy                 int                           `json:"busy"`
	RecentOffline        int                           `json:"recent_offline"`
	History              int                           `json:"history"`
	Archived             int                           `json:"archived"`
	OfflineTotal         int                           `json:"offline_total"`
	EnrolledOnline       int                           `json:"enrolled_online"`
	UnenrolledOnline     int                           `json:"unenrolled_online"`
	FlappingAgents       int                           `json:"flapping_agents"`
	GatewayReleaseCommit string                        `json:"gateway_release_commit,omitempty"`
	DriftedOnline        int                           `json:"drifted_online"`
	ActiveOps            int                           `json:"active_ops"`
	QueuedOps            int                           `json:"queued_ops"`
	ActiveSessions       int                           `json:"active_sessions"`
	ResourceLocks        int                           `json:"resource_locks"`
	Versions             []platformMonitorVersionCount `json:"versions"`
}

type platformMonitorAgent struct {
	Cluster           string   `json:"cluster"`
	Instance          string   `json:"instance"`
	Key               string   `json:"key"`
	Activity          string   `json:"activity"`
	Status            string   `json:"status"`
	Busy              bool     `json:"busy"`
	BusyReason        string   `json:"busy_reason,omitempty"`
	Enrolled          bool     `json:"enrolled"`
	ReleaseCommit     string   `json:"release_commit,omitempty"`
	VersionDrifted    bool     `json:"version_drifted,omitempty"`
	Remote            string   `json:"remote,omitempty"`
	ConnectedAt       string   `json:"connected_at,omitempty"`
	LastSeen          string   `json:"last_seen,omitempty"`
	HeartbeatAgeMs    *int64   `json:"heartbeat_age_ms,omitempty"`
	ActiveOps         int      `json:"active_ops"`
	QueuedOps         int      `json:"queued_ops"`
	Sessions          []string `json:"sessions"`
	Resources         []string `json:"resources"`
	WindowConnects    int      `json:"window_connects"`
	WindowDisconnects int      `json:"window_disconnects"`
	WindowCalls       int      `json:"window_calls"`
	WindowFailed      int      `json:"window_failed"`
	WindowBytesIn     int64    `json:"window_bytes_in"`
	WindowBytesOut    int64    `json:"window_bytes_out"`
	WindowTokens      int64    `json:"window_tokens"`
}

type platformConnectionEventItem struct {
	ID            int64  `json:"id"`
	Cluster       string `json:"cluster"`
	Instance      string `json:"instance"`
	Key           string `json:"key"`
	Generation    uint64 `json:"generation"`
	Event         string `json:"event"`
	ReleaseCommit string `json:"release_commit,omitempty"`
	Remote        string `json:"remote,omitempty"`
	Enrolled      bool   `json:"enrolled"`
	OccurredAt    string `json:"occurred_at"`
}

type platformLoadBucket struct {
	StartMs           int64 `json:"start_ms"`
	EndMs             int64 `json:"end_ms"`
	Samples           int   `json:"samples"`
	MaxOnlineAgents   int   `json:"max_online_agents"`
	MaxBusyAgents     int   `json:"max_busy_agents"`
	MaxActiveOps      int   `json:"max_active_ops"`
	MaxQueuedOps      int   `json:"max_queued_ops"`
	MaxActiveSessions int   `json:"max_active_sessions"`
}

type platformUsageSummary struct {
	Total          int   `json:"total"`
	Running        int   `json:"running"`
	Success        int   `json:"success"`
	Failed         int   `json:"failed"`
	OutcomeUnknown int   `json:"outcome_unknown"`
	AvgDurationMs  int64 `json:"avg_duration_ms"`
	BytesIn        int64 `json:"bytes_in"`
	BytesOut       int64 `json:"bytes_out"`
	InputTokens    int64 `json:"input_tokens"`
	OutputTokens   int64 `json:"output_tokens"`
	TotalTokens    int64 `json:"total_tokens"`
	ActiveUsers    int   `json:"active_users"`
	ActiveTargets  int   `json:"active_targets"`
	ActiveModels   int   `json:"active_models"`
}

type platformUsageBucket struct {
	StartMs     int64 `json:"start_ms"`
	EndMs       int64 `json:"end_ms"`
	Total       int   `json:"total"`
	Success     int   `json:"success"`
	Failed      int   `json:"failed"`
	BytesIn     int64 `json:"bytes_in"`
	BytesOut    int64 `json:"bytes_out"`
	TotalTokens int64 `json:"total_tokens"`
}

type platformActionUsage struct {
	Action        string `json:"action"`
	Total         int    `json:"total"`
	Success       int    `json:"success"`
	Failed        int    `json:"failed"`
	Running       int    `json:"running"`
	AvgDurationMs int64  `json:"avg_duration_ms"`
	BytesIn       int64  `json:"bytes_in"`
	BytesOut      int64  `json:"bytes_out"`
	TotalTokens   int64  `json:"total_tokens"`
}

type platformModelUsage struct {
	Model        string `json:"model"`
	Total        int    `json:"total"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
}

type platformTargetUsage struct {
	Cluster        string `json:"cluster"`
	Instance       string `json:"instance"`
	Key            string `json:"key"`
	Online         bool   `json:"online"`
	Status         string `json:"status"`
	Total          int    `json:"total"`
	Success        int    `json:"success"`
	Failed         int    `json:"failed"`
	OutcomeUnknown int    `json:"outcome_unknown"`
	Running        int    `json:"running"`
	AvgDurationMs  int64  `json:"avg_duration_ms"`
	BytesIn        int64  `json:"bytes_in"`
	BytesOut       int64  `json:"bytes_out"`
	TotalTokens    int64  `json:"total_tokens"`
	LastUsedAt     string `json:"last_used_at,omitempty"`
}

type platformUserUsage struct {
	UserID        string `json:"user_id"`
	UserName      string `json:"user_name"`
	IsAdmin       bool   `json:"is_admin"`
	Total         int    `json:"total"`
	Success       int    `json:"success"`
	Failed        int    `json:"failed"`
	ActiveTargets int    `json:"active_targets"`
	TokenCount    int    `json:"token_count"`
	BytesIn       int64  `json:"bytes_in"`
	BytesOut      int64  `json:"bytes_out"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	TotalTokens   int64  `json:"total_tokens"`
	LastActiveAt  string `json:"last_active_at,omitempty"`
}

type platformAuditItem struct {
	ID             int64  `json:"id"`
	UserID         string `json:"user_id,omitempty"`
	UserName       string `json:"user_name,omitempty"`
	Cluster        string `json:"cluster,omitempty"`
	Instance       string `json:"instance,omitempty"`
	Action         string `json:"action"`
	Session        string `json:"session,omitempty"`
	CommandSummary string `json:"command_summary,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	Model          string `json:"model,omitempty"`
	TotalTokens    int64  `json:"total_tokens,omitempty"`
	BytesIn        int64  `json:"bytes_in"`
	BytesOut       int64  `json:"bytes_out"`
	StartedAt      string `json:"started_at"`
	EndedAt        string `json:"ended_at,omitempty"`
	DurationMs     *int64 `json:"duration_ms,omitempty"`
}

type platformDetachedTaskItem struct {
	AuditID         int64  `json:"audit_id"`
	OperationID     string `json:"operation_id"`
	UserID          string `json:"user_id,omitempty"`
	UserName        string `json:"user_name,omitempty"`
	IsOwn           bool   `json:"is_own"`
	Cluster         string `json:"cluster"`
	Instance        string `json:"instance"`
	Session         string `json:"session"`
	CommandSummary  string `json:"command_summary"`
	Status          string `json:"status"`
	StartedAt       string `json:"started_at"`
	FinishedAt      string `json:"finished_at,omitempty"`
	TaskID          string `json:"task_id,omitempty"`
	CancelRequested bool   `json:"cancel_requested"`
}

type platformMonitorResponse struct {
	Scope            string                         `json:"scope"`
	Range            string                         `json:"range"`
	Cluster          string                         `json:"cluster,omitempty"`
	FromMs           int64                          `json:"from_ms"`
	ToMs             int64                          `json:"to_ms"`
	Clusters         []platformMonitorClusterOption `json:"clusters"`
	AgentSummary     platformMonitorAgentSummary    `json:"agent_summary"`
	Agents           []platformMonitorAgent         `json:"agents"`
	ConnectionEvents []platformConnectionEventItem  `json:"connection_events"`
	LoadTrend        []platformLoadBucket           `json:"load_trend"`
	UsageSummary     platformUsageSummary           `json:"usage_summary"`
	Trend            []platformUsageBucket          `json:"trend"`
	ByAction         []platformActionUsage          `json:"by_action"`
	ByModel          []platformModelUsage           `json:"by_model"`
	ByTarget         []platformTargetUsage          `json:"by_target"`
	ByUser           []platformUserUsage            `json:"by_user"`
	RunningOps       []platformAuditItem            `json:"running_ops"`
	DetachedTasks    []platformDetachedTaskItem     `json:"detached_tasks"`
	RecentIssues     []platformAuditItem            `json:"recent_issues"`
}

func isRunningAuditStatus(status string) bool {
	switch status {
	case "running", "executing", "accepted", "observing":
		return true
	default:
		return false
	}
}

func formatOptionalTime(t time.Time) string {
	if !meaningfulInstanceTime(t) {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func (h *GatewayHub) HandlePlatformMonitor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	rawRange := strings.TrimSpace(r.URL.Query().Get("range"))
	if rawRange == "" {
		rawRange = "24h"
	}
	spec, ok := platformMonitorRanges[rawRange]
	if !ok {
		writeDoopsError(w, http.StatusBadRequest, "", doopsErrorResponse{
			Error:   "invalid_platform_monitor_range",
			Code:    "invalid_platform_monitor_range",
			Message: "时间范围无效，仅支持 1h、24h、7d、30d。",
		})
		return
	}
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	if cluster != "" && !monitorID(cluster) {
		writeDoopsError(w, http.StatusBadRequest, "", doopsErrorResponse{
			Error:   "invalid_platform_monitor_cluster",
			Code:    "invalid_platform_monitor_cluster",
			Message: "集群名称格式无效。",
		})
		return
	}

	resp, err := h.buildPlatformMonitor(r.Context(), auth, spec, cluster, time.Now().UTC())
	if err != nil {
		writeDoopsError(w, http.StatusServiceUnavailable, "", doopsErrorResponse{
			Error:   "platform_monitor_unavailable",
			Code:    "platform_monitor_unavailable",
			Message: "无法读取平台监控数据，请检查 Gateway 数据库状态。",
		})
		return
	}
	writeJSONHTTP(w, resp)
}

func (h *GatewayHub) buildPlatformMonitor(
	ctx context.Context,
	auth TokenAuth,
	spec platformMonitorRangeSpec,
	cluster string,
	now time.Time,
) (platformMonitorResponse, error) {
	isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)
	scope := "self"
	if isAdmin {
		scope = "global"
	}
	since := now.Add(-spec.window)
	fromMs := since.UnixMilli()
	toMs := now.UnixMilli()

	statuses, err := h.store.ListAgentStatus()
	if err != nil {
		return platformMonitorResponse{}, err
	}
	liveByKey := make(map[string]GatewayTarget)
	for _, target := range h.ListTargets() {
		liveByKey[target.Key] = target
	}

	seenKeys := make(map[string]bool, len(statuses)+len(liveByKey))
	mergedStatuses := make([]AgentStatus, 0, len(statuses)+len(liveByKey))
	for _, st := range statuses {
		seenKeys[tunnelKey(st.Cluster, st.Instance)] = true
		mergedStatuses = append(mergedStatuses, st)
	}
	for key, t := range liveByKey {
		if seenKeys[key] {
			continue
		}
		mergedStatuses = append(mergedStatuses, AgentStatus{
			Cluster:       t.Cluster,
			Instance:      t.Instance,
			Remote:        t.Remote,
			ReleaseCommit: t.ReleaseCommit,
			Status:        "online",
			ConnectedAt:   t.ConnectedAt,
			LastSeen:      t.LastSeen,
		})
	}

	clusterMap := make(map[string]*platformMonitorClusterOption)
	visibleTargetKeys := make(map[string]bool)
	allVisibleAgents := make([]platformMonitorAgent, 0, len(mergedStatuses))
	for _, st := range mergedStatuses {
		archived := meaningfulInstanceTime(st.ArchivedAt)
		if !isAdmin {
			if archived || !h.store.UserCanUseTarget(auth.UserID, st.Cluster, st.Instance) {
				continue
			}
		}
		key := tunnelKey(st.Cluster, st.Instance)
		live, online := liveByKey[key]

		if !archived {
			opt := clusterMap[st.Cluster]
			if opt == nil {
				opt = &platformMonitorClusterOption{Name: st.Cluster}
				clusterMap[st.Cluster] = opt
			}
			opt.Total++
			if online {
				opt.Online++
			}
		}

		if cluster != "" && st.Cluster != cluster {
			continue
		}
		visibleTargetKeys[key] = true

		row := platformMonitorAgent{
			Cluster:       st.Cluster,
			Instance:      st.Instance,
			Key:           key,
			Enrolled:      st.TokenID != "",
			ReleaseCommit: st.ReleaseCommit,
			Remote:        st.Remote,
			Sessions:      []string{},
			Resources:     []string{},
		}
		connectedAt := st.ConnectedAt
		lastSeen := st.LastSeen

		if online {
			row.Activity = "online"
			row.Status = live.Status
			if row.Status == "" {
				row.Status = "idle"
			}
			row.Busy = live.Busy
			row.BusyReason = live.BusyReason
			row.Enrolled = live.Enrolled
			if live.ReleaseCommit != "" {
				row.ReleaseCommit = live.ReleaseCommit
			}
			if live.Remote != "" {
				row.Remote = live.Remote
			}
			if meaningfulInstanceTime(live.ConnectedAt) {
				connectedAt = live.ConnectedAt
			}
			if meaningfulInstanceTime(live.LastSeen) {
				lastSeen = live.LastSeen
			}
			row.ActiveOps = live.ActiveOps
			row.QueuedOps = live.QueuedOps
			if len(live.Sessions) > 0 {
				row.Sessions = append([]string(nil), live.Sessions...)
			}
			if len(live.Resources) > 0 {
				row.Resources = append([]string(nil), live.Resources...)
			}
		} else {
			row.Status = "offline"
			switch {
			case archived:
				row.Activity = "archived"
			case agentInstanceActive("offline", lastSeen, now):
				row.Activity = "recent_offline"
			default:
				row.Activity = "history"
			}
		}

		row.ConnectedAt = formatOptionalTime(connectedAt)
		row.LastSeen = formatOptionalTime(lastSeen)
		if meaningfulInstanceTime(lastSeen) {
			age := now.Sub(lastSeen.UTC()).Milliseconds()
			if age < 0 {
				age = 0
			}
			row.HeartbeatAgeMs = &age
		}
		allVisibleAgents = append(allVisibleAgents, row)
	}

	clusters := make([]platformMonitorClusterOption, 0, len(clusterMap))
	for _, opt := range clusterMap {
		clusters = append(clusters, *opt)
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Name < clusters[j].Name })

	sortPlatformAgents(allVisibleAgents)

	var agentSummary platformMonitorAgentSummary
	versionCounts := make(map[string]int)
	for _, a := range allVisibleAgents {
		switch a.Activity {
		case "online":
			agentSummary.Online++
			if a.Busy {
				agentSummary.Busy++
			}
			if a.ActiveOps > 0 || a.QueuedOps > 0 {
				agentSummary.Active++
			}
			if a.Enrolled {
				agentSummary.EnrolledOnline++
			} else {
				agentSummary.UnenrolledOnline++
			}
			agentSummary.ActiveOps += a.ActiveOps
			agentSummary.QueuedOps += a.QueuedOps
			agentSummary.ActiveSessions += len(a.Sessions)
			agentSummary.ResourceLocks += len(a.Resources)
			versionCounts[a.ReleaseCommit]++
		case "recent_offline":
			agentSummary.RecentOffline++
		case "history":
			agentSummary.History++
		case "archived":
			agentSummary.Archived++
		}
	}
	agentSummary.OfflineTotal = agentSummary.RecentOffline + agentSummary.History
	agentSummary.GatewayReleaseCommit, agentSummary.DriftedOnline = applyPlatformAgentVersionDrift(
		allVisibleAgents,
		release.Commit,
	)

	agentSummary.Versions = make([]platformMonitorVersionCount, 0, len(versionCounts))
	for commit, count := range versionCounts {
		agentSummary.Versions = append(agentSummary.Versions, platformMonitorVersionCount{
			ReleaseCommit: commit,
			Count:         count,
		})
	}
	sort.Slice(agentSummary.Versions, func(i, j int) bool {
		if agentSummary.Versions[i].Count != agentSummary.Versions[j].Count {
			return agentSummary.Versions[i].Count > agentSummary.Versions[j].Count
		}
		return agentSummary.Versions[i].ReleaseCommit < agentSummary.Versions[j].ReleaseCommit
	})

	resp := platformMonitorResponse{
		Scope:            scope,
		Range:            spec.name,
		Cluster:          cluster,
		FromMs:           fromMs,
		ToMs:             toMs,
		Clusters:         clusters,
		AgentSummary:     agentSummary,
		Agents:           allVisibleAgents,
		ConnectionEvents: []platformConnectionEventItem{},
		LoadTrend:        []platformLoadBucket{},
		Trend:            makeEmptyBuckets(fromMs, toMs, spec.buckets),
		ByAction:         []platformActionUsage{},
		ByModel:          []platformModelUsage{},
		ByTarget:         []platformTargetUsage{},
		ByUser:           []platformUserUsage{},
		RunningOps:       []platformAuditItem{},
		DetachedTasks:    []platformDetachedTaskItem{},
		RecentIssues:     []platformAuditItem{},
	}

	if err := h.populatePlatformUsage(ctx, &resp, auth, isAdmin, spec.buckets, now, liveByKey, visibleTargetKeys); err != nil {
		return platformMonitorResponse{}, err
	}
	return resp, nil
}

func applyPlatformAgentVersionDrift(agents []platformMonitorAgent, gatewayCommit string) (string, int) {
	baseline := strings.TrimSpace(gatewayCommit)
	if release.ValidCommit(baseline) {
		drifted := 0
		for i := range agents {
			if agents[i].Activity != "online" {
				agents[i].VersionDrifted = false
				continue
			}
			commit := strings.TrimSpace(agents[i].ReleaseCommit)
			isDrifted := commit == "" || commit != baseline
			agents[i].VersionDrifted = isDrifted
			if isDrifted {
				drifted++
			}
		}
		return baseline, drifted
	}

	reportedCounts := make(map[string]int)
	unreported := 0
	for i := range agents {
		agents[i].VersionDrifted = false
		if agents[i].Activity != "online" {
			continue
		}
		commit := strings.TrimSpace(agents[i].ReleaseCommit)
		if commit == "" {
			unreported++
			continue
		}
		reportedCounts[commit]++
	}
	if len(reportedCounts) == 0 && unreported == 0 {
		return "", 0
	}
	if len(reportedCounts) == 1 && unreported == 0 {
		return "", 0
	}

	primaryCommit := ""
	primaryCount := -1
	for commit, count := range reportedCounts {
		if count > primaryCount || (count == primaryCount && commit < primaryCommit) {
			primaryCommit = commit
			primaryCount = count
		}
	}

	drifted := 0
	for i := range agents {
		if agents[i].Activity != "online" {
			continue
		}
		commit := strings.TrimSpace(agents[i].ReleaseCommit)
		if commit == "" || primaryCommit == "" || commit != primaryCommit {
			agents[i].VersionDrifted = true
			drifted++
		}
	}
	return "", drifted
}

func sortPlatformAgents(rows []platformMonitorAgent) {
	rank := func(activity string) int {
		switch activity {
		case "online":
			return 0
		case "recent_offline":
			return 1
		case "history":
			return 2
		default:
			return 3
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		ri, rj := rank(rows[i].Activity), rank(rows[j].Activity)
		if ri != rj {
			return ri < rj
		}
		if rows[i].Activity == "online" {
			loadI := rows[i].ActiveOps + rows[i].QueuedOps
			loadJ := rows[j].ActiveOps + rows[j].QueuedOps
			if rows[i].Busy != rows[j].Busy {
				return rows[i].Busy
			}
			if loadI != loadJ {
				return loadI > loadJ
			}
		} else {
			if rows[i].LastSeen != rows[j].LastSeen {
				return rows[i].LastSeen > rows[j].LastSeen
			}
		}
		return rows[i].Key < rows[j].Key
	})
}

func makeEmptyBuckets(fromMs, toMs int64, count int) []platformUsageBucket {
	out := make([]platformUsageBucket, count)
	span := toMs - fromMs
	for i := 0; i < count; i++ {
		start := fromMs + (span*int64(i))/int64(count)
		end := fromMs + (span*int64(i+1))/int64(count)
		out[i] = platformUsageBucket{StartMs: start, EndMs: end}
	}
	return out
}

func makeEmptyLoadBuckets(fromMs, toMs int64, count int) []platformLoadBucket {
	out := make([]platformLoadBucket, count)
	span := toMs - fromMs
	for i := 0; i < count; i++ {
		start := fromMs + (span*int64(i))/int64(count)
		end := fromMs + (span*int64(i+1))/int64(count)
		out[i] = platformLoadBucket{StartMs: start, EndMs: end}
	}
	return out
}

func (h *GatewayHub) populatePlatformUsage(
	ctx context.Context,
	resp *platformMonitorResponse,
	auth TokenAuth,
	isAdmin bool,
	bucketCount int,
	now time.Time,
	liveByKey map[string]GatewayTarget,
	visibleTargetKeys map[string]bool,
) error {
	tx, err := h.store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	userNames := make(map[string]string)
	adminUsers := make(map[string]bool)
	userRows, err := tx.QueryContext(ctx, `SELECT u.id, u.name,
		EXISTS(SELECT 1 FROM grants g WHERE g.user_id = u.id AND g.cluster = '*' AND g.instance = '*')
		FROM users u`)
	if err != nil {
		return err
	}
	for userRows.Next() {
		var id, name string
		var adminFlag bool
		if err := userRows.Scan(&id, &name, &adminFlag); err != nil {
			userRows.Close()
			return err
		}
		userNames[id] = name
		adminUsers[id] = adminFlag
	}
	if err := userRows.Err(); err != nil {
		userRows.Close()
		return err
	}
	userRows.Close()

	resolveUserName := func(userID string) string {
		if userID == "" {
			return ""
		}
		if name := userNames[userID]; name != "" {
			return name
		}
		return userID
	}

	fromMs := resp.FromMs
	toMs := resp.ToMs
	spanMs := toMs - fromMs
	if spanMs <= 0 {
		spanMs = 1
	}

	// 1. Agent connection events within [fromMs, toMs] (uses idx_agent_conn_events_time).
	connectCounts := make(map[string]int)
	disconnectCounts := make(map[string]int)
	connQuery := `SELECT id, cluster, instance, generation, event, release_commit, remote, enrolled, occurred_at
		FROM agent_connection_events WHERE occurred_at_ms >= ? AND occurred_at_ms <= ?`
	connArgs := []any{fromMs, toMs}
	if resp.Cluster != "" {
		connQuery += " AND cluster = ?"
		connArgs = append(connArgs, resp.Cluster)
	}
	connQuery += " ORDER BY occurred_at_ms DESC, id DESC"
	connRows, err := tx.QueryContext(ctx, connQuery, connArgs...)
	if err != nil {
		return err
	}
	for connRows.Next() {
		var item platformConnectionEventItem
		var enrolledInt int
		var rawTime string
		if err := connRows.Scan(&item.ID, &item.Cluster, &item.Instance, &item.Generation,
			&item.Event, &item.ReleaseCommit, &item.Remote, &enrolledInt, &rawTime); err != nil {
			connRows.Close()
			return err
		}
		item.Key = tunnelKey(item.Cluster, item.Instance)
		if !isAdmin && !visibleTargetKeys[item.Key] {
			continue
		}
		if item.Event == "online" {
			connectCounts[item.Key]++
		} else if item.Event == "offline" {
			disconnectCounts[item.Key]++
		}
		if len(resp.ConnectionEvents) < 30 {
			item.Enrolled = enrolledInt != 0
			if parsed, pErr := parseTime(rawTime); pErr == nil {
				item.OccurredAt = formatOptionalTime(parsed)
			} else {
				item.OccurredAt = rawTime
			}
			resp.ConnectionEvents = append(resp.ConnectionEvents, item)
		}
	}
	if err := connRows.Err(); err != nil {
		connRows.Close()
		return err
	}
	connRows.Close()

	for i := range resp.Agents {
		key := resp.Agents[i].Key
		resp.Agents[i].WindowConnects = connectCounts[key]
		resp.Agents[i].WindowDisconnects = disconnectCounts[key]
		if connectCounts[key] >= 2 || (connectCounts[key] >= 1 && disconnectCounts[key] >= 1) {
			resp.AgentSummary.FlappingAgents++
		}
	}

	// 2. Global load samples (admin global view only so non-admin never infers global fleet load).
	if isAdmin && resp.Cluster == "" && bucketCount > 0 {
		loadBuckets := makeEmptyLoadBuckets(fromMs, toMs, bucketCount)
		loadRows, err := tx.QueryContext(ctx, `SELECT at_ms, online_agents, busy_agents, active_ops, queued_ops, active_sessions
			FROM platform_load_samples WHERE at_ms >= ? AND at_ms <= ? ORDER BY at_ms ASC`, fromMs, toMs)
		if err != nil {
			return err
		}
		for loadRows.Next() {
			var atMs int64
			var on, busy, act, q, sess int
			if err := loadRows.Scan(&atMs, &on, &busy, &act, &q, &sess); err != nil {
				loadRows.Close()
				return err
			}
			idx := int(((atMs - fromMs) * int64(bucketCount)) / spanMs)
			if idx < 0 {
				idx = 0
			} else if idx >= bucketCount {
				idx = bucketCount - 1
			}
			b := &loadBuckets[idx]
			b.Samples++
			if on > b.MaxOnlineAgents {
				b.MaxOnlineAgents = on
			}
			if busy > b.MaxBusyAgents {
				b.MaxBusyAgents = busy
			}
			if act > b.MaxActiveOps {
				b.MaxActiveOps = act
			}
			if q > b.MaxQueuedOps {
				b.MaxQueuedOps = q
			}
			if sess > b.MaxActiveSessions {
				b.MaxActiveSessions = sess
			}
		}
		if err := loadRows.Err(); err != nil {
			loadRows.Close()
			return err
		}
		loadRows.Close()
		resp.LoadTrend = loadBuckets
	}

	// 3. Audit events within [fromMs, toMs] via sargable started_at_ms index (idx_audit_started_ms).
	whereParts := []string{
		"started_at_ms >= ?",
		"started_at_ms <= ?",
		"NOT (action = 'agent:upgrade' AND status = 'observing')",
	}
	args := []any{fromMs, toMs}
	if resp.Cluster != "" {
		whereParts = append(whereParts, "cluster = ?")
		args = append(args, resp.Cluster)
	}
	if !isAdmin {
		whereParts = append(whereParts, "(user_id = ? OR credential_id IN (SELECT id FROM credentials WHERE owner_id = ?))")
		args = append(args, auth.UserID, auth.UserID)
	}
	whereClause := strings.Join(whereParts, " AND ")

	type actionAccum struct {
		platformActionUsage
		durSum   int64
		durCount int64
	}
	type targetAccum struct {
		platformTargetUsage
		durSum   int64
		durCount int64
		lastTime time.Time
	}
	type userAccum struct {
		platformUserUsage
		targets  map[string]bool
		tokens   map[string]bool
		lastTime time.Time
	}

	actionsMap := make(map[string]*actionAccum)
	modelsMap := make(map[string]*platformModelUsage)
	targetsMap := make(map[string]*targetAccum)
	usersMap := make(map[string]*userAccum)
	activeUsersSet := make(map[string]bool)
	activeTargetsSet := make(map[string]bool)
	var totalDurSum, totalDurCount int64

	eventRows, err := tx.QueryContext(ctx, `SELECT user_id, token_id, cluster, instance, action, status,
		bytes_in, bytes_out, model, input_tokens, output_tokens, total_tokens, started_at_ms, started_at, ended_at
		FROM audit_events WHERE `+whereClause, args...)
	if err != nil {
		return err
	}
	for eventRows.Next() {
		var userID, tokenID, evCluster, evInstance, action, status, model, startedRaw, endedRaw string
		var bytesIn, bytesOut, inTok, outTok, totTok, startedMs int64
		if err := eventRows.Scan(&userID, &tokenID, &evCluster, &evInstance, &action, &status,
			&bytesIn, &bytesOut, &model, &inTok, &outTok, &totTok, &startedMs, &startedRaw, &endedRaw); err != nil {
			eventRows.Close()
			return err
		}
		if action == "" {
			action = "unknown"
		}
		if totTok == 0 && (inTok > 0 || outTok > 0) {
			totTok = inTok + outTok
		}
		startedAt, _ := parseTime(startedRaw)
		var durationMs int64
		hasDuration := false
		if endedRaw != "" && !startedAt.IsZero() {
			if endedAt, err := parseTime(endedRaw); err == nil && !endedAt.Before(startedAt) {
				durationMs = endedAt.Sub(startedAt).Milliseconds()
				hasDuration = true
			}
		}

		running := isRunningAuditStatus(status)
		success := status == "success"
		failed := !running && !success

		resp.UsageSummary.Total++
		resp.UsageSummary.BytesIn += bytesIn
		resp.UsageSummary.BytesOut += bytesOut
		resp.UsageSummary.InputTokens += inTok
		resp.UsageSummary.OutputTokens += outTok
		resp.UsageSummary.TotalTokens += totTok
		switch {
		case running:
			resp.UsageSummary.Running++
		case success:
			resp.UsageSummary.Success++
		default:
			resp.UsageSummary.Failed++
			if status == "outcome_unknown" {
				resp.UsageSummary.OutcomeUnknown++
			}
		}
		if hasDuration {
			totalDurSum += durationMs
			totalDurCount++
		}
		if userID != "" {
			activeUsersSet[userID] = true
		}
		if evCluster != "" && evInstance != "" {
			activeTargetsSet[tunnelKey(evCluster, evInstance)] = true
		}

		if model != "" || totTok > 0 {
			modelKey := strings.TrimSpace(model)
			if modelKey == "" {
				modelKey = "未标注模型"
			}
			mItem := modelsMap[modelKey]
			if mItem == nil {
				mItem = &platformModelUsage{Model: modelKey}
				modelsMap[modelKey] = mItem
			}
			mItem.Total++
			mItem.InputTokens += inTok
			mItem.OutputTokens += outTok
			mItem.TotalTokens += totTok
		}

		if startedMs > 0 && bucketCount > 0 {
			idx := int(((startedMs - fromMs) * int64(bucketCount)) / spanMs)
			if idx < 0 {
				idx = 0
			} else if idx >= bucketCount {
				idx = bucketCount - 1
			}
			b := &resp.Trend[idx]
			b.Total++
			b.BytesIn += bytesIn
			b.BytesOut += bytesOut
			b.TotalTokens += totTok
			if success {
				b.Success++
			} else if failed {
				b.Failed++
			}
		}

		act := actionsMap[action]
		if act == nil {
			act = &actionAccum{platformActionUsage: platformActionUsage{Action: action}}
			actionsMap[action] = act
		}
		act.Total++
		act.BytesIn += bytesIn
		act.BytesOut += bytesOut
		act.TotalTokens += totTok
		if running {
			act.Running++
		} else if success {
			act.Success++
		} else {
			act.Failed++
		}
		if hasDuration {
			act.durSum += durationMs
			act.durCount++
		}

		if evCluster != "" && evInstance != "" {
			tKey := tunnelKey(evCluster, evInstance)
			tgt := targetsMap[tKey]
			if tgt == nil {
				live, online := liveByKey[tKey]
				st := "offline"
				if online {
					st = live.Status
					if st == "" {
						st = "idle"
					}
				}
				tgt = &targetAccum{
					platformTargetUsage: platformTargetUsage{
						Cluster:  evCluster,
						Instance: evInstance,
						Key:      tKey,
						Online:   online,
						Status:   st,
					},
				}
				targetsMap[tKey] = tgt
			}
			tgt.Total++
			tgt.BytesIn += bytesIn
			tgt.BytesOut += bytesOut
			tgt.TotalTokens += totTok
			if running {
				tgt.Running++
			} else if success {
				tgt.Success++
			} else {
				tgt.Failed++
				if status == "outcome_unknown" {
					tgt.OutcomeUnknown++
				}
			}
			if hasDuration {
				tgt.durSum += durationMs
				tgt.durCount++
			}
			if startedAt.After(tgt.lastTime) {
				tgt.lastTime = startedAt
			}
		}

		if userID != "" {
			usr := usersMap[userID]
			if usr == nil {
				usr = &userAccum{
					platformUserUsage: platformUserUsage{
						UserID:   userID,
						UserName: resolveUserName(userID),
						IsAdmin:  adminUsers[userID],
					},
					targets: make(map[string]bool),
					tokens:  make(map[string]bool),
				}
				usersMap[userID] = usr
			}
			usr.Total++
			usr.BytesIn += bytesIn
			usr.BytesOut += bytesOut
			usr.InputTokens += inTok
			usr.OutputTokens += outTok
			usr.TotalTokens += totTok
			if success {
				usr.Success++
			} else if failed {
				usr.Failed++
			}
			if evCluster != "" && evInstance != "" {
				usr.targets[tunnelKey(evCluster, evInstance)] = true
			}
			if tokenID != "" {
				usr.tokens[tokenID] = true
			}
			if startedAt.After(usr.lastTime) {
				usr.lastTime = startedAt
			}
		}
	}
	if err := eventRows.Err(); err != nil {
		eventRows.Close()
		return err
	}
	eventRows.Close()

	resp.UsageSummary.ActiveUsers = len(activeUsersSet)
	resp.UsageSummary.ActiveTargets = len(activeTargetsSet)
	resp.UsageSummary.ActiveModels = len(modelsMap)
	if totalDurCount > 0 {
		resp.UsageSummary.AvgDurationMs = int64(math.Round(float64(totalDurSum) / float64(totalDurCount)))
	}

	for i := range resp.Agents {
		if tgt := targetsMap[resp.Agents[i].Key]; tgt != nil {
			resp.Agents[i].WindowCalls = tgt.Total
			resp.Agents[i].WindowFailed = tgt.Failed
			resp.Agents[i].WindowBytesIn = tgt.BytesIn
			resp.Agents[i].WindowBytesOut = tgt.BytesOut
			resp.Agents[i].WindowTokens = tgt.TotalTokens
		}
	}

	resp.ByAction = make([]platformActionUsage, 0, len(actionsMap))
	for _, item := range actionsMap {
		if item.durCount > 0 {
			item.AvgDurationMs = int64(math.Round(float64(item.durSum) / float64(item.durCount)))
		}
		resp.ByAction = append(resp.ByAction, item.platformActionUsage)
	}
	sort.Slice(resp.ByAction, func(i, j int) bool {
		if resp.ByAction[i].Total != resp.ByAction[j].Total {
			return resp.ByAction[i].Total > resp.ByAction[j].Total
		}
		return resp.ByAction[i].Action < resp.ByAction[j].Action
	})

	resp.ByModel = make([]platformModelUsage, 0, len(modelsMap))
	for _, item := range modelsMap {
		resp.ByModel = append(resp.ByModel, *item)
	}
	sort.Slice(resp.ByModel, func(i, j int) bool {
		if resp.ByModel[i].TotalTokens != resp.ByModel[j].TotalTokens {
			return resp.ByModel[i].TotalTokens > resp.ByModel[j].TotalTokens
		}
		if resp.ByModel[i].Total != resp.ByModel[j].Total {
			return resp.ByModel[i].Total > resp.ByModel[j].Total
		}
		return resp.ByModel[i].Model < resp.ByModel[j].Model
	})
	if len(resp.ByModel) > 20 {
		resp.ByModel = resp.ByModel[:20]
	}

	resp.ByTarget = make([]platformTargetUsage, 0, len(targetsMap))
	for _, item := range targetsMap {
		if item.durCount > 0 {
			item.AvgDurationMs = int64(math.Round(float64(item.durSum) / float64(item.durCount)))
		}
		item.LastUsedAt = formatOptionalTime(item.lastTime)
		resp.ByTarget = append(resp.ByTarget, item.platformTargetUsage)
	}
	sort.Slice(resp.ByTarget, func(i, j int) bool {
		if resp.ByTarget[i].Total != resp.ByTarget[j].Total {
			return resp.ByTarget[i].Total > resp.ByTarget[j].Total
		}
		if resp.ByTarget[i].Failed != resp.ByTarget[j].Failed {
			return resp.ByTarget[i].Failed > resp.ByTarget[j].Failed
		}
		return resp.ByTarget[i].Key < resp.ByTarget[j].Key
	})
	if len(resp.ByTarget) > 20 {
		resp.ByTarget = resp.ByTarget[:20]
	}

	resp.ByUser = make([]platformUserUsage, 0, len(usersMap))
	for _, item := range usersMap {
		item.ActiveTargets = len(item.targets)
		item.TokenCount = len(item.tokens)
		item.LastActiveAt = formatOptionalTime(item.lastTime)
		resp.ByUser = append(resp.ByUser, item.platformUserUsage)
	}
	sort.Slice(resp.ByUser, func(i, j int) bool {
		if resp.ByUser[i].Total != resp.ByUser[j].Total {
			return resp.ByUser[i].Total > resp.ByUser[j].Total
		}
		return resp.ByUser[i].UserName < resp.ByUser[j].UserName
	})
	if len(resp.ByUser) > 20 {
		resp.ByUser = resp.ByUser[:20]
	}

	runWhere := []string{
		"status IN ('running', 'executing', 'accepted', 'observing')",
		"NOT (action = 'agent:upgrade' AND status = 'observing')",
	}
	runArgs := []any{}
	if resp.Cluster != "" {
		runWhere = append(runWhere, "cluster = ?")
		runArgs = append(runArgs, resp.Cluster)
	}
	if !isAdmin {
		runWhere = append(runWhere, "(user_id = ? OR credential_id IN (SELECT id FROM credentials WHERE owner_id = ?))")
		runArgs = append(runArgs, auth.UserID, auth.UserID)
	}
	runningOps, err := queryPlatformAuditItems(ctx, tx, strings.Join(runWhere, " AND "), runArgs, 30, now, resolveUserName)
	if err != nil {
		return err
	}
	resp.RunningOps = runningOps

	issueWhere := append([]string(nil), whereParts...)
	issueWhere = append(issueWhere, "status NOT IN ('success', 'running', 'executing', 'accepted', 'observing')")
	issueArgs := append([]any(nil), args...)
	recentIssues, err := queryPlatformAuditItems(ctx, tx, strings.Join(issueWhere, " AND "), issueArgs, 30, now, resolveUserName)
	if err != nil {
		return err
	}
	resp.RecentIssues = recentIssues
	if err := tx.Commit(); err != nil {
		return err
	}

	if h.client != nil && h.client.detached != nil {
		resp.DetachedTasks = h.listVisibleDetachedTasks(auth.UserID, isAdmin, resp.Cluster, resolveUserName)
	}
	return nil
}

func queryPlatformAuditItems(
	ctx context.Context,
	tx *sql.Tx,
	whereClause string,
	args []any,
	limit int,
	now time.Time,
	resolveUserName func(string) string,
) ([]platformAuditItem, error) {
	queryArgs := append(append([]any(nil), args...), limit)
	rows, err := tx.QueryContext(ctx, `SELECT id, user_id, cluster, instance, action, session,
		command_summary, status, error, correlation_id, model, total_tokens, bytes_in, bytes_out, started_at, ended_at
		FROM audit_events WHERE `+whereClause+` ORDER BY id DESC LIMIT ?`, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []platformAuditItem{}
	for rows.Next() {
		var item platformAuditItem
		var startedRaw, endedRaw string
		if err := rows.Scan(&item.ID, &item.UserID, &item.Cluster, &item.Instance, &item.Action, &item.Session,
			&item.CommandSummary, &item.Status, &item.Error, &item.CorrelationID, &item.Model, &item.TotalTokens,
			&item.BytesIn, &item.BytesOut, &startedRaw, &endedRaw); err != nil {
			return nil, err
		}
		item.UserName = resolveUserName(item.UserID)
		startedAt, _ := parseTime(startedRaw)
		item.StartedAt = formatOptionalTime(startedAt)
		if endedRaw != "" {
			if endedAt, err := parseTime(endedRaw); err == nil {
				item.EndedAt = formatOptionalTime(endedAt)
				if !startedAt.IsZero() && !endedAt.Before(startedAt) {
					d := endedAt.Sub(startedAt).Milliseconds()
					item.DurationMs = &d
				}
			}
		} else if !startedAt.IsZero() && isRunningAuditStatus(item.Status) {
			d := now.Sub(startedAt).Milliseconds()
			if d < 0 {
				d = 0
			}
			item.DurationMs = &d
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (h *GatewayHub) listVisibleDetachedTasks(
	userID string,
	isAdmin bool,
	cluster string,
	resolveUserName func(string) string,
) []platformDetachedTaskItem {
	r := h.client.detached
	r.mu.Lock()
	r.evictLocked(time.Now())
	raw := make([]detachedTask, 0, len(r.tasks))
	for _, task := range r.tasks {
		if !isAdmin && task.UserID != userID {
			continue
		}
		if cluster != "" && task.Cluster != cluster {
			continue
		}
		copyTask := *task
		copyTask.output = ""
		raw = append(raw, copyTask)
	}
	r.mu.Unlock()

	out := make([]platformDetachedTaskItem, 0, len(raw))
	for _, task := range raw {
		if !isAdmin && !h.store.UserCanUseTarget(userID, task.Cluster, task.Instance) {
			continue
		}
		item := platformDetachedTaskItem{
			AuditID:         task.AuditID,
			OperationID:     task.OperationID,
			UserID:          task.UserID,
			UserName:        resolveUserName(task.UserID),
			IsOwn:           task.UserID == userID,
			Cluster:         task.Cluster,
			Instance:        task.Instance,
			Session:         task.Session,
			CommandSummary:  task.CommandSummary,
			Status:          task.Status,
			StartedAt:       task.StartedAt.UTC().Format(time.RFC3339),
			TaskID:          task.TaskID,
			CancelRequested: task.CancelRequested,
		}
		if !task.FinishedAt.IsZero() {
			item.FinishedAt = task.FinishedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AuditID > out[j].AuditID })
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}
