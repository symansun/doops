package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/doops/agent/api"
	"github.com/user/doops/agent/pkg/release"
)

type GatewayHubOptions struct {
	AgentLease              time.Duration
	OperationTimeout        time.Duration
	TargetQueueTimeout      time.Duration
	TargetReconnectGrace    time.Duration
	LoginTokenTTL           time.Duration
	MaxConcurrentOperations int
	MaxConcurrentPerUser    int
	MaxQueuedPerTarget      int
}

type GatewayHub struct {
	store        *GatewayStore
	opts         GatewayHubOptions
	registry     *AgentRegistry
	registration *AgentRegistrationHandler
	client       *ClientService
	edge         *EdgeRegistry

	scheduler               *Scheduler
	credentialMutationLocks sync.Map
}

type contextMutex struct {
	ready chan struct{}
}

func newContextMutex() contextMutex {
	ready := make(chan struct{}, 1)
	ready <- struct{}{}
	return contextMutex{ready: ready}
}

func (m *contextMutex) Acquire(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.ready:
		return nil
	}
}

func (m *contextMutex) Release() {
	m.ready <- struct{}{}
}

// AttachScheduler 注入定时巡检调度器，供 /v1/admin/jobs run-now 等接口使用。
func (h *GatewayHub) AttachScheduler(sched *Scheduler) {
	h.scheduler = sched
}

var (
	errGatewayClientDisconnected = errors.New("gateway client disconnected")
	errGatewayAgentDisconnected  = errors.New("agent disconnected")
	errGatewayMessageQueueClosed = errors.New("gateway message queue closed")
	errGatewayMessageQueueFull   = errors.New("gateway message queue full")
)

const (
	gatewayMessageBudgetMaxBytes       = 128 << 20
	gatewayMessageQueueMaxBytes        = 64 << 20
	gatewayAgentTouchTimeout           = 2 * time.Second
	gatewayReadProgressAckInterval     = 5 * time.Second
	gatewayReadProgressAckWriteTimeout = 3 * time.Second
	credentialPlanProtocolV2           = "doops.sh/credential-plan/v2"
	credentialPlanDeclarationV3        = "doops.sh/v3"
)

func credentialPlanCapability() map[string]interface{} {
	return map[string]interface{}{
		"schemaVersion":            credentialPlanProtocolV2,
		"declarationSchemaVersion": credentialPlanDeclarationV3,
		"sourceRevision":           true,
		"targetBinding":            true,
		"sessionBinding":           true,
		"buildDefinition":          true,
	}
}

func credentialPlanCapabilityCompatible(value interface{}) bool {
	return capabilityMatches(value, credentialPlanCapability())
}

type GatewayActiveOperation struct {
	ID                string        `json:"id"`
	AuditID           int64         `json:"audit_id,omitempty"`
	State             string        `json:"state"`
	Resource          string        `json:"resource,omitempty"`
	RequestedResource string        `json:"requested_resource,omitempty"`
	EffectiveLock     string        `json:"effective_lock,omitempty"`
	BlockedBy         []string      `json:"blocked_by,omitempty"`
	UserID            string        `json:"user_id"`
	TokenID           string        `json:"token_id,omitempty"`
	Cluster           string        `json:"cluster"`
	Instance          string        `json:"instance"`
	Action            GatewayAction `json:"action"`
	Session           string        `json:"session,omitempty"`
	CommandSummary    string        `json:"command_summary,omitempty"`
	Kind              string        `json:"kind"`
	StartedAt         time.Time     `json:"started_at"`
	AgeSeconds        int64         `json:"age_seconds"`
}

type gatewayActiveOperation struct {
	GatewayActiveOperation
	cancel context.CancelFunc
	agent  *GatewayAgent
	lease  *operationLease
	finish *AuditFinish
}

// operationLease binds a resource release to the operation that acquired it.
// The release is idempotent so normal completion and operation finalization
// can safely converge on one terminal cleanup path.
type operationLease struct {
	once    sync.Once
	release func()
}

func newOperationLease(release func()) *operationLease {
	return &operationLease{release: release}
}

func (l *operationLease) Release() {
	if l == nil || l.release == nil {
		return
	}
	l.once.Do(l.release)
}

type GatewayAgent struct {
	Cluster                string    `json:"cluster"`
	Instance               string    `json:"instance"`
	Key                    string    `json:"key"`
	Remote                 string    `json:"remote"`
	TokenID                string    `json:"token_id,omitempty"`
	Generation             uint64    `json:"generation"`
	RuntimeID              string    `json:"runtime_id,omitempty"`
	ReleaseCommit          string    `json:"release_commit,omitempty"`
	CredentialPlanProtocol string    `json:"credential_plan_protocol,omitempty"`
	StructuredSSH          bool      `json:"structured_ssh,omitempty"`
	ExecutionLifecycle     bool      `json:"execution_lifecycle,omitempty"`
	ConnectedAt            time.Time `json:"connected_at"`
	LastSeen               time.Time `json:"last_seen"`
	HeartbeatAt            time.Time `json:"heartbeat_at,omitempty"`
	Busy                   bool      `json:"busy"`
	Status                 string    `json:"status,omitempty"`
	BusyReason             string    `json:"busy_reason,omitempty"`
	ActiveOps              int       `json:"active_ops,omitempty"`
	QueuedOps              int       `json:"queued_ops,omitempty"`

	stateMu          sync.RWMutex
	conn             *websocket.Conn
	writeMu          sync.Mutex
	dataWriteState   websocketDataWriteState
	heartbeatPayload string
	lastProgressAck  time.Time
	queueMu          sync.Mutex
	queued           int
	opsMu            sync.Mutex
	stateChanged     chan struct{}
	draining         bool
	writers          int
	sessionWriters   map[string]int
	readers          int
	resources        map[string]*agentResourceSlot
	pendingMu        sync.Mutex
	pending          map[int64]*gatewayMessageQueue
	activeByRequest  map[int64]*gatewayMessageQueue
	activeSessions   map[string]int
	messageBudget    *gatewayMessageBudget
	closed           chan struct{}
	reqID            int64
}

type agentResourceSlot struct {
	slot    chan struct{}
	queued  int
	session string
}

type GatewayTarget struct {
	Cluster       string    `json:"cluster"`
	Instance      string    `json:"instance"`
	Key           string    `json:"key"`
	Remote        string    `json:"remote"`
	TokenID       string    `json:"token_id,omitempty"`
	ReleaseCommit string    `json:"release_commit,omitempty"`
	ConnectedAt   time.Time `json:"connected_at"`
	LastSeen      time.Time `json:"last_seen"`
	Busy          bool      `json:"busy"`
	Status        string    `json:"status"`
	BusyReason    string    `json:"busy_reason,omitempty"`
	ActiveOps     int       `json:"active_ops"`
	QueuedOps     int       `json:"queued_ops"`
	Resources     []string  `json:"resources,omitempty"`
	Sessions      []string  `json:"sessions,omitempty"`
}

type gatewayWSMessage struct {
	Raw    []byte
	Parsed map[string]interface{}
}

type gatewayQueuedMessage struct {
	message gatewayWSMessage
	size    int64
}

type gatewayMessageBudget struct {
	mu       sync.Mutex
	used     int64
	maxBytes int64
}

type gatewayMessageQueue struct {
	mu              sync.Mutex
	items           []gatewayQueuedMessage
	queuedBytes     int64
	maxBytes        int64
	budget          *gatewayMessageBudget
	notify          chan struct{}
	requestID       int64
	cancelMethod    string
	cancelOnce      sync.Once
	cancelDone      chan struct{}
	terminal        chan struct{}
	terminalOnce    sync.Once
	terminalErr     error
	terminalMessage gatewayWSMessage
	closed          bool
	err             error
}

func newGatewayMessageBudget(maxBytes int64) *gatewayMessageBudget {
	return &gatewayMessageBudget{maxBytes: maxBytes}
}

func (b *gatewayMessageBudget) reserve(size int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if size <= 0 || b.used+size > b.maxBytes {
		return false
	}
	b.used += size
	return true
}

func (b *gatewayMessageBudget) release(size int64) {
	if size <= 0 {
		return
	}
	b.mu.Lock()
	b.used -= size
	b.mu.Unlock()
}

func newGatewayMessageQueue(budget *gatewayMessageBudget, maxBytes int64, requestID int64, cancelMethod string) *gatewayMessageQueue {
	if budget == nil {
		panic("gateway message budget is not initialized")
	}
	if maxBytes <= 0 {
		maxBytes = gatewayMessageQueueMaxBytes
	}
	return &gatewayMessageQueue{
		maxBytes:     maxBytes,
		budget:       budget,
		notify:       make(chan struct{}, 1),
		requestID:    requestID,
		cancelMethod: cancelMethod,
		cancelDone:   make(chan struct{}),
		terminal:     make(chan struct{}),
	}
}

func (q *gatewayMessageQueue) enqueue(message gatewayWSMessage) error {
	size := int64(len(message.Raw))*2 + 1024
	if len(message.Raw) == 0 {
		data, err := json.Marshal(message.Parsed)
		if err != nil {
			return fmt.Errorf("encode queued message: %w", err)
		}
		size = int64(len(data))*2 + 1024
	}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return errGatewayMessageQueueClosed
	}
	if q.queuedBytes+size > q.maxBytes {
		err := fmt.Errorf("%w: queued=%d incoming=%d limit=%d", errGatewayMessageQueueFull, q.queuedBytes, size, q.maxBytes)
		released := q.failLocked(err)
		q.mu.Unlock()
		q.budget.release(released)
		q.signal()
		return err
	}
	if !q.budget.reserve(size) {
		err := fmt.Errorf("%w: shared budget limit=%d", errGatewayMessageQueueFull, q.budget.maxBytes)
		released := q.failLocked(err)
		q.mu.Unlock()
		q.budget.release(released)
		q.signal()
		return err
	}
	q.items = append(q.items, gatewayQueuedMessage{message: message, size: size})
	q.queuedBytes += size
	q.mu.Unlock()
	q.signal()
	return nil
}

func (q *gatewayMessageQueue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *gatewayMessageQueue) dequeue() (gatewayWSMessage, bool, error) {
	q.mu.Lock()
	if q.closed {
		err := q.err
		q.mu.Unlock()
		return gatewayWSMessage{}, false, err
	}
	if len(q.items) == 0 {
		q.mu.Unlock()
		return gatewayWSMessage{}, false, nil
	}
	item := q.items[0]
	q.items[0] = gatewayQueuedMessage{}
	if len(q.items) == 1 {
		q.items = nil
	} else {
		q.items = q.items[1:]
	}
	q.queuedBytes -= item.size
	q.budget.release(item.size)
	q.mu.Unlock()
	return item.message, true, nil
}

func (q *gatewayMessageQueue) failLocked(err error) int64 {
	q.closed = true
	q.err = err
	released := q.queuedBytes
	q.queuedBytes = 0
	for i := range q.items {
		q.items[i] = gatewayQueuedMessage{}
	}
	q.items = nil
	return released
}

