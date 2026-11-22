package gatewaycmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/user/doops/agent/internal/server"
	"github.com/user/doops/agent/pkg/release"
)

const defaultGatewayDB = "/var/lib/doops-gateway/gateway.db"

func Run(args []string) {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	switch cmd {
	case "serve":
		serve(args)
	case "version":
		fmt.Printf("release=%s commit=%s\n", release.Version, release.Commit)
	case "user":
		user(args)
	case "token":
		userToken(args)
	case "grant":
		grant(args)
	case "audit":
		audit(args)
	case "enroll":
		enroll(args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		os.Exit(2)
	}
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.String("port", "42222", "Public doops-gateway port")
	dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
	loginTokenTTL := fs.Duration("login-token-ttl", server.DefaultLoginTokenTTL, "Password-login sliding token TTL")
	loginTokenMaxLifetime := fs.Duration("login-token-max-lifetime", server.DefaultLoginTokenMaxLifetime, "Absolute max lifetime for a password-login token, including renewals")
	operationTimeout := fs.Duration("operation-timeout", 60*time.Minute, "Maximum runtime for one forwarded operation")
	detachedTaskTimeout := fs.Duration("detached-task-timeout", 24*time.Hour, "Maximum lifetime of a detached background task (doops_bg detach)")
	// Retired limits stay parseable so existing unit files keep starting.
	retiredGlobalLimit := fs.Int("max-concurrent-operations", 0, "Retired; ignored")
	retiredUserLimit := fs.Int("max-concurrent-per-user", 0, "Retired; ignored")
	targetQueueTimeout := fs.Duration("target-queue-timeout", 10*time.Minute, "How long an operation waits for a busy target")
	maxQueuedPerTarget := fs.Int("max-queued-per-target", 10, "Maximum queued operations per target; negative disables queueing")
	targetReconnectGrace := fs.Duration("target-reconnect-grace", 10*time.Second, "How long an RPC or internal call waits for a reconnecting Agent; 0 fails immediately, negative uses the default")
	shutdownDrainTimeout := fs.Duration("shutdown-drain-timeout", 2*time.Minute, "On SIGTERM/SIGINT, how long to wait for running operations before closing connections; 0 exits immediately")
	// The issue scheduler was removed; the flag stays accepted so existing service units keep starting.
	_ = fs.Duration("scheduler-tick", 0, "Removed; accepted and ignored")
	monitorTick := fs.Duration("monitor-tick", 15*time.Second, "Interval to check due periodic monitor/inspection schedules; 0 disables")
	edgeListen := fs.String("edge-listen", "", "TCP listen address for Edge public traffic; empty disables it")
	edgeProxyProtocol := fs.Bool("edge-proxy-protocol", true, "Require PROXY protocol on Edge listener (must be true behind nginx stream)")
	// Temporary migration escape hatch. Open agent registration is closed by
	// default; an Agent needs the enrollment token issued for its target.
	// The environment variable lets a deployment (for example a private
	// Gateway without enrollments) open it without editing the service unit.
	allowOpenDefault := envFlagEnabled("DOOPS_ALLOW_OPEN_AGENT_REGISTRATION")
	allowOpenAgentRegistration := fs.Bool("allow-open-agent-registration", allowOpenDefault, "TEMPORARY: accept Agents without an enrollment token on targets that have no confirmed enrollment (insecure; migrate them to enrollment tokens); DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=1 sets the same switch")
	allowUnenrolledCredentialDefault := envFlagEnabled("DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY")
	allowUnenrolledCredentialDelivery := fs.Bool("allow-unenrolled-credential-delivery", allowUnenrolledCredentialDefault, "TEMPORARY and insecure: deliver model/registry credentials to an unenrolled Agent on an explicitly open Gateway; DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY=1 sets the same switch")
	fs.Parse(args)

	if *retiredGlobalLimit != 0 || *retiredUserLimit != 0 {
		log.Printf("[gateway] -max-concurrent-operations and -max-concurrent-per-user are retired and ignored")
	}
	if *detachedTaskTimeout <= 0 {
		log.Fatal("-detached-task-timeout must be positive")
	}
	resolvedDBPath, err := canonicalGatewayDBPath(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	dbLock, err := acquireGatewayDBLock(resolvedDBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer dbLock.Close()

	store, err := server.OpenGatewayStore(resolvedDBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	consoleForwardSecret := strings.TrimSpace(os.Getenv("DOOPS_CONSOLE_FORWARD_SECRET"))

	// Fail closed before listening. Release the database and lock explicitly:
	// os.Exit skips defers, and the process must not look healthy when the key
	// does not match the ciphertext in this database.
	keyState, err := store.VerifySecretKey()
	if err != nil {
		log.Printf("[gateway] refusing to start: master key check failed for database %s: %s", resolvedDBPath, secretKeyStartupMessage(err))
		_ = store.Close()
		_ = dbLock.Close()
		os.Exit(1)
	}
	if keyState == server.GatewaySecretKeyAbsent {
		log.Printf("[gateway] warning: DOOPS_GATEWAY_SECRET_KEY is not set; credential features stay unavailable until it is configured")
	}

	hub := server.NewGatewayHub(store, server.GatewayHubOptions{
		LoginTokenTTL:              *loginTokenTTL,
		LoginTokenMaxLifetime:      *loginTokenMaxLifetime,
		OperationTimeout:           *operationTimeout,
		DetachedTaskTimeout:        *detachedTaskTimeout,
		TargetQueueTimeout:         *targetQueueTimeout,
		TargetReconnectGrace:       server.ReconnectGraceOption(*targetReconnectGrace),
		MaxQueuedPerTarget:         *maxQueuedPerTarget,
		AllowOpenAgentRegistration: *allowOpenAgentRegistration,
		RequireAgentEnrollment:     !*allowOpenAgentRegistration,
		// Environment only, never a flag: a flag would expose the secret in ps.
		ConsoleForwardSecret:              []byte(consoleForwardSecret),
		AllowUnenrolledCredentialDelivery: *allowUnenrolledCredentialDelivery,
	})
	hub.StartHeartbeatFlusher(30 * time.Second)
	hub.StartOfflineInstanceReaper(time.Hour)
	hub.StartMonitorRunner(*monitorTick)

	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	log.Printf("🌉 doops-gateway starting on :%s", *port)
	log.Printf("   database: %s", resolvedDBPath)
	log.Printf("   agent endpoint: ws://127.0.0.1:%s/v1/agent/connect", *port)
	log.Printf("   edge endpoint:  ws://127.0.0.1:%s/v1/edge/connect", *port)
	log.Printf("   client endpoint: ws://127.0.0.1:%s/v1/rpc?cluster=<cluster>&instance=<instance>", *port)
	log.Printf("   login token ttl=%s max-lifetime=%s", *loginTokenTTL, *loginTokenMaxLifetime)
	log.Printf("   agent enrollment: %s", agentEnrollmentPolicy(*allowOpenAgentRegistration))
	if *allowOpenAgentRegistration {
		logOpenRegistrationWarning(hub)
	}
	if *allowUnenrolledCredentialDelivery {
		log.Printf("[gateway] WARNING: DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY is enabled; unenrolled Agents can receive model and registry credentials. Use only while the Gateway has no enrollment tokens.")
	}
	if consoleForwardSecret != "" {
		// Never print the secret itself.
		log.Printf("   console login forwarding: enabled (DOOPS_CONSOLE_FORWARD_SECRET)")
	}
	if strings.TrimSpace(*edgeListen) != "" {
		listener, err := hub.StartEdgeListener(*edgeListen, *edgeProxyProtocol)
		if err != nil {
			log.Fatal(err)
		}
		defer listener.Close()
		log.Printf("   edge listen: %s proxy_protocol=%v", listener.Addr, *edgeProxyProtocol)
	}
	// ReadTimeout and WriteTimeout stay unset. They would apply to hijacked
	// agent, client, and edge tunnels. Slow header reads and idle keep-alives
	// are bounded separately.
	srv := gatewayHTTPServer(":"+*port, mux)
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serveErr:
		hub.CloseTunnels()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[gateway] http server stopped: %v", err)
			_ = store.Close()
			_ = dbLock.Close()
			os.Exit(1)
		}
		return
	case <-sigCtx.Done():
	}
	// Restore default signal handling so a second SIGTERM/SIGINT exits immediately.
	stop()

	drainWait := *shutdownDrainTimeout
	if drainWait < 0 {
		drainWait = 0
	}
	log.Printf("[gateway] draining: refusing new operations, waiting up to %s for %d active", drainWait, hub.ActiveOperationCount())
	hub.BeginDrain()
	if drainWait > 0 {
		drainCtx, cancelDrain := context.WithTimeout(context.Background(), drainWait)
		remaining := hub.WaitIdle(drainCtx)
		cancelDrain()
		if remaining > 0 {
			log.Printf("[gateway] drain timed out with %d operations still running; they will be recorded as interrupted on next start", remaining)
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	_ = srv.Shutdown(shutdownCtx)
	cancelShutdown()
	hub.CloseTunnels()
	log.Printf("[gateway] shutdown complete")
}

// secretKeyStartupMessage turns a startup key-check error into operator
// guidance. It never includes the key, a ciphertext or a payload value.
func secretKeyStartupMessage(err error) string {
	switch {
	case errors.Is(err, server.ErrSecretKeyMismatch):
		return "DOOPS_GATEWAY_SECRET_KEY does not match the key that encrypted the stored credential payloads; restore the original key (see backend/docs/CREDENTIAL_RESOURCES.md, master key backup) and restart"
	case errors.Is(err, server.ErrSecretKeyUnavailable):
		return "credential payloads are stored but DOOPS_GATEWAY_SECRET_KEY is not set; restore the original key and restart"
	default:
		return "credential master key check could not run"
	}
}

func gatewayHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
}

func user(args []string) {
	if len(args) == 0 || (args[0] != "create" && args[0] != "passwd") {
		fmt.Fprintln(os.Stderr, "usage: doops-gateway user create -name <name> [-password pass] [-db path]")
		fmt.Fprintln(os.Stderr, "       doops-gateway user passwd -name <name> -password <pass> [-db path]")
		os.Exit(2)
	}
	if args[0] == "passwd" {
		userPasswd(args[1:])
		return
	}
	fs := flag.NewFlagSet("user create", flag.ExitOnError)
	name := fs.String("name", "", "User name")
	password := fs.String("password", "", "Optional login password")
	dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
	fs.Parse(args[1:])
	store := mustStore(*dbPath)
	defer store.Close()
	u, err := store.CreateUserWithPassword(server.CreateUserRequest{Name: *name, Password: *password})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("user_id=%s name=%s\n", u.ID, u.Name)
	if *password != "" {
		fmt.Println("password=enabled")
	}
}

func userPasswd(args []string) {
	fs := flag.NewFlagSet("user passwd", flag.ExitOnError)
	name := fs.String("name", "", "User name")
	password := fs.String("password", "", "New login password")
	dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
	fs.Parse(args)
	store := mustStore(*dbPath)
	defer store.Close()
	u, err := store.FindUserByName(*name)
	if err != nil {
		log.Fatal(err)
	}
	if err := store.SetUserPassword(u.ID, *password); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("password=updated user=%s\n", u.Name)
}

func enroll(args []string) {
	if len(args) == 0 || (args[0] != "create" && args[0] != "list" && args[0] != "revoke") {
		fmt.Fprintln(os.Stderr, "usage: doops-gateway enroll create -cluster <cluster> -instance <instance> [-db path]")
		fmt.Fprintln(os.Stderr, "       doops-gateway enroll list [-db path]")
		fmt.Fprintln(os.Stderr, "       doops-gateway enroll revoke -id <id> [-db path]")
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("enroll list", flag.ExitOnError)
		dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
		fs.Parse(args[1:])
		store := mustStore(*dbPath)
		defer store.Close()
		rows, err := store.ListAgentEnrollments()
		if err != nil {
			log.Fatal(err)
		}
		for _, row := range rows {
			fmt.Printf("id=%s cluster=%s instance=%s prefix=%s revoked=%t enforced=%t created_at=%s\n",
				row.ID, row.Cluster, row.Instance, row.Prefix, row.Revoked, row.Enforced, row.CreatedAt.UTC().Format(time.RFC3339))
		}
	case "revoke":
		fs := flag.NewFlagSet("enroll revoke", flag.ExitOnError)
		id := fs.String("id", "", "Enrollment id")
		dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
		fs.Parse(args[1:])
		if strings.TrimSpace(*id) == "" {
			log.Fatal("enrollment id is required")
		}
		store := mustStore(*dbPath)
		defer store.Close()
		if err := store.RevokeAgentEnrollment(*id); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("id=%s revoked=true\n", strings.TrimSpace(*id))
	default:
		fs := flag.NewFlagSet("enroll create", flag.ExitOnError)
		cluster := fs.String("cluster", "", "Cluster name")
		instance := fs.String("instance", "", "Instance name")
		dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
		fs.Parse(args[1:])
		store := mustStore(*dbPath)
		defer store.Close()
		created, err := store.CreateAgentEnrollment(*cluster, *instance)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("id=%s\ncluster=%s\ninstance=%s\ntoken=%s\n", created.ID, created.Cluster, created.Instance, created.Plaintext)
		fmt.Println("warning=store this enrollment token now; only its hash is saved. The connected Agent stays online until it reconnects. Put the token on that Agent first; enforcement starts after it connects with this token")
	}
}

func userToken(args []string) {
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprintln(os.Stderr, "usage: doops-gateway token create -user <name> [-name label] [-db path]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("token create", flag.ExitOnError)
	userName := fs.String("user", "", "User name")
	name := fs.String("name", "", "Token label")
	dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
	expires := fs.Duration("expires", 0, "Optional token TTL, e.g. 720h")
	fs.Parse(args[1:])
	store := mustStore(*dbPath)
	defer store.Close()
	u, err := store.FindUserByName(*userName)
	if err != nil {
		log.Fatal(err)
	}
	req := server.CreateTokenRequest{Kind: server.TokenKindUser, UserID: u.ID, Name: *name}
	if *expires > 0 {
		req.ExpiresAt = time.Now().Add(*expires)
	}
	tok, err := store.CreateToken(req)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("token=%s\n", tok.Plaintext)
	fmt.Println("warning=store this token now; only its hash is saved")
}

func grant(args []string) {
	fs := flag.NewFlagSet("grant", flag.ExitOnError)
	userName := fs.String("user", "", "User name")
	cluster := fs.String("cluster", "*", "Cluster scope")
	instance := fs.String("instance", "*", "Instance scope")
	admin := fs.Bool("admin", false, "Make the user a platform admin instead of granting a target")
	dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
	fs.Parse(args)
	store := mustStore(*dbPath)
	defer store.Close()
	u, err := store.FindUserByName(*userName)
	if err != nil {
		log.Fatal(err)
	}
	if *admin {
		if err := store.SetUserAdmin(u.ID, true); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("granted user=%s admin=true\n", u.Name)
		return
	}
	if err := store.GrantUser(u.ID, server.ScopeGrant{Cluster: *cluster, Instance: *instance}); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("granted user=%s scope=%s/%s\n", u.Name, *cluster, *instance)
}

func audit(args []string) {
	if len(args) == 0 || (args[0] != "list" && args[0] != "purge") {
		fmt.Fprintln(os.Stderr, "usage: doops-gateway audit list [-db path] [-limit N] [-user-id ID] [-cluster C] [-instance I] [-session S] [-action A] [-status X]")
		fmt.Fprintln(os.Stderr, "       doops-gateway audit purge -before <RFC3339> [-db path]")
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		auditList(args[1:])
	case "purge":
		auditPurge(args[1:])
	}
}

func auditList(args []string) {
	fs := flag.NewFlagSet("audit list", flag.ExitOnError)
	dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
	limit := fs.Int("limit", 50, "Rows to show")
	userID := fs.String("user-id", "", "Filter by user id")
	cluster := fs.String("cluster", "", "Filter by cluster")
	instance := fs.String("instance", "", "Filter by instance")
	session := fs.String("session", "", "Filter by session")
	action := fs.String("action", "", "Filter by action")
	status := fs.String("status", "", "Filter by status")
	fs.Parse(args)
	store := mustStore(*dbPath)
	defer store.Close()
	events, err := store.ListAuditFiltered(server.AuditFilter{
		UserID:   *userID,
		Cluster:  *cluster,
		Instance: *instance,
		Session:  *session,
		Action:   server.GatewayAction(strings.TrimSpace(*action)),
		Status:   *status,
		Limit:    *limit,
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, ev := range events {
		fmt.Printf("%d %s user=%s token=%s %s/%s action=%s session=%s status=%s error=%s cmd=%q\n",
			ev.ID, ev.StartedAt.Format(time.RFC3339), ev.UserID, ev.TokenID, ev.Cluster, ev.Instance,
			ev.Action, ev.Session, ev.Status, ev.Error, ev.CommandSummary)
	}
}

func auditPurge(args []string) {
	fs := flag.NewFlagSet("audit purge", flag.ExitOnError)
	dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
	before := fs.String("before", "", "Delete eligible completed audit rows before this RFC3339 timestamp and record a local operator receipt")
	fs.Parse(args)
	if strings.TrimSpace(*before) == "" {
		log.Fatal("-before is required")
	}
	cutoff, err := time.Parse(time.RFC3339, strings.TrimSpace(*before))
	if err != nil {
		log.Fatalf("invalid -before timestamp: %v", err)
	}
	store := mustStore(*dbPath)
	defer store.Close()
	deleted, err := store.PurgeLocalAuditBefore(cutoff)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("deleted=%d before=%s\n", deleted, cutoff.UTC().Format(time.RFC3339))
}

func mustStore(path string) *server.GatewayStore {
	store, err := server.OpenGatewayStore(path)
	if err != nil {
		log.Fatal(err)
	}
	return store
}

// agentEnrollmentPolicy is the one-line startup summary of agent registration.
// envFlagEnabled accepts the conventional truthy spellings used by the
// Gateway's other environment-only switches.
func envFlagEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func agentEnrollmentPolicy(allowOpen bool) string {
	if allowOpen {
		return "open registration ENABLED for targets without a confirmed enrollment (temporary; migrate every Agent to an enrollment token)"
	}
	return "enrollment token required for every Agent"
}

// logOpenRegistrationWarning states the security consequence once at startup
// and counts the live sessions that are currently unenrolled. Only the target
// key is logged; enrollment tokens and token IDs are never printed.
func logOpenRegistrationWarning(hub *server.GatewayHub) {
	log.Printf("[gateway] WARNING: -allow-open-agent-registration is set. Any client that knows a cluster/instance without a confirmed enrollment can impersonate that target. It receives and can use no credentials, but it can run other operations as that target. Turn the flag off once every Agent carries its enrollment token.")
	unenrolled := 0
	for _, target := range hub.ListTargets() {
		if target.Enrolled {
			continue
		}
		unenrolled++
		log.Printf("[gateway] WARNING: unenrolled target online: %s", target.Key)
	}
	log.Printf("[gateway] WARNING: %d live target(s) are not enrolled", unenrolled)
}
