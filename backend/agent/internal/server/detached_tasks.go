package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/user/doops/agent/api"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	detachedBackgroundProtocol = "detachedBackground"
	detachedBackgroundSchema   = "doops.sh/detached-background/v1"
	// Detached entries stay readable after their terminal state for lazy eviction.
	detachedTaskRetention = 24 * time.Hour
	detachedOutputLimit   = 64 * 1024

	defaultDetachedTaskTimeout = 24 * time.Hour
)

func detachedBackgroundCapability(maxDuration time.Duration) map[string]interface{} {
	return map[string]interface{}{
		"schemaVersion": detachedBackgroundSchema,
		"maxDuration":   maxDuration.String(),
	}
}

// isDetachedToolCallRequest is a cheap pre-dispatch peek used to pick the
// detached call lifecycle. The authoritative validation still happens in
// detachedArg inside handleGatewayToolCall.
func isDetachedToolCallRequest(raw json.RawMessage) bool {
	var params api.ToolCallParams
	if err := json.Unmarshal(raw, &params); err != nil || params.Name != "doops_bg" {
		return false
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(params.Arguments, &args); err != nil {
		return false
	}
	var detach bool
	if err := json.Unmarshal(args["detach"], &detach); err != nil {
		return false
	}
	return detach
}

// detachedArg reads arguments.detach: it must be a JSON boolean, may only be
// set on doops_bg, and is stripped before the Agent sees the arguments.
func detachedArg(tool string, args json.RawMessage) (bool, json.RawMessage, error) {
	var parsed map[string]json.RawMessage
	if len(args) == 0 || json.Unmarshal(args, &parsed) != nil || parsed == nil {
		if tool != "doops_bg" {
			return false, args, nil
		}
		return false, args, nil
	}
	raw, present := parsed["detach"]
	if !present {
		return false, args, nil
	}
	var detach bool
	if err := json.Unmarshal(raw, &detach); err != nil {
		return false, args, fmt.Errorf("detach must be a boolean")
	}
	if tool != "doops_bg" {
		return false, args, fmt.Errorf("detach is only supported by doops_bg")
	}
	delete(parsed, "detach")
	stripped, err := json.Marshal(parsed)
	if err != nil {
		return false, args, err
	}
	return detach, stripped, nil
}

type detachedTask struct {
	AuditID         int64
	OperationID     string
	UserID          string
	Cluster         string
	Instance        string
	Session         string
	CommandSummary  string
	Status          string
	StartedAt       time.Time
	FinishedAt      time.Time
	TaskID          string
	CancelRequested bool
	cancel          context.CancelFunc
	output          string
}

type detachedTaskView struct {
	AuditID         int64  `json:"audit_id"`
	OperationID     string `json:"operation_id"`
	Cluster         string `json:"cluster"`
	Instance        string `json:"instance"`
	Session         string `json:"session"`
	CommandSummary  string `json:"command_summary"`
	Status          string `json:"status"`
	StartedAt       string `json:"started_at"`
	FinishedAt      string `json:"finished_at,omitempty"`
	TaskID          string `json:"task_id,omitempty"`
	CancelRequested bool   `json:"cancel_requested"`
	Output          string `json:"output,omitempty"`
}

func (t *detachedTask) view(withOutput bool) detachedTaskView {
	v := detachedTaskView{
		AuditID:         t.AuditID,
		OperationID:     t.OperationID,
		Cluster:         t.Cluster,
		Instance:        t.Instance,
		Session:         t.Session,
		CommandSummary:  t.CommandSummary,
		Status:          t.Status,
		StartedAt:       t.StartedAt.UTC().Format(time.RFC3339),
		TaskID:          t.TaskID,
		CancelRequested: t.CancelRequested,
	}
	if !t.FinishedAt.IsZero() {
		v.FinishedAt = t.FinishedAt.UTC().Format(time.RFC3339)
	}
	if withOutput {
		v.Output = t.output
	}
	return v
}

// The registry mirrors metadata and bounded output to SQLite so finished entries
// survive Gateway restarts for detachedTaskRetention; running entries interrupted
// by a Gateway restart recover deterministically as outcome_unknown.
type detachedTaskRegistry struct {
	store *GatewayStore
	mu    sync.Mutex
	tasks map[int64]*detachedTask
}

func newDetachedTaskRegistry(store *GatewayStore) *detachedTaskRegistry {
	r := &detachedTaskRegistry{store: store, tasks: make(map[int64]*detachedTask)}
	if store != nil {
		_ = store.recoverAndPruneDetachedTasks(time.Now().UTC())
		if loaded, err := store.loadDetachedTasks(); err == nil {
			for i := range loaded {
				cp := loaded[i]
				r.tasks[cp.AuditID] = &cp
			}
		}
	}
	return r
}

var detachedTaskIDLine = regexp.MustCompile(`(?m)^TaskID: (\S+)`)

func (r *detachedTaskRegistry) register(task *detachedTask) {
	r.mu.Lock()
	r.tasks[task.AuditID] = task
	snapshot := *task
	r.mu.Unlock()
	if r.store != nil {
		_ = r.store.upsertDetachedTask(snapshot)
	}
}

func (r *detachedTaskRegistry) appendOutput(auditID int64, chunk string) {
	r.mu.Lock()
	task := r.tasks[auditID]
	if task == nil {
		r.mu.Unlock()
		return
	}
	if task.TaskID == "" {
		if m := detachedTaskIDLine.FindStringSubmatch(chunk); m != nil {
			task.TaskID = m[1]
		}
	}
	task.output += sanitizeSecretText(chunk)
	if len(task.output) > detachedOutputLimit {
		task.output = task.output[len(task.output)-detachedOutputLimit:]
	}
	taskID, out := task.TaskID, task.output
	r.mu.Unlock()
	if r.store != nil {
		_ = r.store.updateDetachedTaskOutput(auditID, taskID, out)
	}
}

func (r *detachedTaskRegistry) finish(auditID int64, status string) {
	var finished time.Time
	updated := false
	r.mu.Lock()
	if task := r.tasks[auditID]; task != nil && task.Status == "running" {
		task.Status = status
		task.FinishedAt = time.Now().UTC()
		finished = task.FinishedAt
		updated = true
	}
	r.mu.Unlock()
	if updated && r.store != nil {
		_ = r.store.finishDetachedTask(auditID, status, finished)
	}
}

func (r *detachedTaskRegistry) evictLocked(now time.Time) {
	var evicted []int64
	for id, task := range r.tasks {
		if task.Status != "running" && now.Sub(task.FinishedAt) > detachedTaskRetention {
			delete(r.tasks, id)
			evicted = append(evicted, id)
		}
	}
	if r.store != nil && len(evicted) > 0 {
		for _, id := range evicted {
			_, _ = r.store.db.Exec(`DELETE FROM detached_tasks WHERE audit_id = ?`, id)
		}
	}
}

// get returns the task only when it belongs to userID.
func (r *detachedTaskRegistry) get(auditID int64, userID string) *detachedTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked(time.Now())
	task := r.tasks[auditID]
	if task == nil || task.UserID != userID {
		return nil
	}
	return task
}

