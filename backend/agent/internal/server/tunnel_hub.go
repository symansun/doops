package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type GatewayHubOptions struct {
	AgentLease            time.Duration
	OperationTimeout      time.Duration
	TargetQueueTimeout    time.Duration
	TargetReconnectGrace  time.Duration
	LoginTokenTTL         time.Duration
	LoginTokenMaxLifetime time.Duration
	LoginFailureLimit     int
	LoginFailureWindow    time.Duration
	MaxQueuedPerTarget    int
	DetachedTaskTimeout   time.Duration
	// AllowOpenAgentRegistration admits an Agent without an enrollment token
	// to a target with no confirmed enrollment. It exists only to migrate
	// Agents that were deployed before enrollment existed.
	AllowOpenAgentRegistration bool
	// RequireAgentEnrollment closes open registration for production. Library
	// callers leave it false (the historical behavior); the Gateway command
	// sets it unless -allow-open-agent-registration is passed.
	RequireAgentEnrollment bool
	// ConsoleForwardSecret is the HMAC key that lets the Web console sign the
	// browser address of a login. It is loaded from DOOPS_CONSOLE_FORWARD_SECRET
	// only, so it never appears in a process listing. Empty disables it.
	ConsoleForwardSecret []byte
	// AllowUnenrolledCredentialDelivery restores pre-enrollment credential
	// delivery for an explicitly open Gateway (no enrollment tokens exist).
	// It is insecure and only for migration deployments such as a private
	// Gateway that has not issued a single enrollment token.
	AllowUnenrolledCredentialDelivery bool
}

type GatewayHub struct {
	store        *GatewayStore
	opts         GatewayHubOptions
	registry     *AgentRegistry
	registration *AgentRegistrationHandler
	client       *ClientService
	edge         *EdgeRegistry

	bgCtx    context.Context
	bgCancel context.CancelFunc

	credentialMutationLocks sync.Map
	monitorRunning          sync.Map
	loginFailures           *loginFailureLimiter
}

type targetResponse struct {
	Targets []GatewayTarget `json:"targets"`
}

func NewGatewayHub(store *GatewayStore, opts GatewayHubOptions) *GatewayHub {
	registry, err := NewAgentRegistry(store)
	if err != nil {
		panic(err)
	}
	allowOpenRegistration := opts.AllowOpenAgentRegistration || !opts.RequireAgentEnrollment
	registry.allowOpenRegistration = allowOpenRegistration
	if opts.AgentLease <= 0 {
		opts.AgentLease = 90 * time.Second
	}
	if opts.OperationTimeout <= 0 {
		opts.OperationTimeout = 60 * time.Minute
	}
	if opts.TargetQueueTimeout <= 0 {
		opts.TargetQueueTimeout = 2 * time.Minute
	}
	// Zero means "unset" and keeps the 10s reconnect grace. The
	// explicitNoReconnectGrace sentinel means "fail immediately"; any other
	// negative value restores the default.
	if opts.TargetReconnectGrace == explicitNoReconnectGrace {
		opts.TargetReconnectGrace = 0
	} else if opts.TargetReconnectGrace <= 0 {
		opts.TargetReconnectGrace = 10 * time.Second
	}
	opts.LoginTokenTTL, opts.LoginTokenMaxLifetime = normalizeLoginTokenLifetimes(opts.LoginTokenTTL, opts.LoginTokenMaxLifetime)
	if opts.LoginFailureLimit <= 0 {
		opts.LoginFailureLimit = 10
	}
	if opts.LoginFailureWindow <= 0 {
		opts.LoginFailureWindow = time.Minute
	}
	if opts.MaxQueuedPerTarget <= 0 {
		opts.MaxQueuedPerTarget = 0
	}
	if opts.DetachedTaskTimeout <= 0 {
		opts.DetachedTaskTimeout = 24 * time.Hour
	}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	hub := &GatewayHub{
		store:         store,
		opts:          opts,
		registry:      registry,
		bgCtx:         bgCtx,
		bgCancel:      bgCancel,
		loginFailures: newLoginFailureLimiter(),
	}
	hub.registration = NewAgentRegistrationHandler(
		registry,
		opts.AgentLease,
		newGatewayMessageBudget(gatewayMessageBudgetMaxBytes),
		allowOpenRegistration,
	)
	hub.client = NewClientService(store, registry, opts)
	hub.client.hub = hub
	hub.registration.onRegistered = hub.deliverAgentCredentials
	edge, err := NewEdgeRegistry(store)
	if err != nil {
		panic(err)
	}
	hub.edge = edge
	if err := store.RecoverInterruptedExecutions(); err != nil {
		panic(fmt.Errorf("recover interrupted executions: %w", err))
	}
	return hub
}