func (q *gatewayMessageQueue) fail(err error) {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	released := q.failLocked(err)
	q.mu.Unlock()
	q.budget.release(released)
	q.signal()
}

func (q *gatewayMessageQueue) close() {
	q.fail(errGatewayMessageQueueClosed)
}

type targetResponse struct {
	Targets []GatewayTarget `json:"targets"`
}

type targetUnlockResponse struct {
	Cluster      string `json:"cluster"`
	Instance     string `json:"instance"`
	Disconnected bool   `json:"disconnected"`
}

type auditResponse struct {
	Events []auditRecord `json:"events"`
}

type auditRecord struct {
	ID              int64                 `json:"id"`
	UserID          string                `json:"user_id"`
	TokenID         string                `json:"token_id"`
	Cluster         string                `json:"cluster"`
	Instance        string                `json:"instance"`
	CredentialID    string                `json:"credential_id,omitempty"`
	VersionID       string                `json:"credential_version_id,omitempty"`
	GrantID         string                `json:"credential_grant_id,omitempty"`
	Scope           *CredentialGrantScope `json:"scope,omitempty"`
	ResourceVersion string                `json:"resource_version,omitempty"`
	Digest          string                `json:"digest,omitempty"`
	CorrelationID   string                `json:"correlation_id,omitempty"`
	Action          string                `json:"action"`
	Session         string                `json:"session"`
	CommandSummary  string                `json:"command_summary"`
	Status          string                `json:"status"`
	Error           string                `json:"error,omitempty"`
	Tail            string                `json:"tail,omitempty"`
	BytesIn         int64                 `json:"bytes_in"`
	BytesOut        int64                 `json:"bytes_out"`
	StartedAt       string                `json:"started_at"`
	EndedAt         string                `json:"ended_at,omitempty"`
}

type auditPurgeResponse struct {
	Deleted int64 `json:"deleted"`
}

type gatewayLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Name     string `json:"name,omitempty"`
}

type gatewayLoginResponse struct {
	Token     string `json:"token"`
	TokenID   string `json:"token_id"`
	TokenType string `json:"token_type"`
	Username  string `json:"username"`
}

type gatewayAdminTokenCreateRequest struct {
	User    string `json:"user"`
	Name    string `json:"name,omitempty"`
	Expires string `json:"expires,omitempty"`
}

func NewGatewayHub(store *GatewayStore, opts GatewayHubOptions) *GatewayHub {
	registry, err := NewAgentRegistry(store)
	if err != nil {
		panic(err)
	}
	if opts.AgentLease <= 0 {
		opts.AgentLease = 90 * time.Second
	}
	if opts.OperationTimeout <= 0 {
		opts.OperationTimeout = 30 * time.Minute
	}
	if opts.TargetQueueTimeout <= 0 {
		opts.TargetQueueTimeout = 2 * time.Minute
	}
	if opts.TargetReconnectGrace <= 0 {
		opts.TargetReconnectGrace = 10 * time.Second
	}
	if opts.LoginTokenTTL <= 0 {
		opts.LoginTokenTTL = 24 * time.Hour
	}
	if opts.MaxConcurrentOperations <= 0 {
		opts.MaxConcurrentOperations = 64
	}
	if opts.MaxConcurrentPerUser <= 0 {
		opts.MaxConcurrentPerUser = 8
	}
	if opts.MaxQueuedPerTarget <= 0 {
		opts.MaxQueuedPerTarget = 0
	}
	hub := &GatewayHub{
		store:    store,
		opts:     opts,
		registry: registry,
	}
	hub.registration = NewAgentRegistrationHandler(
		registry,
		opts.AgentLease,
		newGatewayMessageBudget(gatewayMessageBudgetMaxBytes),
	)
	hub.client = NewClientService(store, registry, opts)
	hub.client.hub = hub
	edge, err := NewEdgeRegistry(store)
	if err != nil {
		panic(err)
	}
	hub.edge = edge
	if err := store.RetireRunningUpgradeOperations(interruptedUpgradeRetiredReason); err != nil {
		panic(err)
	}
	if err := store.RecoverInterruptedExecutions(); err != nil {
		panic(fmt.Errorf("recover interrupted executions: %w", err))
	}
	return hub
}

func (h *GatewayHub) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws", HandleRetiredInboundWS)
	mux.HandleFunc("/v1/auth/login", h.HandleAuthLogin)
	mux.HandleFunc("/v1/agent/connect", h.HandleAgentConnect)
	mux.HandleFunc("/v1/edge/connect", h.HandleEdgeConnect)
	mux.HandleFunc("/v1/rpc", h.HandleClientRPC)
	mux.HandleFunc("/v1/git/", h.HandleGitHTTP)
	mux.HandleFunc("/", h.HandlePublicProxy)
	mux.HandleFunc("/v1/targets", h.HandleTargets)
	mux.HandleFunc("/v1/targets/unlock", h.auditAdminMutation("/v1/targets/unlock", h.HandleTargetUnlock))
	mux.HandleFunc("/v1/audit", h.HandleAudit)
	mux.HandleFunc("/v1/credential-bundles", h.HandleCredentialBundles)
	mux.HandleFunc("/v1/credentials", h.HandleCredentials)
	mux.HandleFunc("/v1/credentials/", h.HandleCredential)
	admin := func(path string, handler http.HandlerFunc) {
		mux.HandleFunc(path, h.auditAdminMutation(path, handler))
	}
	mux.HandleFunc("/v1/admin/tokens", h.HandleAdminTokens)
	admin("/v1/admin/users", h.HandleAdminUsers)
	admin("/v1/admin/users/password", h.HandleAdminUserPassword)
	admin("/v1/admin/users/disable", h.HandleAdminUserDisable)
	admin("/v1/admin/grants", h.HandleAdminGrants)
	admin("/v1/admin/instances", h.HandleAdminInstances)
	admin("/v1/admin/operations", h.HandleAdminOperations)
	admin("/v1/admin/proxy-routes", h.HandleAdminProxyRoutes)
	admin("/v1/admin/repos", h.HandleAdminRepos)
	admin("/v1/admin/repos/test", h.HandleAdminRepoTest)
	admin("/v1/admin/repos/clone", h.HandleAdminRepoClone)
	admin("/v1/admin/jobs", h.HandleAdminJobs)
	admin("/v1/admin/jobs/run", h.HandleAdminJobRun)
	admin("/v1/admin/jobs/issues", h.HandleAdminJobIssues)
	admin("/v1/admin/audit/optimize", h.HandleAdminAuditOptimize)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func (h *GatewayHub) HandleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req gatewayLoginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid login request", http.StatusBadRequest)
		return
	}
	user, err := h.store.VerifyUserPassword(req.Username, req.Password)
	if err != nil {
		writeDoopsError(w, http.StatusUnauthorized, "", doopsErrorResponse{
			Error:      "unauthorized",
			Code:       "gateway_login_invalid_credentials",
			AuthDomain: "gateway",
			Message:    "Gateway username or password was rejected",
			NextAction: "Verify the Gateway username/password or provide a valid Gateway user token",
		})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "password login"
	}
	_, _ = h.store.DeleteExpiredTokens(time.Now().UTC())
	token, err := h.store.CreateToken(CreateTokenRequest{
		Kind:      TokenKindUser,
		UserID:    user.ID,
		Name:      name,
		ExpiresAt: time.Now().UTC().Add(h.opts.LoginTokenTTL),
	})
	if err != nil {
		http.Error(w, "failed to create token", http.StatusInternalServerError)
		return
	}
	writeJSONHTTP(w, gatewayLoginResponse{
		Token:     token.Plaintext,
		TokenID:   token.ID,
		TokenType: string(TokenKindUser),
		Username:  user.Name,
	})
}

func (h *GatewayHub) HandleAdminTokens(w http.ResponseWriter, r *http.Request) {
	h.auditAdminMutation("/v1/admin/tokens", h.handleAdminTokens)(w, r)
}

func (h *GatewayHub) handleAdminTokens(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r)
		return
	}
	if !h.store.UserHasAction(auth.UserID, ActionAdmin) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet:
		tokens, err := h.store.ListTokens(TokenKindUser)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONHTTP(w, map[string]interface{}{"tokens": tokens})
		return
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		if err := h.store.RevokeToken(id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "token not found", http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONHTTP(w, map[string]interface{}{"id": id, "revoked": true})
		return
	case http.MethodPost:
		// 继续向下执行创建逻辑
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req gatewayAdminTokenCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid token request", http.StatusBadRequest)
		return
	}
	user, err := h.store.FindUserByName(req.User)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	createReq := CreateTokenRequest{Kind: TokenKindUser, UserID: user.ID, Name: req.Name}
	if strings.TrimSpace(req.Expires) != "" {
		ttl, err := time.ParseDuration(strings.TrimSpace(req.Expires))
		if err != nil {
			http.Error(w, "invalid expires duration", http.StatusBadRequest)
			return
		}
		if ttl <= 0 {
			http.Error(w, "expires duration must be positive", http.StatusBadRequest)
			return
		}
		createReq.ExpiresAt = time.Now().UTC().Add(ttl)
	}
	token, err := h.store.CreateToken(createReq)
	if err != nil {
		http.Error(w, "failed to create token", http.StatusInternalServerError)
		return
	}
	writeJSONHTTP(w, gatewayLoginResponse{
		Token:     token.Plaintext,
		TokenID:   token.ID,
		TokenType: string(TokenKindUser),
		Username:  user.Name,
	})
}