func (r *detachedTaskRegistry) list(userID, cluster, instance string) []detachedTaskView {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictLocked(time.Now())
	out := make([]detachedTaskView, 0, len(r.tasks))
	for _, task := range r.tasks {
		if task.UserID != userID {
			continue
		}
		if cluster != "" && task.Cluster != cluster {
			continue
		}
		if instance != "" && task.Instance != instance {
			continue
		}
		out = append(out, task.view(false))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AuditID > out[j].AuditID })
	return out
}

func (r *detachedTaskRegistry) requestCancel(task *detachedTask) bool {
	r.mu.Lock()
	if task.Status != "running" {
		r.mu.Unlock()
		return false
	}
	task.CancelRequested = true
	cancelFn := task.cancel
	auditID := task.AuditID
	r.mu.Unlock()
	if r.store != nil {
		_, _ = r.store.db.Exec(`UPDATE detached_tasks SET cancel_requested = 1 WHERE audit_id = ?`, auditID)
	}
	if cancelFn != nil {
		cancelFn()
	}
	return true
}

func (s *GatewayStore) upsertDetachedTask(task detachedTask) error {
	if s == nil || s.db == nil {
		return nil
	}
	finishedRaw := ""
	var finishedMs int64
	if !task.FinishedAt.IsZero() {
		finishedRaw = formatTime(task.FinishedAt.UTC())
		finishedMs = task.FinishedAt.UTC().UnixMilli()
	}
	cancelInt := 0
	if task.CancelRequested {
		cancelInt = 1
	}
	_, err := s.db.Exec(`INSERT INTO detached_tasks
		(audit_id, operation_id, user_id, cluster, instance, session, command_summary, status,
		 started_at, finished_at, finished_at_ms, task_id, cancel_requested, output)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(audit_id) DO UPDATE SET
			status = excluded.status,
			finished_at = excluded.finished_at,
			finished_at_ms = excluded.finished_at_ms,
			task_id = excluded.task_id,
			cancel_requested = excluded.cancel_requested,
			output = excluded.output`,
		task.AuditID, task.OperationID, task.UserID, task.Cluster, task.Instance, task.Session,
		task.CommandSummary, task.Status, formatTime(task.StartedAt.UTC()),
		finishedRaw, finishedMs, task.TaskID, cancelInt, sanitizeSecretText(task.output))
	return err
}