func (h *GatewayHub) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws", HandleRetiredInboundWS)
	mux.HandleFunc("/v1/auth/login", h.HandleAuthLogin)
	mux.HandleFunc("/v1/auth/me", h.HandleAuthMe)
	mux.HandleFunc("/v1/agent/connect", h.HandleAgentConnect)
	mux.HandleFunc("/v1/edge/connect", h.HandleEdgeConnect)
	mux.HandleFunc("/v1/rpc", h.HandleClientRPC)
	mux.HandleFunc("/v1/git/", h.HandleGitHTTP)
	mux.HandleFunc("/", h.HandlePublicProxy)
	mux.HandleFunc("/v1/targets", h.HandleTargets)
	mux.HandleFunc("/v1/platform-monitor", h.HandlePlatformMonitor)
	mux.HandleFunc("/v1/monitor/overview", h.HandleMonitorOverview)
	mux.HandleFunc("/v1/monitor/resources/history", h.HandleMonitorResourceHistory)
	mux.HandleFunc("/v1/monitor/schedules", h.HandleMonitorSchedules)
	mux.HandleFunc("/v1/monitor/runs", h.HandleMonitorRuns)
	mux.HandleFunc("/v1/monitor/runs/", h.HandleMonitorRuns)
	mux.HandleFunc("/v1/assets/records", h.HandlePersonalAssetRecords)
	mux.HandleFunc("/v1/assets/catalog", h.HandleResourceCatalog)
	mux.HandleFunc("/v1/assets/cluster-profiles", h.HandleClusterProfiles)
	mux.HandleFunc("/v1/assets/workspace", h.HandleAgentAssetScope)
	mux.HandleFunc("/v1/assets/machines", h.HandleMachineInventory)
	mux.HandleFunc("/v1/assets/clusters", h.HandleClusterInventory)
	mux.HandleFunc("/v1/assets/node-links", h.HandleAssetNodeLinks)
	mux.HandleFunc("/v1/assets/dashboard", h.HandleAssetDashboard)
	mux.HandleFunc("/v1/assets/machine-diagnostics", h.HandleMachineDiagnostics)
	mux.HandleFunc("/v1/assets/systems", h.HandleBusinessInventory)
	mux.HandleFunc("/v1/assets/topology", h.HandleAssetTopology)
	mux.HandleFunc("/v1/assets/coverage", h.HandleAssetCoverage)
	mux.HandleFunc("/v1/assets/monitor", h.HandleAssetMonitor)
	mux.HandleFunc("/v1/assets/inspections", h.HandleAssetInspection)
	mux.HandleFunc("/v1/audit", h.HandleAudit)
	mux.HandleFunc("/v1/background-tasks", h.HandleBackgroundTasks)
	mux.HandleFunc("/v1/background-tasks/", h.HandleBackgroundTask)
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
	admin("/v1/admin/instances/archive", h.HandleAdminInstanceArchive)
	admin("/v1/admin/instances/unarchive", h.HandleAdminInstanceUnarchive)
	admin("/v1/admin/proxy-routes", h.HandleAdminProxyRoutes)
	mux.HandleFunc("/v1/admin/operations", h.HandleAdminOperations)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if h.IsDraining() {
			http.Error(w, "draining", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// StartHeartbeatFlusher periodically persists all live Agents' in-memory
// LastSeen timestamps in one batched SQLite write transaction.
func (h *GatewayHub) StartHeartbeatFlusher(interval time.Duration) {
	if h == nil || h.registry == nil {
		return
	}
	if interval <= 0 {
		interval = h.opts.AgentLease / 3
		if interval <= 0 {
			interval = 30 * time.Second
		}
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-h.bgCtx.Done():
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(h.bgCtx, gatewayAgentTouchTimeout)
				err := h.registry.FlushHeartbeatsContext(ctx)
				cancel()
				if err != nil && h.bgCtx.Err() == nil {
					log.Printf("[gateway] batched agent heartbeat flush failed: %v", err)
				}
			}
		}
	}()
}