func (h *GatewayHub) HandleAdminOperations(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r)
		return
	}
	if !h.store.UserHasAction(auth.UserID, ActionAdmin) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	instance := strings.TrimSpace(r.URL.Query().Get("instance"))
	if (cluster == "") != (instance == "") {
		http.Error(w, "cluster and instance must be provided together", http.StatusBadRequest)
		return
	}
	targetScoped := cluster != "" && instance != ""
	if !targetScoped {
		http.Error(w, "cluster and instance are required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		ops := filterActiveOperationsForTarget(h.listActiveOperations(), cluster, instance)
		unresolved, err := h.client.unresolvedExecutions(cluster, instance)
		if err != nil {
			http.Error(w, "execution_guard_unavailable", http.StatusServiceUnavailable)
			return
		}
		for _, event := range unresolved {
			ops = append(ops, GatewayActiveOperation{
				ID: fmt.Sprintf("audit-%d", event.ID), AuditID: event.ID, State: "outcome_unknown",
				UserID: event.UserID, Cluster: event.Cluster, Instance: event.Instance,
				Action: event.Action, Session: event.Session, CommandSummary: event.CommandSummary,
				Kind: "unresolved", StartedAt: event.StartedAt, AgeSeconds: int64(time.Since(event.StartedAt).Seconds()),
			})
		}
		writeJSONHTTP(w, map[string]interface{}{"operations": ops})
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		if !h.cancelActiveOperationForTarget(id, cluster, instance) {
			http.Error(w, "operation not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		writeJSONHTTP(w, map[string]interface{}{"id": id, "cancel_requested": true, "state": "cancel_requested"})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (h *GatewayHub) listActiveOperations() []GatewayActiveOperation {
	return h.client.listActiveOperations()
}

func filterActiveOperationsForTarget(ops []GatewayActiveOperation, cluster, instance string) []GatewayActiveOperation {
	filtered := make([]GatewayActiveOperation, 0, len(ops))
	for _, op := range ops {
		if op.Cluster == cluster && op.Instance == instance {
			filtered = append(filtered, op)
		}
	}
	return filtered
}

func (h *GatewayHub) cancelActiveOperation(id string) bool {
	return h.client.cancelActiveOperation(id)
}

func (h *GatewayHub) cancelActiveOperationForTarget(id, cluster, instance string) bool {
	return h.client.cancelActiveOperationForTarget(id, cluster, instance)
}

func (h *GatewayHub) HandleAgentConnect(w http.ResponseWriter, r *http.Request) {
	h.registration.ServeHTTP(w, r)
}

func newGatewayAgentSession(
	cluster,
	instance,
	tokenID,
	remote string,
	conn *websocket.Conn,
	messageBudget *gatewayMessageBudget,
) *GatewayAgent {
	agent := &GatewayAgent{
		Cluster:         cluster,
		Instance:        instance,
		Key:             tunnelKey(cluster, instance),
		Remote:          remote,
		TokenID:         tokenID,
		ConnectedAt:     time.Now().UTC(),
		LastSeen:        time.Now().UTC(),
		conn:            conn,
		pending:         make(map[int64]*gatewayMessageQueue),
		activeByRequest: make(map[int64]*gatewayMessageQueue),
		resources:       make(map[string]*agentResourceSlot),
		sessionWriters:  make(map[string]int),
		activeSessions:  make(map[string]int),
		messageBudget:   messageBudget,
		closed:          make(chan struct{}),
		stateChanged:    make(chan struct{}),
	}
	return agent
}

func (h *GatewayHub) HandleTargets(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r)
		return
	}
	if !h.store.UserHasAction(auth.UserID, ActionTargetsList) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	targets := h.ListTargets()
	filtered := targets[:0]
	for _, target := range targets {
		if h.store.UserCan(auth.UserID, target.Cluster, target.Instance, ActionTargetsList) {
			filtered = append(filtered, target)
		}
	}
	writeJSONHTTP(w, targetResponse{Targets: filtered})
}

func (h *GatewayHub) HandleTargetUnlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r)
		return
	}
	if !h.store.UserHasAction(auth.UserID, ActionAdmin) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	instance := strings.TrimSpace(r.URL.Query().Get("instance"))
	if cluster == "" || instance == "" {
		http.Error(w, "missing cluster or instance", http.StatusBadRequest)
		return
	}
	agent := h.getAgent(cluster, instance)
	if agent == nil {
		http.Error(w, fmt.Sprintf("target offline: %s/%s", cluster, instance), http.StatusNotFound)
		return
	}
	if err := agent.forceUnlock(); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSONHTTP(w, targetUnlockResponse{Cluster: cluster, Instance: instance, Disconnected: true})
}