func (s *GatewayStore) updateDetachedTaskOutput(auditID int64, taskID, output string) error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`UPDATE detached_tasks SET task_id = ?, output = ? WHERE audit_id = ?`,
		taskID, output, auditID)
	return err
}

func (s *GatewayStore) finishDetachedTask(auditID int64, status string, finishedAt time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	utc := finishedAt.UTC()
	_, err := s.db.Exec(`UPDATE detached_tasks SET status = ?, finished_at = ?, finished_at_ms = ?
		WHERE audit_id = ? AND status = 'running'`,
		status, formatTime(utc), utc.UnixMilli(), auditID)
	return err
}

func (s *GatewayStore) recoverAndPruneDetachedTasks(now time.Time) error {
	if s == nil || s.db == nil {
		return nil
	}
	utc := now.UTC()
	if _, err := s.db.Exec(`UPDATE detached_tasks
		SET status = 'outcome_unknown', finished_at = ?, finished_at_ms = ?
		WHERE status = 'running'`,
		formatTime(utc), utc.UnixMilli()); err != nil {
		return err
	}
	cutoffMs := utc.Add(-detachedTaskRetention).UnixMilli()
	_, err := s.db.Exec(`DELETE FROM detached_tasks WHERE status != 'running' AND finished_at_ms > 0 AND finished_at_ms < ?`, cutoffMs)
	return err
}

func (s *GatewayStore) loadDetachedTasks() ([]detachedTask, error) {
	rows, err := s.db.Query(`SELECT audit_id, operation_id, user_id, cluster, instance, session,
		command_summary, status, started_at, finished_at, task_id, cancel_requested, output
		FROM detached_tasks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []detachedTask
	for rows.Next() {
		var t detachedTask
		var startedRaw, finishedRaw string
		var cancelInt int
		if err := rows.Scan(&t.AuditID, &t.OperationID, &t.UserID, &t.Cluster, &t.Instance, &t.Session,
			&t.CommandSummary, &t.Status, &startedRaw, &finishedRaw, &t.TaskID, &cancelInt, &t.output); err != nil {
			return nil, err
		}
		t.StartedAt, _ = parseTime(startedRaw)
		if finishedRaw != "" {
			t.FinishedAt, _ = parseTime(finishedRaw)
		}
		t.CancelRequested = cancelInt != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// visibleTask applies the owner + still-granted rule without revealing
// whether the task exists to anyone else.
func (h *GatewayHub) visibleDetachedTask(auditID int64, auth TokenAuth) *detachedTask {
	task := h.client.detached.get(auditID, auth.UserID)
	if task == nil {
		return nil
	}
	if !h.store.UserCanUseTarget(auth.UserID, task.Cluster, task.Instance) {
		return nil
	}
	return task
}

func (h *GatewayHub) HandleBackgroundTasks(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	instance := strings.TrimSpace(r.URL.Query().Get("instance"))
	tasks := h.client.detached.list(auth.UserID, cluster, instance)
	visible := tasks[:0]
	for _, view := range tasks {
		if h.store.UserCanUseTarget(auth.UserID, view.Cluster, view.Instance) {
			visible = append(visible, view)
		}
	}
	writeJSONHTTP(w, map[string]interface{}{"tasks": visible})
}

func (h *GatewayHub) HandleBackgroundTask(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v1/background-tasks/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	auditID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || auditID <= 0 {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		task := h.visibleDetachedTask(auditID, auth)
		if task == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"background_task_not_found"}`))
			return
		}
		h.client.detached.mu.Lock()
		view := task.view(true)
		h.client.detached.mu.Unlock()
		writeJSONHTTP(w, view)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		task := h.visibleDetachedTask(auditID, auth)
		if task == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"background_task_not_found"}`))
			return
		}
		if !h.client.detached.requestCancel(task) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"background_task_finished"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprintf(w, `{"audit_id":%d,"cancel_requested":true}`, auditID)
		return
	}
	http.Error(w, "Not Found", http.StatusNotFound)
}
