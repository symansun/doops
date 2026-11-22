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

// The registry is in-memory: a Gateway restart drops every entry, and an Agent
// tunnel loss still ends the task under the existing outcome rules. Finished
// entries are evicted lazily after detachedTaskRetention.
type detachedTaskRegistry struct {
	mu    sync.Mutex
	tasks map[int64]*detachedTask
}

func newDetachedTaskRegistry() *detachedTaskRegistry {
	return &detachedTaskRegistry{tasks: make(map[int64]*detachedTask)}
}

var detachedTaskIDLine = regexp.MustCompile(`(?m)^TaskID: (\S+)`)

func (r *detachedTaskRegistry) register(task *detachedTask) {
	r.mu.Lock()
	r.tasks[task.AuditID] = task
	r.mu.Unlock()
}

func (r *detachedTaskRegistry) appendOutput(auditID int64, chunk string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task := r.tasks[auditID]
	if task == nil {
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
}

func (r *detachedTaskRegistry) finish(auditID int64, status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task := r.tasks[auditID]; task != nil && task.Status == "running" {
		task.Status = status
		task.FinishedAt = time.Now().UTC()
	}
}

func (r *detachedTaskRegistry) evictLocked(now time.Time) {
	for id, task := range r.tasks {
		if task.Status != "running" && now.Sub(task.FinishedAt) > detachedTaskRetention {
			delete(r.tasks, id)
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
	defer r.mu.Unlock()
	if task.Status != "running" {
		return false
	}
	task.CancelRequested = true
	if task.cancel != nil {
		task.cancel()
	}
	return true
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