func (h *GatewayHub) HandleAudit(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r)
		return
	}
	isAdmin := h.store.UserIsPlatformAdmin(auth.UserID)

	switch r.Method {
	case http.MethodGet:
		events, err := h.visibleAuditEvents(auth, r.URL.Query())
		if err != nil {
			if errors.Is(err, ErrUnauthorized) {
				http.Error(w, "Forbidden", http.StatusForbidden)
			} else {
				http.Error(w, "audit_unavailable", http.StatusServiceUnavailable)
			}
			return
		}
		records := make([]auditRecord, 0, len(events))
		for _, event := range events {
			record := auditRecord{
				ID:              event.ID,
				UserID:          event.UserID,
				TokenID:         event.TokenID,
				Cluster:         event.Cluster,
				Instance:        event.Instance,
				CredentialID:    event.CredentialID,
				VersionID:       event.VersionID,
				GrantID:         event.GrantID,
				Scope:           event.Scope,
				ResourceVersion: event.ResourceVersion,
				Digest:          event.Digest,
				CorrelationID:   event.CorrelationID,
				Action:          string(event.Action),
				Session:         event.Session,
				CommandSummary:  event.CommandSummary,
				Status:          event.Status,
				Error:           event.Error,
				Tail:            event.Tail,
				BytesIn:         event.BytesIn,
				BytesOut:        event.BytesOut,
				StartedAt:       event.StartedAt.UTC().Format(time.RFC3339),
			}
			if !event.EndedAt.IsZero() {
				record.EndedAt = event.EndedAt.UTC().Format(time.RFC3339)
			}
			records = append(records, record)
		}
		writeJSONHTTP(w, auditResponse{Events: records})
	case http.MethodDelete:
		if !isAdmin {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		beforeRaw := strings.TrimSpace(r.URL.Query().Get("before"))
		if beforeRaw == "" {
			http.Error(w, "missing before", http.StatusBadRequest)
			return
		}
		before, err := time.Parse(time.RFC3339, beforeRaw)
		if err != nil {
			http.Error(w, "invalid before; use RFC3339", http.StatusBadRequest)
			return
		}
		deleted, err := h.store.DeleteAuditBefore(before)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONHTTP(w, auditPurgeResponse{Deleted: deleted})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

func (h *GatewayHub) HandleClientRPC(w http.ResponseWriter, r *http.Request) {
	h.client.HandleClientRPC(w, r)
}

func (h *ClientService) HandleClientRPC(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r)
		return
	}
	cluster := strings.TrimSpace(r.URL.Query().Get("cluster"))
	instance := strings.TrimSpace(r.URL.Query().Get("instance"))
	if cluster == "" {
		http.Error(w, "missing cluster", http.StatusBadRequest)
		return
	}
	if instance == "" {
		http.Error(w, "missing instance", http.StatusBadRequest)
		return
	}
	if !h.store.UserCan(auth.UserID, cluster, instance, ActionInfo) &&
		!h.store.UserCan(auth.UserID, cluster, instance, ActionExec) &&
		!h.store.UserCan(auth.UserID, cluster, instance, ActionAsk) &&
		!h.store.UserCan(auth.UserID, cluster, instance, ActionRead) &&
		!h.store.UserCan(auth.UserID, cluster, instance, ActionWrite) &&
		!h.store.UserCan(auth.UserID, cluster, instance, ActionPush) &&
		!h.store.UserCan(auth.UserID, cluster, instance, ActionCheck) &&
		!h.store.UserCan(auth.UserID, cluster, instance, ActionClean) &&
		!h.store.UserCan(auth.UserID, cluster, instance, ActionAgentUpgrade) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[gateway] client upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	var connMu sync.Mutex
	var dataWriteState websocketDataWriteState
	writeJSON := func(v interface{}) error {
		if err := writeWebSocketJSON(conn, &connMu, &dataWriteState, v, agentWSDataWriteTimeout); err != nil {
			log.Printf("[gateway] client write failed: %v", err)
			return fmt.Errorf("%w: %v", errGatewayClientDisconnected, err)
		}
		return nil
	}
	writeRaw := func(raw []byte) error {
		return writeJSON(json.RawMessage(raw))
	}

	connCtx, cancelConn := context.WithCancel(r.Context())
	defer cancelConn()
	messages := make(chan []byte, 16)
	go func() {
		defer close(messages)
		defer cancelConn()
		for {
			msgType, data, err := conn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Printf("[gateway] client read failed: %v", err)
				}
				return
			}
			if msgType != websocket.TextMessage {
				continue
			}
			select {
			case messages <- data:
			case <-connCtx.Done():
				return
			}
		}
	}()

	var callWG sync.WaitGroup
	var callsMu sync.Mutex
	calls := make(map[string]context.CancelFunc)
	for data := range messages {
		var req api.JSONRPCRequest
		if err := json.Unmarshal(data, &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			writeJSON(api.JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]interface{}{
					"protocolVersion": "2024-11-05",
					"serverInfo": map[string]string{
						"name":          "doops-gateway",
						"version":       release.Version,
						"releaseCommit": release.Commit,
					},
					"capabilities": clientCapabilities(h.registry.Get(cluster, instance)),
				},
			})
		case "tools/call":
			key, ok := clientRPCRequestKey(req.ID)
			if !ok {
				writeJSON(buildErrorResponse(req.ID, -32600, "tools/call requires a string or numeric request id"))
				continue
			}
			callsMu.Lock()
			if _, exists := calls[key]; exists {
				callsMu.Unlock()
				writeJSON(buildErrorResponse(req.ID, -32600, "request id is already active"))
				continue
			}
			callCtx, cancelCall := context.WithCancel(connCtx)
			calls[key] = cancelCall
			callsMu.Unlock()
			callWG.Add(1)
			go func(req api.JSONRPCRequest) {
				defer callWG.Done()
				defer func() {
					cancelCall()
					callsMu.Lock()
					delete(calls, key)
					callsMu.Unlock()
				}()
				if err := h.handleGatewayToolCall(callCtx, auth, cluster, instance, req, writeRaw, writeJSON); err != nil {
					writeJSON(buildErrorResponse(req.ID, -32603, err.Error()))
				}
			}(req)
		case "tools/cancel":
			var params struct {
				ID interface{} `json:"id"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				writeJSON(buildErrorResponse(req.ID, -32602, "invalid tools/cancel params"))
				continue
			}
			key, ok := clientRPCRequestKey(params.ID)
			if !ok {
				writeJSON(buildErrorResponse(req.ID, -32602, "tools/cancel requires the original request id"))
				continue
			}
			callsMu.Lock()
			cancel := calls[key]
			callsMu.Unlock()
			if cancel != nil {
				cancel()
			}
			writeJSON(api.JSONRPCResponse{
				JSONRPC: "2.0", ID: req.ID,
				Result: map[string]interface{}{"canceled": cancel != nil},
			})
		default:
			writeJSON(buildErrorResponse(req.ID, -32601, "unknown method: "+req.Method))
		}
	}
	callWG.Wait()
}

func clientRPCRequestKey(id interface{}) (string, bool) {
	switch id.(type) {
	case string, float64:
		encoded, err := json.Marshal(id)
		return string(encoded), err == nil
	default:
		return "", false
	}
}

func (h *ClientService) handleGatewayToolCall(ctx context.Context, auth TokenAuth, cluster, instance string, req api.JSONRPCRequest, writeRaw func([]byte) error, writeJSON func(interface{}) error) error {
	if err := h.store.validateUserTokenAuth(auth); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return writeJSON(buildErrorResponse(req.ID, -32001, "gateway_auth_failed"))
		}
		return writeJSON(buildErrorResponse(req.ID, -32603, "gateway_auth_unavailable"))
	}
	ctx = contextWithInternalAuditAuth(ctx, auth)
	var params api.ToolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSON(buildErrorResponse(req.ID, -32602, "invalid tools/call params"))
		return nil
	}
	if internalCredentialTool(params.Name) {
		writeJSON(buildErrorResponse(req.ID, -32003, "credential control-plane tools are internal only"))
		return nil
	}
	var promptArgs api.AgentPromptParams
	if params.Name == "doops_agent_prompt" {
		if err := json.Unmarshal(params.Arguments, &promptArgs); err != nil {
			writeJSON(buildErrorResponse(req.ID, -32602, "invalid agent prompt arguments"))
			return nil
		}
		operation := extractStringArg(params.Arguments, "operation")
		if operation != "" && operation != "ask" && operation != "apply" {
			writeJSON(buildErrorResponse(req.ID, -32602, "unsupported agent prompt operation: "+operation))
			return nil
		}
		if err := validateExplicitCredentialNames(promptArgs.CredentialRefs); err != nil {
			writeJSON(buildErrorResponse(req.ID, -32602, err.Error()))
			return nil
		}
		if err := validatePromptCredentialAccess(operation, promptArgs.Skill, len(promptArgs.CredentialRefs) != 0); err != nil {
			writeJSON(buildErrorResponse(req.ID, -32602, err.Error()))
			return nil
		}
		if len(promptArgs.CredentialBindings) != 0 {
			writeJSON(buildErrorResponse(req.ID, -32602, "credential_bindings are gateway-managed"))
			return nil
		}
	}
	action := actionForTool(params.Name, params.Arguments)
	if action == "" {
		writeJSON(buildErrorResponse(req.ID, -32601, "unknown doops action for tool: "+params.Name))
		return nil
	}
	startAudit := h.store.StartAudit
	if params.Name == "doops_file_write" {
		startAudit = h.store.startMutationAudit
	}
	auditID, auditErr := startAudit(AuditEvent{
		UserID:         auth.UserID,
		TokenID:        auth.TokenID,
		Cluster:        cluster,
		Instance:       instance,
		Action:         action,
		Session:        extractSession(params.Arguments),
		CommandSummary: summarizeToolCall(params.Name, params.Arguments),
		StartedAt:      time.Now().UTC(),
	})
	if auditErr != nil {
		writeJSON(buildErrorResponse(req.ID, -32603, "audit_unavailable"))
		return nil
	}
	var opID string
	var pendingFinish *AuditFinish
	defer func() {
		if opID != "" || pendingFinish == nil {
			return
		}
		// Rejected admission has no execution lease, but still owns its audit.
		id := h.registerActiveOperation(GatewayActiveOperation{
			AuditID: auditID, UserID: auth.UserID, TokenID: auth.TokenID,
			Cluster: cluster, Instance: instance, Action: action,
			Session: extractSession(params.Arguments), Kind: "rpc",
			CommandSummary: summarizeToolCall(params.Name, params.Arguments),
		}, func() {})
		_ = h.finishExecutionAudit(auditID, *pendingFinish)
		h.finishActiveOperation(id)
	}()
	finishAudit := func(status, errMsg, tail string, bytesOut int64) error {
		errMsg, tail = sanitizeAgentPromptAudit(params.Name, errMsg, tail)
		errMsg = sanitizeSecretText(errMsg)
		tail = sanitizeSecretText(tail)
		finish := AuditFinish{
			Status:   status,
			Error:    errMsg,
			Tail:     tail,
			BytesIn:  int64(len(params.Arguments)),
			BytesOut: bytesOut,
			EndedAt:  time.Now().UTC(),
		}
		if err := h.finishExecutionAudit(auditID, finish); err != nil {
			pendingFinish = &finish
			log.Printf("[gateway] audit_completion_failed: id=%d", auditID)
			return err
		}
		return nil
	}
	if !h.store.UserCan(auth.UserID, cluster, instance, action) {
		errMsg := fmt.Sprintf("forbidden: %s on %s/%s", action, cluster, instance)
		finishAudit("forbidden", errMsg, "", 0)
		writeJSON(buildErrorResponse(req.ID, -32003, errMsg))
		return nil
	}
	if err := h.validateCommand(params.Name, params.Arguments); err != nil {
		finishAudit("blocked", err.Error(), "", 0)
		writeJSON(buildErrorResponse(req.ID, -32602, err.Error()))
		return nil
	}
	agent := h.registry.Get(cluster, instance)
	if agent == nil {
		errMsg := fmt.Sprintf("target offline: %s/%s", cluster, instance)
		finishAudit("offline", errMsg, "", 0)
		writeJSON(buildErrorResponse(req.ID, -32004, errMsg))
		return nil
	}
	if params.Name == "doops_ssh" && !agent.StructuredSSH {
		errMsg := fmt.Sprintf(
			"target Agent does not advertise protocol %q; upgrade the Agent before using structured doops ssh",
			structuredSSHProtocol,
		)
		finishAudit("blocked", errMsg, "", 0)
		writeJSON(buildErrorResponse(req.ID, -32003, errMsg))
		return nil
	}
	if err := agent.requireExecutionLifecycle(params.Name); err != nil {
		finishAudit("blocked", err.Error(), "", 0)
		writeJSON(buildErrorResponse(req.ID, -32003, err.Error()))
		return nil
	}
	if action == ActionAgentUpgrade {
		var args agentUpgradeParams
		if err := json.Unmarshal(params.Arguments, &args); err != nil {
			errMsg := fmt.Sprintf("invalid agent upgrade params: %v", err)
			finishAudit("error", errMsg, "", 0)
			writeJSON(buildErrorResponse(req.ID, -32602, errMsg))
			return nil
		}
		if err := validateAgentUpgradeDispatch(args); err != nil {
			finishAudit("error", err.Error(), "", 0)
			writeJSON(buildErrorResponse(req.ID, -32003, err.Error()))
			return nil
		}
	}
	var err error
	var authorizedCredentials []authorizedCredentialMaterialization
	if params.Name == "doops_agent_prompt" && len(promptArgs.CredentialRefs) != 0 {
		authorizedCredentials, err = h.hub.planExplicitCredentialApply(
			ctx, auth, cluster, instance, promptArgs.SessionID,
			promptArgs.Instruction, promptArgs.CredentialRefs,
		)
		if err != nil {
			category := credentialErrorCategory(err)
			finishAudit("blocked", category, "", 0)
			writeJSON(buildErrorResponse(req.ID, -32003, category))
			return nil
		}
	}
	opCtx, cancelOp := context.WithCancel(ctx)
	defer cancelOp()
	resourceKey := resourceKeyForTool(action, params.Name, params.Arguments, cluster, instance)
	operation := GatewayActiveOperation{
		AuditID:        auditID,
		Resource:       resourceKey,
		UserID:         auth.UserID,
		TokenID:        auth.TokenID,
		Cluster:        cluster,
		Instance:       instance,
		Action:         action,
		Session:        extractSession(params.Arguments),
		CommandSummary: summarizeToolCall(params.Name, params.Arguments),
		Kind:           "rpc",
	}
	if h.hasCancellationBarrier(operation) {
		finishAudit("busy", "target has a cancellation pending; wait for terminal evidence", "", 0)
		writeJSON(buildErrorResponse(req.ID, -32005, "target busy: cancellation pending"))
		return nil
	}
	var holderID string
	opID, holderID = h.registerActiveOperationForAgent(operation, cancelOp, agent)
	operationFinished := false
	defer func() {
		if !operationFinished {
			h.finishActiveOperation(opID)
		}
	}()
	if holderID != "" {
		errMsg := "session has an active operation; wait for terminal evidence: " + holderID
		finishAudit("busy", errMsg, "", 0)
		writeJSON(buildErrorResponse(req.ID, -32005, errMsg))
		return nil
	}

	if len(authorizedCredentials) != 0 {
		credentialIDs := make([]string, 0, len(authorizedCredentials))
		for _, item := range authorizedCredentials {
			credentialIDs = append(credentialIDs, item.Authorized.Resource.ID)
		}
		// Match verification/promotion's lock order before holding a target lease.
		releaseCredentialLocks, err := h.hub.acquireCredentialMutationLocks(opCtx, credentialIDs)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				finishAudit("canceled", "operation canceled", "", 0)
				writeJSON(buildErrorResponse(req.ID, -32007, "operation canceled"))
				return nil
			}
			finishAudit("error", "credential_mutation_lock_failed", "", 0)
			writeJSON(buildErrorResponse(req.ID, -32603, "credential_mutation_lock_failed"))
			return nil
		}
		defer releaseCredentialLocks()
	}
	lease, err := agent.acquireActionLeaseForSession(opCtx, action, resourceKey, extractSession(params.Arguments), h.opts.MaxQueuedPerTarget, h.opts.TargetQueueTimeout)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			finishAudit("canceled", "operation canceled", "", 0)
			writeJSON(buildErrorResponse(req.ID, -32007, "operation canceled"))
			return nil
		}
		errMsg := fmt.Sprintf("%v: %s/%s", err, cluster, instance)
		finishAudit("busy", errMsg, "", 0)
		writeJSON(buildErrorResponse(req.ID, -32005, errMsg))
		return nil
	}
	h.attachOperationLease(opID, lease)
	opCtx = withInternalCredentialLock(opCtx, agent, resourceKey)
	releaseLimit, err := h.acquireOperationSlot(auth.UserID)
	if err != nil {
		finishAudit("rate_limited", err.Error(), "", 0)
		writeJSON(buildErrorResponse(req.ID, -32006, err.Error()))
		return nil
	}
	defer releaseLimit()
	if err := opCtx.Err(); err != nil {
		finishAudit("canceled", "operation canceled before dispatch", "", 0)
		writeJSON(buildErrorResponse(req.ID, -32007, "operation canceled before dispatch"))
		return nil
	}
	guarded := requiresExecutionOutcomeGuard(action, params.Name, params.Arguments, resourceKey)
	h.markOperationRunning(opID)
	opCtx = h.withExecutionOwner(opCtx, agent, resourceKey, opID)
	if guarded {
		if err := h.beginExecution(agent, opID, auditID); err != nil {
			_ = finishAudit("blocked", err.Error(), "", 0)
			writeJSON(buildErrorResponse(req.ID, -32005, err.Error()))
			return nil
		}
	}
	var credentialPreparation credentialApplyPreparation
	if len(authorizedCredentials) != 0 {
		for index := range authorizedCredentials {
			item := &authorizedCredentials[index]
			resolved, refreshErr := h.hub.store.AuthorizeCredentialUse(CredentialUseRequest{
				ActorID: auth.UserID, Credential: item.Reference.Name, Scope: item.Reference.Scope,
			})
			if refreshErr != nil || !h.hub.store.UserCan(auth.UserID, cluster, instance, ActionCredentialUse) {
				finishAudit("blocked", "credential_authorization_changed", "", 0)
				writeJSON(buildErrorResponse(req.ID, -32003, "credential_authorization_changed"))
				return nil
			}
			item.Authorized = resolved
		}
		credentialPreparation, err = h.hub.materializeExplicitCredentialApply(
			opCtx, auth, promptArgs.SessionID, authorizedCredentials,
		)
		if err != nil {
			category := credentialOperationErrorCategory(err)
			finishAudit("error", category, "", 0)
			writeJSON(buildErrorResponse(req.ID, -32603, category))
			return nil
		}
		promptArgs.CredentialRefs = nil
		promptArgs.CredentialBindings = credentialPreparation.Bindings
		params.Arguments, err = json.Marshal(promptArgs)
		if err != nil {
			_ = h.hub.rollbackCredentialPrepare(opCtx, credentialPreparation.completed)
			finishAudit("error", "credential_binding_encode_failed", "", 0)
			writeJSON(buildErrorResponse(req.ID, -32603, "credential_binding_encode_failed"))
			return nil
		}
	}
	tail := newAuditTailBuffer(8192)
	var bytesOut int64
	finalStatus := "success"
	finalErr := ""
	knownApplyFailure := false
	var fileWriteResult []byte
	var finalResponse []byte

	err = agent.relayToolCall(opCtx, params, h.opts.OperationTimeout, func(msg gatewayWSMessage) error {
		if method, _ := msg.Parsed["method"].(string); method == "notifications/message" {
			if p, ok := msg.Parsed["params"].(map[string]interface{}); ok {
				if chunk, ok := p["data"].(string); ok {
					bytesOut += int64(len(chunk))
					if params.Name != "doops_file_write" {
						tail.WriteString(chunk)
					}
				}
			}
			forwarded := msg.Parsed
			if p, ok := forwarded["params"].(map[string]interface{}); ok {
				p["request_id"] = req.ID
			}
			raw, marshalErr := json.Marshal(forwarded)
			if marshalErr != nil {
				return marshalErr
			}
			return writeRaw(raw)
		}
		if id, ok := msg.Parsed["id"]; ok {
			msg.Parsed["id"] = req.ID
			if result, ok := msg.Parsed["result"].(map[string]interface{}); ok {
				if isErr, ok := result["isError"]; ok && fmt.Sprintf("%v", isErr) == "true" {
					finalStatus = "error"
					finalErr = resultContentText(result)
					knownApplyFailure = true
				}
			}
			if rpcErr, ok := msg.Parsed["error"]; ok && rpcErr != nil {
				finalStatus = "error"
				finalErr = fmt.Sprintf("%v", rpcErr)
				knownApplyFailure = true
			}
			raw, _ := json.Marshal(msg.Parsed)
			if params.Name == "doops_file_write" {
				fileWriteResult = raw
				return nil
			}
			finalResponse = raw
			_ = id
		}
		return nil
	})
	if knownApplyFailure && len(credentialPreparation.completed) != 0 {
		if rollbackErr := h.hub.rollbackCredentialPrepare(context.WithoutCancel(opCtx), credentialPreparation.completed); rollbackErr != nil {
			err = fmt.Errorf("%w: credential_apply_outcome_unknown", errExecutionOutcomeUnknown)
		}
	}
	if err != nil {
		if errors.Is(err, errExecutionOutcomeUnknown) {
			finalStatus = "outcome_unknown"
			finalErr = errExecutionOutcomeUnknown.Error()
			finalResponse, _ = json.Marshal(buildErrorResponse(req.ID, -32008, finalErr))
		} else if errors.Is(err, errGatewayClientDisconnected) {
			finalStatus = "canceled"
			finalErr = errGatewayClientDisconnected.Error()
			finalResponse, _ = json.Marshal(buildErrorResponse(req.ID, -32603, finalErr))
		} else if errors.Is(err, context.Canceled) {
			finalStatus = "canceled"
			finalErr = "operation canceled"
			finalResponse, _ = json.Marshal(buildErrorResponse(req.ID, -32007, finalErr))
		} else {
			finalStatus = "error"
			finalErr = sanitizeSecretText(err.Error())
			finalResponse, _ = json.Marshal(buildErrorResponse(req.ID, -32603, finalErr))
		}
	}
	if err := finishAudit(finalStatus, finalErr, tail.String(), bytesOut); err != nil {
		h.finishActiveOperation(opID)
		operationFinished = true
		failure, _ := json.Marshal(buildErrorResponse(
			req.ID,
			-32603,
			"audit_completion_failed; execution outcome requires verification",
		))
		return writeRaw(failure)
	}
	// Persist the terminal audit state before releasing the resource lease. The
	// client response is sent last so a follow-up mutation cannot race cleanup.
	h.finishActiveOperation(opID)
	operationFinished = true
	if len(fileWriteResult) != 0 {
		return writeRaw(fileWriteResult)
	}
	if len(finalResponse) != 0 {
		return writeRaw(finalResponse)
	}
	return nil
}

func clientCapabilities(agent *GatewayAgent) map[string]interface{} {
	protocols := map[string]interface{}{}
	if agent != nil && agent.StructuredSSH {
		protocols[structuredSSHProtocol] = structuredSSHCapability()
	}
	if agent != nil && agent.ExecutionLifecycle {
		protocols[executionLifecycleProtocol] = executionLifecycleCapability()
	}
	capabilities := map[string]interface{}{
		"tools": map[string]interface{}{},
	}
	if agent != nil && release.ValidCommit(agent.ReleaseCommit) {
		capabilities["releaseCommit"] = agent.ReleaseCommit
	}
	if len(protocols) != 0 {
		capabilities["protocols"] = protocols
	}
	return capabilities
}

func validateExplicitCredentialNames(names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if !validCredentialMetadataToken(name, 128) {
			return errors.New("credential reference name is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("credential reference name is duplicated")
		}
		seen[name] = struct{}{}
	}
	return nil
}

func sanitizeAgentPromptAudit(toolName, errMsg, tail string) (string, string) {
	if toolName == "doops_file_write" {
		if errMsg != "" {
			errMsg = "file_write_failed"
		}
		return errMsg, ""
	}
	if toolName != "doops_agent_prompt" {
		return sanitizeSecretText(errMsg), sanitizeSecretText(tail)
	}
	if errMsg != "" {
		errMsg = sanitizeCredentialCategory(errMsg)
	}
	return errMsg, ""
}

func internalCredentialTool(tool string) bool {
	return tool == "doops_credential_materialize" ||
		tool == "doops_credential_cleanup"
}

func (h *GatewayHub) registerAgent(agent *GatewayAgent) error {
	return h.registry.Register(agent)
}

func (h *GatewayHub) unregisterAgent(agent *GatewayAgent) error {
	return h.registry.Unregister(agent)
}

func (h *GatewayHub) getAgent(cluster, instance string) *GatewayAgent {
	return h.registry.Get(cluster, instance)
}

func (h *GatewayHub) waitForAgent(ctx context.Context, cluster, instance string) *GatewayAgent {
	return h.registry.Wait(ctx, cluster, instance, h.opts.TargetReconnectGrace)
}

func (h *GatewayHub) ListTargets() []GatewayTarget {
	return h.registry.List()
}

func (h *GatewayHub) authenticateUser(r *http.Request) (TokenAuth, error) {
	return h.store.VerifyUserToken(bearerToken(r))
}

func (h *ClientService) authenticateUser(r *http.Request) (TokenAuth, error) {
	return h.store.VerifyUserToken(bearerToken(r))
}

func (h *ClientService) writeUserAuthError(w http.ResponseWriter, r *http.Request) {
	writeGatewayAuthError(w, `Bearer realm="doops-gateway"`)
}

func (h *GatewayHub) writeUserAuthError(w http.ResponseWriter, r *http.Request) {
	writeGatewayAuthError(w, `Bearer realm="doops-gateway"`)
}

func bearerToken(r *http.Request) string {
	token := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		return strings.TrimSpace(token[len("bearer "):])
	}
	return ""
}

type doopsErrorResponse struct {
	Error      string `json:"error"`
	Code       string `json:"code"`
	AuthDomain string `json:"auth_domain,omitempty"`
	Message    string `json:"message"`
	NextAction string `json:"next_action,omitempty"`
}

func writeGatewayAuthError(w http.ResponseWriter, challenge string) {
	writeDoopsError(w, http.StatusUnauthorized, challenge, doopsErrorResponse{
		Error:      "unauthorized",
		Code:       "gateway_auth_failed",
		AuthDomain: "gateway",
		Message:    "Gateway user token is missing, invalid, expired, or revoked",
		NextAction: "Run doops login for this Gateway target; do not replace the Gateway token with a managed credential or business API key",
	})
}

func writeDoopsError(w http.ResponseWriter, status int, challenge string, response doopsErrorResponse) {
	if challenge != "" {
		w.Header().Set("WWW-Authenticate", challenge)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func writeJSONHTTP(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (a *GatewayAgent) initialize() error {
	id := atomic.AddInt64(&a.reqID, 1)
	ch := a.registerPending(id, "")
	defer a.unregisterPending(id)
	if err := a.writeJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialize",
		"id":      id,
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo": map[string]string{
				"name":    "doops-gateway",
				"version": "1.0",
			},
		},
	}); err != nil {
		return err
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		if msg, ok, queueErr := ch.dequeue(); queueErr != nil {
			return queueErr
		} else if ok {
			if result, ok := msg.Parsed["result"].(map[string]interface{}); ok {
				a.applyInitializeResult(result)
			}
			return nil
		}
		select {
		case <-ch.notify:
		case <-timer.C:
			return fmt.Errorf("agent initialize timed out")
		case <-a.closed:
			return fmt.Errorf("agent connection closed")
		}
	}
}

func (a *GatewayAgent) applyInitializeResult(result map[string]interface{}) {
	runtimeID, _ := result["runtimeId"].(string)
	a.RuntimeID = strings.TrimSpace(runtimeID)
	serverInfo, _ := result["serverInfo"].(map[string]interface{})
	releaseCommit, _ := serverInfo["releaseCommit"].(string)
	releaseCommit = strings.TrimSpace(releaseCommit)
	if !release.ValidCommit(releaseCommit) {
		releaseCommit = ""
	}
	a.ReleaseCommit = releaseCommit
	capabilities, _ := result["capabilities"].(map[string]interface{})
	protocols, ok := capabilities["protocols"].(map[string]interface{})
	a.ExecutionLifecycle = executionLifecycleCapabilityCompatible(protocols[executionLifecycleProtocol])
	if !ok {
		return
	}
	if credentialPlanCapabilityCompatible(protocols["credentialPlan"]) {
		a.CredentialPlanProtocol = credentialPlanProtocolV2
	}
	a.StructuredSSH = structuredSSHCapabilityCompatible(protocols[structuredSSHProtocol])
}

func (a *GatewayAgent) readLoop(lease, initialReadTimeout time.Duration) {
	defer close(a.closed)
	defer a.conn.Close()
	a.conn.SetReadDeadline(time.Now().Add(initialReadTimeout))
	a.conn.SetPongHandler(func(payload string) error {
		a.heartbeat(payload)
		return a.conn.SetReadDeadline(time.Now().Add(lease))
	})
	for {
		refreshActivity := func() {
			now := time.Now()
			_ = a.conn.SetReadDeadline(now.Add(lease))
			a.touchMemory(now.UTC())
			a.acknowledgeReadProgress(now)
		}
		msgType, data, err := readWebSocketMessage(a.conn, refreshActivity)
		if err != nil {
			if !errors.Is(err, io.EOF) && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("[gateway] agent read failed: %s: %v", a.Key, err)
			}
			return
		}
		refreshActivity()
		if msgType != websocket.TextMessage {
			continue
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(data, &parsed); err != nil {
			continue
		}
		msg := gatewayWSMessage{Raw: append([]byte(nil), data...), Parsed: parsed}
		if id, ok := numericID(parsed["id"]); ok {
			a.pendingMu.Lock()
			queue := a.pending[id]
			a.pendingMu.Unlock()
			if queue != nil {
				queue.observeTerminal(msg)
				if err := queue.enqueue(msg); err != nil && !errors.Is(err, errGatewayMessageQueueClosed) {
					log.Printf("[gateway] agent message queue failed: %s id=%d: %v", a.Key, id, err)
					a.cancelPending(queue, true)
				}
				continue
			}
		}
		if method, _ := parsed["method"].(string); method == "notifications/message" {
			requestID, ok := requestIDFromNotification(parsed)
			if !ok {
				log.Printf("[gateway] agent protocol violation: notification missing request_id: %s", a.Key)
				return
			}
			a.pendingMu.Lock()
			queue := a.activeByRequest[requestID]
			a.pendingMu.Unlock()
			if queue != nil {
				if err := queue.enqueue(msg); err != nil && !errors.Is(err, errGatewayMessageQueueClosed) {
					log.Printf("[gateway] agent notification queue failed: %s request_id=%d: %v", a.Key, requestID, err)
					a.cancelPending(queue, true)
				}
			}
		}
	}
}

func (a *GatewayAgent) acknowledgeReadProgress(now time.Time) {
	if !a.lastProgressAck.IsZero() && now.Sub(a.lastProgressAck) < gatewayReadProgressAckInterval {
		return
	}
	a.lastProgressAck = now
	// An unsolicited Pong proves that the gateway consumed bytes before a large message finishes.
	writeHeartbeatControl(
		a.conn,
		&a.dataWriteState,
		websocket.PongMessage,
		[]byte("read-progress"),
		gatewayReadProgressAckWriteTimeout,
	)
}

func (a *GatewayAgent) cancelPending(queue *gatewayMessageQueue, async bool) <-chan struct{} {
	if queue == nil || queue.cancelMethod == "" {
		return nil
	}
	send := func() {
		defer close(queue.cancelDone)
		if err := a.writeJSON(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  queue.cancelMethod,
			"id":      fmt.Sprintf("cancel-%d", queue.requestID),
			"params":  map[string]interface{}{"id": queue.requestID},
		}); err != nil {
			log.Printf("[gateway] cancel failed: %s id=%d method=%s: %v", a.Key, queue.requestID, queue.cancelMethod, err)
		}
	}
	queue.cancelOnce.Do(func() {
		if async {
			go send()
			return
		}
		send()
	})
	return queue.cancelDone
}

func (a *GatewayAgent) waitPendingCancel(queue *gatewayMessageQueue) error {
	select {
	case <-queue.terminal:
		return queue.terminalErr
	default:
	}
	a.cancelPending(queue, true)
	timer := time.NewTimer(executionCancellationTimeout)
	defer timer.Stop()
	select {
	case <-queue.terminal:
		return queue.terminalErr
	case <-a.closed:
		return errExecutionOutcomeUnknown
	case <-timer.C:
		return errExecutionOutcomeUnknown
	}
}

func (a *GatewayAgent) pingLoop(registry *AgentRegistry, lease time.Duration) {
	interval := lease / 3
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if !writeHeartbeatPing(a.conn, &a.dataWriteState, []byte("ping"), 10*time.Second) {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if !writeHeartbeatPing(a.conn, &a.dataWriteState, []byte("ping"), 10*time.Second) {
				return
			}
			if registry != nil {
				a.stateMu.RLock()
				lastSeen := a.LastSeen
				a.stateMu.RUnlock()
				ctx, cancel := context.WithTimeout(context.Background(), gatewayAgentTouchTimeout)
				err := registry.TouchContext(ctx, a, lastSeen)
				cancel()
				if err != nil {
					log.Printf("[gateway] persist agent last-seen failed: %s generation=%d: %v", a.Key, a.Generation, err)
				}
			}
		case <-a.closed:
			return
		}
	}
}

func (a *GatewayAgent) touchMemory(lastSeen time.Time) {
	a.stateMu.Lock()
	a.LastSeen = lastSeen
	a.stateMu.Unlock()
}

func (a *GatewayAgent) heartbeat(payload string) {
	now := time.Now().UTC()
	a.stateMu.Lock()
	a.HeartbeatAt = now
	a.heartbeatPayload = payload
	a.LastSeen = now
	a.stateMu.Unlock()
}

func (a *GatewayAgent) relayToolCall(ctx context.Context, params api.ToolCallParams, timeout time.Duration, forward func(gatewayWSMessage) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := atomic.AddInt64(&a.reqID, 1)
	ch := a.registerPending(id, "tools/cancel")
	defer a.unregisterPending(id)
	uncertainIfInterrupted := interruptionNeedsReconciliation(params.Name, params.Arguments)
	uncertainWithoutTerminal := missingTerminalNeedsReconciliation(params.Name, params.Arguments)
	cancelAndWait := func() error {
		if err := a.waitPendingCancel(ch); err != nil {
			if !uncertainWithoutTerminal && errors.Is(err, errExecutionOutcomeUnknown) {
				return nil
			}
			return err
		}
		// Local termination cannot fence a remote controller or detached process.
		if uncertainIfInterrupted {
			return errExecutionOutcomeUnknown
		}
		return nil
	}
	sessionID := extractSession(params.Arguments)
	routeNotifications := params.Name != "doops_task_status"
	a.pendingMu.Lock()
	if routeNotifications {
		if a.activeByRequest == nil {
			a.activeByRequest = make(map[int64]*gatewayMessageQueue)
		}
		a.activeByRequest[id] = ch
		if sessionID != "" {
			if a.activeSessions == nil {
				a.activeSessions = make(map[string]int)
			}
			a.activeSessions[sessionID]++
		}
	}
	a.pendingMu.Unlock()
	defer func() {
		a.pendingMu.Lock()
		if routeNotifications && a.activeByRequest[id] == ch {
			delete(a.activeByRequest, id)
		}
		if routeNotifications && sessionID != "" {
			if count := a.activeSessions[sessionID]; count <= 1 {
				delete(a.activeSessions, sessionID)
			} else {
				a.activeSessions[sessionID] = count - 1
			}
		}
		a.pendingMu.Unlock()
	}()

	if err := a.writeJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"id":      id,
		"params": map[string]interface{}{
			"name":      params.Name,
			"arguments": params.Arguments,
		},
	}); err != nil {
		if uncertainWithoutTerminal {
			return fmt.Errorf("%w: dispatch failed: %v", errExecutionOutcomeUnknown, err)
		}
		return fmt.Errorf("%w: dispatch failed: %v", errGatewayAgentDisconnected, err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var forwardErr error
	forwardMessage := func(msg gatewayWSMessage) (bool, error) {
		if msg.Parsed == nil {
			return false, nil
		}
		if _, ok := msg.Parsed["id"]; ok {
			if (uncertainWithoutTerminal && executionResponseUnknown(msg.Parsed)) ||
				remoteMutationFailed(params.Name, params.Arguments, msg.Parsed) {
				return true, errExecutionOutcomeUnknown
			}
			if uncertainIfInterrupted && (ctx.Err() != nil || executionResponseCanceled(msg.Parsed)) {
				return true, errExecutionOutcomeUnknown
			}
			if forwardErr == nil {
				if err := forward(msg); err != nil {
					forwardErr = err
				}
			}
			return true, forwardErr
		}
		if forwardErr == nil {
			if err := forward(msg); err != nil {
				forwardErr = err
			}
		}
		return false, nil
	}
	for {
		if msg, ok, queueErr := ch.dequeue(); queueErr != nil {
			if err := cancelAndWait(); err != nil {
				return err
			}
			return queueErr
		} else if ok {
			if done, err := forwardMessage(msg); done {
				return err
			}
			continue
		}

		select {
		case <-ctx.Done():
			if err := cancelAndWait(); err != nil {
				return err
			}
			return ctx.Err()
		case <-ch.notify:
		case <-timer.C:
			if err := cancelAndWait(); err != nil {
				return err
			}
			return fmt.Errorf("operation timed out")
		case <-a.closed:
			for {
				msg, ok, queueErr := ch.dequeue()
				if queueErr != nil {
					if err := cancelAndWait(); err != nil {
						return err
					}
					return queueErr
				}
				if !ok {
					break
				}
				if done, err := forwardMessage(msg); done {
					return err
				}
			}
			select {
			case <-ch.terminal:
				if ch.terminalErr != nil &&
					!(errors.Is(ch.terminalErr, errExecutionOutcomeUnknown) && !uncertainWithoutTerminal) {
					return ch.terminalErr
				}
				if remoteMutationFailed(params.Name, params.Arguments, ch.terminalMessage.Parsed) {
					return errExecutionOutcomeUnknown
				}
				if uncertainIfInterrupted && (ctx.Err() != nil || executionResponseCanceled(ch.terminalMessage.Parsed)) {
					return errExecutionOutcomeUnknown
				}
				if forwardErr != nil {
					return forwardErr
				}
				return forward(ch.terminalMessage)
			default:
				if !uncertainWithoutTerminal {
					return errGatewayAgentDisconnected
				}
				return errExecutionOutcomeUnknown
			}
		}
	}
}

func (a *GatewayAgent) registerPending(id int64, cancelMethod string) *gatewayMessageQueue {
	queue := newGatewayMessageQueue(a.messageBudget, gatewayMessageQueueMaxBytes, id, cancelMethod)
	a.pendingMu.Lock()
	a.pending[id] = queue
	a.pendingMu.Unlock()
	return queue
}

func (a *GatewayAgent) unregisterPending(id int64) {
	a.pendingMu.Lock()
	queue := a.pending[id]
	delete(a.pending, id)
	a.pendingMu.Unlock()
	if queue != nil {
		queue.close()
	}
}

func (a *GatewayAgent) writeJSON(v interface{}) error {
	return writeWebSocketJSON(a.conn, &a.writeMu, &a.dataWriteState, v, agentWSDataWriteTimeout)
}

func (a *GatewayAgent) acquireActionLease(ctx context.Context, action GatewayAction, resourceKey string, maxQueued int, wait time.Duration) (*operationLease, error) {
	return a.acquireActionLeaseForSession(ctx, action, resourceKey, "", maxQueued, wait)
}

func exclusiveLockKey(resourceKey, session string) string {
	if strings.HasPrefix(resourceKey, "target-mutation:") && strings.TrimSpace(session) != "" {
		return "session-mutation:" + strings.TrimPrefix(resourceKey, "target-mutation:") + "/" + strings.TrimSpace(session)
	}
	return resourceKey
}

func sessionForResourceKey(resourceKey string) string {
	for _, prefix := range []string{"session-mutation:", "workspace:"} {
		if strings.HasPrefix(resourceKey, prefix) {
			value := strings.TrimPrefix(resourceKey, prefix)
			if prefix == "session-mutation:" {
				if index := strings.LastIndexByte(value, '/'); index >= 0 {
					return value[index+1:]
				}
			}
			return value
		}
	}
	return ""
}

func (a *GatewayAgent) sessionResourceHeldLocked(lockKey string) bool {
	if strings.HasPrefix(lockKey, "target:") {
		return a.writers > 0 || a.readers > 0 || len(a.resources) != 0
	}
	session := sessionForResourceKey(lockKey)
	if session == "" {
		return false
	}
	for key, count := range a.sessionWriters {
		if count == 0 {
			continue
		}
		if strings.HasPrefix(key, "target:") {
			return true
		}
		if sessionForResourceKey(key) == session {
			return true
		}
	}
	for key, slot := range a.resources {
		if slot != nil && len(slot.slot) == 0 && (sessionForResourceKey(key) == session || slot.session == session) {
			return true
		}
	}
	return false
}

func (a *GatewayAgent) sessionWriterHeldLocked(resourceKey, session string) bool {
	for key, count := range a.sessionWriters {
		if count > 0 && strings.HasPrefix(key, "target:") {
			return true
		}
	}
	if strings.HasPrefix(resourceKey, "target:") {
		return a.writers > 0
	}
	workspace := sessionForResourceKey(resourceKey)
	for key, count := range a.sessionWriters {
		holder := sessionForResourceKey(key)
		if count > 0 && ((session != "" && holder == session) || (workspace != "" && holder == workspace)) {
			return true
		}
	}
	for key, slot := range a.resources {
		if key != resourceKey && slot != nil && len(slot.slot) == 0 &&
			((session != "" && (slot.session == session || sessionForResourceKey(key) == session)) ||
				(workspace != "" && slot.session == workspace)) {
			return true
		}
	}
	return false
}

func (a *GatewayAgent) acquireActionLeaseForSession(ctx context.Context, action GatewayAction, resourceKey, session string, maxQueued int, wait time.Duration) (*operationLease, error) {
	if (strings.HasPrefix(resourceKey, "target-mutation:") || strings.HasPrefix(resourceKey, "nginx:")) && validateSession(strings.TrimSpace(session)) != nil {
		return nil, fmt.Errorf("a valid session is required for mutation")
	}
	if isExclusiveOperation(action, resourceKey) {
		lockKey := exclusiveLockKey(resourceKey, session)
		if err := a.acquireExclusive(ctx, lockKey, maxQueued, wait); err != nil {
			return nil, err
		}
		return newOperationLease(func() { a.releaseExclusive(lockKey) }), nil
	}
	if isConcurrentQuery(action, resourceKey) {
		if err := a.acquireReadOnly(); err != nil {
			return nil, err
		}
		return newOperationLease(a.releaseReadOnly), nil
	}
	if resourceKey != "" {
		if err := a.acquireResource(ctx, resourceKey, session, maxQueued, wait); err != nil {
			return nil, err
		}
		return newOperationLease(func() { a.releaseResource(resourceKey) }), nil
	}
	if isConcurrentReadOnlyAction(action) {
		if err := a.acquireReadOnly(); err != nil {
			return nil, err
		}
		return newOperationLease(a.releaseReadOnly), nil
	}
	lockKey := exclusiveLockKey(resourceKey, session)
	if err := a.acquireExclusive(ctx, lockKey, maxQueued, wait); err != nil {
		return nil, err
	}
	return newOperationLease(func() { a.releaseExclusive(lockKey) }), nil
}

func (a *GatewayAgent) acquireExclusive(ctx context.Context, lockKey string, maxQueued int, wait time.Duration) error {
	if strings.TrimSpace(lockKey) == "" {
		return fmt.Errorf("operation resource is required")
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	queued := false
	defer func() {
		if !queued {
			return
		}
		a.queueMu.Lock()
		if a.queued > 0 {
			a.queued--
		}
		a.queueMu.Unlock()
	}()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		a.opsMu.Lock()
		admissible := !a.draining && !a.sessionResourceHeldLocked(lockKey)
		if admissible {
			a.writers++
			if a.sessionWriters == nil {
				a.sessionWriters = make(map[string]int)
			}
			a.sessionWriters[lockKey]++
			a.opsMu.Unlock()
			a.setBusy(true)
			return nil
		}
		if a.stateChanged == nil {
			a.stateChanged = make(chan struct{})
		}
		changed := a.stateChanged
		a.opsMu.Unlock()

		if !queued {
			if maxQueued <= 0 {
				return fmt.Errorf("target busy")
			}
			a.queueMu.Lock()
			if a.queued >= maxQueued {
				a.queueMu.Unlock()
				return fmt.Errorf("target queue full")
			}
			a.queued++
			a.queueMu.Unlock()
			queued = true
		}

		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		case <-a.closed:
			return fmt.Errorf("target offline")
		case <-timer.C:
			return fmt.Errorf("target queue timeout")
		}
	}
}

func (a *GatewayAgent) releaseExclusive(lockKey string) {
	a.opsMu.Lock()
	if a.sessionWriters[lockKey] == 0 {
		a.opsMu.Unlock()
		return
	}
	a.writers--
	delete(a.sessionWriters, lockKey)
	a.opsMu.Unlock()
	a.setBusy(false)
	a.signalStateChange()
}

func (a *GatewayAgent) acquireReadOnly() error {
	a.opsMu.Lock()
	if a.draining || a.targetExclusiveHeldLocked() {
		a.opsMu.Unlock()
		return fmt.Errorf("target busy")
	}
	a.readers++
	a.opsMu.Unlock()
	a.setBusy(false)
	return nil
}

func (a *GatewayAgent) targetExclusiveHeldLocked() bool {
	for key, count := range a.sessionWriters {
		if count > 0 && strings.HasPrefix(key, "target:") {
			return true
		}
	}
	return false
}

func (a *GatewayAgent) releaseReadOnly() {
	a.opsMu.Lock()
	if a.readers > 0 {
		a.readers--
	}
	idle := a.readers == 0 && a.writers == 0
	a.opsMu.Unlock()
	if idle {
		a.setBusy(false)
	}
	a.signalStateChange()
}

func (a *GatewayAgent) acquireResource(ctx context.Context, key, session string, maxQueued int, wait time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.opsMu.Lock()
	if a.draining || a.sessionWriterHeldLocked(key, session) {
		a.opsMu.Unlock()
		return fmt.Errorf("target busy")
	}
	slot := a.resources[key]
	if slot == nil {
		if a.resources == nil {
			a.resources = make(map[string]*agentResourceSlot)
		}
		slot = &agentResourceSlot{slot: make(chan struct{}, 1)}
		slot.slot <- struct{}{}
		a.resources[key] = slot
	}
	select {
	case <-slot.slot:
		slot.session = session
		a.readers++
		a.opsMu.Unlock()
		a.setBusy(true)
		return nil
	default:
	}
	if maxQueued <= 0 {
		a.opsMu.Unlock()
		return fmt.Errorf("target busy")
	}
	if slot.queued >= maxQueued {
		a.opsMu.Unlock()
		return fmt.Errorf("target queue full")
	}
	slot.queued++
	a.opsMu.Unlock()
	defer func() {
		a.opsMu.Lock()
		if slot.queued > 0 {
			slot.queued--
		}
		if slot.queued == 0 && len(slot.slot) == 1 && a.resources[key] == slot {
			delete(a.resources, key)
		}
		a.opsMu.Unlock()
	}()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-slot.slot:
		a.opsMu.Lock()
		if err := ctx.Err(); err != nil {
			slot.slot <- struct{}{}
			a.opsMu.Unlock()
			return err
		}
		if a.draining || a.sessionWriterHeldLocked(key, session) {
			slot.slot <- struct{}{}
			a.opsMu.Unlock()
			return fmt.Errorf("target busy")
		}
		slot.session = session
		a.readers++
		a.opsMu.Unlock()
		a.setBusy(true)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("target queue timeout")
	case <-a.closed:
		return fmt.Errorf("target offline")
	}
}

func (a *GatewayAgent) releaseResource(key string) {
	a.opsMu.Lock()
	slot := a.resources[key]
	if slot != nil {
		select {
		case slot.slot <- struct{}{}:
		default:
		}
		if slot.queued == 0 && len(slot.slot) == 1 {
			delete(a.resources, key)
		}
	}
	if a.readers > 0 {
		a.readers--
	}
	idle := a.readers == 0 && a.writers == 0
	a.opsMu.Unlock()
	if idle {
		a.setBusy(false)
	}
	a.signalStateChange()
}

func (a *GatewayAgent) signalStateChange() {
	a.opsMu.Lock()
	if a.stateChanged == nil {
		a.stateChanged = make(chan struct{})
	}
	previous := a.stateChanged
	a.stateChanged = make(chan struct{})
	a.opsMu.Unlock()
	close(previous)
}

func isConcurrentReadOnlyAction(action GatewayAction) bool {
	switch action {
	case ActionRead, ActionInfo, ActionCheck:
		return true
	default:
		return false
	}
}

func isConcurrentQuery(action GatewayAction, resourceKey string) bool {
	return (isConcurrentReadOnlyAction(action) && action != ActionRead) ||
		strings.HasPrefix(resourceKey, "query:")
}

func isExclusiveOperation(action GatewayAction, resourceKey string) bool {
	return action == ActionAgentUpgrade || strings.HasPrefix(resourceKey, "target:") || strings.HasPrefix(resourceKey, "target-mutation:") ||
		(resourceKey == "" && !isConcurrentReadOnlyAction(action))
}

func (a *GatewayAgent) forceUnlock() error {
	a.opsMu.Lock()
	defer a.opsMu.Unlock()
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	if a.writers != 0 || a.readers != 0 || len(a.resources) != 0 ||
		len(a.sessionWriters) != 0 || a.queued != 0 || len(a.pending) != 0 {
		return fmt.Errorf("target has active or queued operations; request operation cancellation and wait for terminal evidence")
	}
	a.draining = true
	return a.conn.Close()
}

func (a *GatewayAgent) setBusy(busy bool) {
	a.stateMu.Lock()
	a.Busy = busy
	a.stateMu.Unlock()
}

type targetRuntimeState struct {
	busy      bool
	status    string
	reason    string
	activeOps int
	queuedOps int
	resources []string
	sessions  []string
}

func (a *GatewayAgent) runtimeState() targetRuntimeState {
	state := targetRuntimeState{status: "idle"}
	a.opsMu.Lock()
	writers := a.writers
	readers := a.readers
	targetExclusive := false
	for lockKey, count := range a.sessionWriters {
		if count > 0 && strings.HasPrefix(lockKey, "target:") {
			targetExclusive = true
			break
		}
	}
	resources := make([]string, 0, len(a.resources))
	resourceQueued := 0
	for key, slot := range a.resources {
		resources = append(resources, key)
		if slot != nil {
			resourceQueued += slot.queued
		}
	}
	a.opsMu.Unlock()
	sort.Strings(resources)

	a.queueMu.Lock()
	queued := a.queued
	a.queueMu.Unlock()

	a.pendingMu.Lock()
	sessions := make([]string, 0, len(a.activeSessions))
	for sessionID := range a.activeSessions {
		sessions = append(sessions, sessionID)
	}
	a.pendingMu.Unlock()
	sort.Strings(sessions)

	state.activeOps = writers + readers
	state.queuedOps = queued + resourceQueued
	state.resources = resources
	state.sessions = sessions

	switch {
	case targetExclusive:
		state.busy = true
		state.status = "busy"
		state.reason = "exclusive_operation"
	case state.activeOps > 0 || len(resources) > 0 || state.queuedOps > 0:
		state.status = "active"
	}
	return state
}

func (a *GatewayAgent) busyState() bool {
	return a.runtimeState().busy
}

func (a *GatewayAgent) snapshot() GatewayTarget {
	a.stateMu.RLock()
	target := GatewayTarget{
		Cluster:       a.Cluster,
		Instance:      a.Instance,
		Key:           a.Key,
		Remote:        a.Remote,
		TokenID:       a.TokenID,
		ReleaseCommit: a.ReleaseCommit,
		ConnectedAt:   a.ConnectedAt,
		LastSeen:      a.LastSeen,
	}
	a.stateMu.RUnlock()
	state := a.runtimeState()
	target.Busy = state.busy
	target.Status = state.status
	target.BusyReason = state.reason
	target.ActiveOps = state.activeOps
	target.QueuedOps = state.queuedOps
	target.Resources = state.resources
	target.Sessions = state.sessions
	return target
}

func numericID(v interface{}) (int64, bool) {
	switch id := v.(type) {
	case float64:
		return int64(id), true
	case int64:
		return id, true
	case int:
		return int64(id), true
	default:
		return 0, false
	}
}

func actionForTool(tool string, args json.RawMessage) GatewayAction {
	switch tool {
	case "doops_web":
		operation := strings.ToLower(strings.TrimSpace(extractStringArg(args, "operation")))
		if containsWebString(webReadOperations, operation) {
			return ActionAsk
		}
		if containsWebString(webMutationOperations, operation) {
			return ActionExec
		}
		return ""
	case "doops_shell", "doops_ssh", "doops_bg", "doops_task_status", "doops_k8s":
		return ActionExec
	case "doops_agent_prompt":
		if extractStringArg(args, "operation") == "apply" {
			return ActionExec
		}
		switch extractStringArg(args, "mode") {
		case "metadata", "history":
			return ActionAsk
		default:
			return ActionAsk
		}
	case "doops_git_clone":
		return ActionPull
	case "doops_file_read":
		return ActionRead
	case "doops_file_write":
		return ActionWrite
	case "doops_node_info":
		return ActionInfo
	case "doops_check_deployment":
		return ActionCheck
	case "doops_clean_workspace":
		return ActionClean
	case "doops_agent_upgrade":
		return ActionAgentUpgrade
	case "doops_credential_materialize":
		return ActionCredentialUse
	case "doops_credential_cleanup":
		return ActionCredentialRevoke
	case "doops_workspace_begin", "doops_workspace_chunk", "doops_workspace_commit":
		return ActionPush
	case "doops_workspace_pull_begin", "doops_workspace_pull_chunk":
		return ActionPull
	default:
		return ""
	}
}

func resourceKeyForTool(action GatewayAction, tool string, args json.RawMessage, cluster, instance string) string {
	targetMutation := "target-mutation:" + cluster + "/" + instance
	if tool == "doops_web" || (tool == "doops_agent_prompt" && extractStringArg(args, "skill") == "doweb") {
		if action == ActionExec {
			// Nginx activation and reload are shared across website sessions.
			return "nginx:" + cluster + "/" + instance
		}
		if tool == "doops_web" {
			return "query:web:" + cluster + "/" + instance
		}
	}
	switch action {
	case ActionExec:
		if tool == "doops_agent_prompt" && extractStringArg(args, "operation") == "apply" {
			return targetMutation
		}
		if tool == "doops_shell" || tool == "doops_bg" || tool == "doops_ssh" {
			// A session selects a workspace, not an OS or remote-resource sandbox.
			return targetMutation
		}
		if tool == "doops_task_status" {
			return "query:task:" + extractStringArg(args, "task_id")
		}
		if tool == "doops_k8s" {
			if !gatewayK8SReadOnly(extractStringArg(args, "operation")) {
				return targetMutation
			}
			if resource := extractStringArg(args, "resource"); resource != "" {
				return "query:k8s:" + extractStringArg(args, "namespace") + "/" + canonicalK8SResource(resource)
			}
			if name := extractStringArg(args, "name"); name != "" {
				return "query:k8s:" + extractStringArg(args, "namespace") + "/" + extractStringArg(args, "kind") + "/" + name
			}
			return "query:k8s:" + cluster + "/" + instance
		}
		return targetMutation
	case ActionAsk:
		if sessionID := extractSession(args); sessionID != "" {
			return "workspace:" + sessionID
		}
	case ActionClean:
		workspace := strings.TrimSpace(extractStringArg(args, "workspace"))
		if validateSession(workspace) == nil {
			return "workspace:" + workspace
		}
		return targetMutation
	case ActionPush, ActionWrite:
		if sessionID := extractSession(args); sessionID != "" {
			return "workspace:" + sessionID
		}
		return targetMutation
	case ActionPull, ActionRead:
		if sessionID := extractSession(args); sessionID != "" {
			return "workspace:" + sessionID
		}
	case ActionCredentialUse, ActionCredentialRevoke:
		return targetMutation
	case ActionAgentUpgrade:
		return "target:" + cluster + "/" + instance
	case ActionCredentialMetadata:
		if sessionID := extractSession(args); sessionID != "" {
			return "workspace:" + sessionID
		}
	}
	return ""
}

func gatewayK8SReadOnly(operation string) bool {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case "get", "describe", "logs", "top", "events", "rollout-status":
		return true
	default:
		return false
	}
}

func extractSession(args json.RawMessage) string {
	var m map[string]interface{}
	if json.Unmarshal(args, &m) != nil {
		return ""
	}
	for _, key := range []string{"session_id", "session"} {
		if v, _ := m[key].(string); v != "" {
			return v
		}
	}
	return ""
}

func extractStringArg(args json.RawMessage, key string) string {
	var m map[string]interface{}
	if json.Unmarshal(args, &m) != nil {
		return ""
	}
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func requestIDFromNotification(parsed map[string]interface{}) (int64, bool) {
	params, _ := parsed["params"].(map[string]interface{})
	if params == nil {
		return 0, false
	}
	requestID, ok := params["request_id"]
	if !ok {
		return 0, false
	}
	return numericID(requestID)
}

func summarizeToolCall(tool string, args json.RawMessage) string {
	var m map[string]interface{}
	if json.Unmarshal(args, &m) != nil {
		return tool
	}
	switch tool {
	case "doops_shell", "doops_bg":
		if cmd, _ := m["command"].(string); cmd != "" {
			return trimTail(cmd, 512)
		}
	case "doops_ssh":
		host, _ := m["host"].(string)
		command, _ := m["command"].([]interface{})
		return fmt.Sprintf("doops_ssh host=%s remote_args=%d", strings.TrimSpace(host), len(command))
	case "doops_agent_prompt":
		operation, _ := m["operation"].(string)
		summary := "doops_agent_prompt operation=" + strings.ToLower(strings.TrimSpace(operation))
		if skill, _ := m["skill"].(string); skill == "doweb" || skill == "dosql" {
			summary += " skill=" + skill
		}
		return summary
	case "doops_web":
		return trimTail("doops_web "+extractStringArg(args, "operation")+" site="+extractStringArg(args, "site_id"), 512)
	case "doops_git_clone":
		repoURL, _ := m["url"].(string)
		branch, _ := m["branch"].(string)
		return trimTail("doops_git_clone "+repoURL+" "+branch, 512)
	case "doops_file_read", "doops_file_write":
		if p, _ := m["path"].(string); p != "" {
			return tool + " " + p
		}
	case "doops_workspace_pull_begin":
		if sessionID, _ := m["session_id"].(string); sessionID != "" {
			return tool + " " + sessionID
		}
	case "doops_agent_upgrade":
		if image, _ := m["image"].(string); image != "" {
			return tool + " " + image
		}
	case "doops_k8s":
		operation, _ := m["operation"].(string)
		resource, _ := m["resource"].(string)
		if resource == "" {
			kind, _ := m["kind"].(string)
			name, _ := m["name"].(string)
			resource = strings.Trim(strings.TrimSpace(kind)+"/"+strings.TrimSpace(name), "/")
		}
		namespace, _ := m["namespace"].(string)
		summary := "doops_k8s " + strings.TrimSpace(operation) + " namespace=" + strings.TrimSpace(namespace) + " resource=" + resource
		if replicas, ok := m["replicas"]; ok {
			summary += " replicas=" + fmt.Sprint(replicas)
		}
		if container, _ := m["container"].(string); container != "" {
			summary += " container=" + container
		}
		if image, _ := m["image"].(string); image != "" {
			summary += " image=" + image
		}
		return trimTail(summary, 512)
	}
	return tool
}

func canonicalK8SResource(resource string) string {
	parts := strings.SplitN(strings.TrimSpace(resource), "/", 2)
	if len(parts) != 2 {
		return strings.TrimSpace(resource)
	}
	aliases := map[string]string{
		"deploy": "deployment", "deployments": "deployment",
		"ds": "daemonset", "daemonsets": "daemonset",
		"sts": "statefulset", "statefulsets": "statefulset",
		"po": "pod", "pods": "pod",
	}
	kind := parts[0]
	if canonical := aliases[kind]; canonical != "" {
		kind = canonical
	}
	return kind + "/" + parts[1]
}

func tunnelKey(cluster, instance string) string {
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	return cluster + "/" + instance
}