// BeginDrain marks the Gateway as draining: /health returns 503 and new
// operations (RPC, Git, Proxy, internal calls, Agent registrations) are rejected.
func (h *GatewayHub) BeginDrain() {
	if h == nil {
		return
	}
	if h.client != nil {
		h.client.beginDrain()
	}
	if h.registration != nil {
		h.registration.draining.Store(true)
	}
}

func (h *GatewayHub) IsDraining() bool {
	return h != nil && h.client != nil && h.client.isDraining()
}

func (h *GatewayHub) ActiveOperationCount() int {
	if h == nil || h.client == nil {
		return 0
	}
	return h.client.activeOperationCount()
}

// WaitIdle waits until all in-flight operations complete or ctx expires.
// Returns the number of operations still active (0 when cleanly drained).
func (h *GatewayHub) WaitIdle(ctx context.Context) int {
	if h == nil || h.client == nil {
		return 0
	}
	return h.client.waitIdle(ctx)
}

// CloseTunnels stops background workers and closes live Agent and Client
// WebSocket connections during graceful shutdown.
func (h *GatewayHub) CloseTunnels() {
	if h == nil {
		return
	}
	if h.bgCancel != nil {
		h.bgCancel()
	}
	if h.client != nil {
		h.client.closeClientConns()
	}
	if h.registry != nil {
		h.registry.CloseAll()
	}
}

func (h *GatewayHub) HandleAdminOperations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	if !h.store.UserIsPlatformAdmin(auth.UserID) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	writeJSONHTTP(w, map[string]interface{}{
		"draining":   h.IsDraining(),
		"operations": h.listActiveOperations(),
	})
}

func (h *GatewayHub) listActiveOperations() []GatewayActiveOperation {
	return h.client.listActiveOperations()
}

func (h *GatewayHub) cancelActiveOperation(id string) bool {
	return h.client.cancelActiveOperation(id)
}

func (h *GatewayHub) HandleAgentConnect(w http.ResponseWriter, r *http.Request) {
	h.registration.ServeHTTP(w, r)
}

func (h *GatewayHub) HandleTargets(w http.ResponseWriter, r *http.Request) {
	auth, err := h.authenticateUser(r)
	if err != nil {
		h.writeUserAuthError(w, r, err)
		return
	}
	targets := h.ListTargets()
	filtered := targets[:0]
	for _, target := range targets {
		if h.store.UserCanUseTarget(auth.UserID, target.Cluster, target.Instance) {
			filtered = append(filtered, target)
		}
	}
	writeJSONHTTP(w, targetResponse{Targets: filtered})
}

func (h *GatewayHub) HandleClientRPC(w http.ResponseWriter, r *http.Request) {
	h.client.HandleClientRPC(w, r)
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

func (h *GatewayHub) ListTargets() []GatewayTarget {
	return h.registry.List()
}

func tunnelKey(cluster, instance string) string {
	cluster = strings.TrimSpace(cluster)
	instance = strings.TrimSpace(instance)
	return cluster + "/" + instance
}
