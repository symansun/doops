package gatewaycmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
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
	case "store":
		store(args)
	case "secret-key":
		secretKey(args)
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
	operationTimeout := fs.Duration("operation-timeout", server.OperationTimeout(), "Maximum runtime for one forwarded operation")
	detachedTaskTimeout := fs.Duration("detached-task-timeout", server.DetachedTaskTimeout(), "Maximum lifetime of a detached background task (doops_bg detach)")
	// Retired limits stay parseable so existing unit files keep starting.
	retiredGlobalLimit := fs.Int("max-concurrent-operations", 0, "Retired; ignored")
	retiredUserLimit := fs.Int("max-concurrent-per-user", 0, "Retired; ignored")
	targetQueueTimeout := fs.Duration("target-queue-timeout", server.TargetQueueTimeout(), "How long an operation waits for a busy target")
	maxQueuedPerTarget := fs.Int("max-queued-per-target", 10, "Maximum queued operations per target; negative disables queueing")
	targetReconnectGrace := fs.Duration("target-reconnect-grace", server.TargetReconnectGrace(), "How long an RPC or internal call waits for a reconnecting Agent; 0 fails immediately, negative uses the default")
	shutdownDrainTimeout := fs.Duration("shutdown-drain-timeout", server.ShutdownDrainTimeout(), "On SIGTERM/SIGINT, how long to wait for running operations before closing connections; 0 exits immediately")
	// The issue scheduler was removed; the flag stays accepted so existing service units keep starting.
	_ = fs.Duration("scheduler-tick", 0, "Removed; accepted and ignored")
	monitorTick := fs.Duration("monitor-tick", server.MonitorTick(), "Interval to check due periodic monitor/inspection schedules; 0 disables")
	auditRetention := fs.Duration("audit-retention", 0, "Delete completed audit rows older than this (for example 2160h for 90 days); 0 keeps every row")
	webhookRetention := fs.Duration("webhook-retention", 720*time.Hour, "Delete terminal webhook deliveries and their events older than this; 0 keeps every row")
	edgeListen := fs.String("edge-listen", "", "TCP listen address for Edge public traffic; empty disables it")
	edgeProxyProtocol := fs.Bool("edge-proxy-protocol", true, "Require PROXY protocol on Edge listener (must be true behind nginx stream)")
	// Agent registration is open by default for targets without a confirmed
	// enrollment, and unenrolled targets can receive credentials in open mode.
	// Operators can opt into strict enrollment token enforcement with
	// -require-agent-enrollment / DOOPS_REQUIRE_AGENT_ENROLLMENT=1 (or restrict
	// credential delivery alone with -require-enrolled-credential-delivery /
	// DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY=1). Legacy flags and
	// environment variables stay accepted for compatibility.
	requireAgentEnrollment := fs.Bool("require-agent-enrollment", false, "Require every Agent to present its target enrollment token (DOOPS_REQUIRE_AGENT_ENROLLMENT=1 sets the same switch; default: open registration for targets without a confirmed enrollment)")
	allowOpenAgentRegistration := fs.Bool("allow-open-agent-registration", true, "Allow Agents without an enrollment token on targets that have no confirmed enrollment (default: true; set -require-agent-enrollment or DOOPS_REQUIRE_AGENT_ENROLLMENT=1 to require tokens)")
	requireEnrolledCredentialDelivery := fs.Bool("require-enrolled-credential-delivery", false, "Refuse delivering or using managed credentials on unenrolled Agents even while open registration is enabled (DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY=1 sets the same switch)")
	allowUnenrolledCredentialDelivery := fs.Bool("allow-unenrolled-credential-delivery", true, "Allow delivering and using managed credentials on unenrolled Agents while open registration is enabled (default: true unless -require-agent-enrollment or -require-enrolled-credential-delivery is set)")
	fs.Parse(args)

	explicitFlags := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		explicitFlags[f.Name] = true
	})
	enrollPolicy, err := resolveGatewayEnrollmentPolicy(
		os.Getenv,
		explicitFlags,
		*requireAgentEnrollment,
		*allowOpenAgentRegistration,
		*allowUnenrolledCredentialDelivery,
		*requireEnrolledCredentialDelivery,
	)
	if err != nil {
		log.Fatal(err)
	}

	if *auditRetention < 0 {
		log.Fatalf("invalid -audit-retention %s: use a positive duration or 0 to keep audit events forever", *auditRetention)
	}
	if *webhookRetention < 0 {
		log.Fatalf("invalid -webhook-retention %s: use a positive duration or 0 to keep webhook rows forever", *webhookRetention)
	}

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
		log.Printf("[gateway] refusing to start: master key check failed for database %s: %s", store.StoreLocation(), secretKeyStartupMessage(err))
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
		AllowOpenAgentRegistration: enrollPolicy.AllowOpenAgentRegistration,
		RequireAgentEnrollment:     enrollPolicy.RequireAgentEnrollment,
		// Environment only, never a flag: a flag would expose the secret in ps.
		ConsoleForwardSecret:              []byte(consoleForwardSecret),
		AllowUnenrolledCredentialDelivery: enrollPolicy.AllowUnenrolledCredentialDelivery,
		AuditRetention:                    *auditRetention,
		WebhookRetention:                  *webhookRetention,
	})
	hub.StartWebhookDispatcher()
	defer hub.StopWebhookDispatcher(server.HTTPShutdownTimeout())
	hub.StartHeartbeatFlusher(server.AgentHeartbeatFlush())
	hub.StartOfflineInstanceReaper(server.OfflineReaperInterval())
	hub.StartMonitorRunner(*monitorTick)
	hub.StartAuditRetention(*auditRetention, server.AuditRetentionInterval())
	hub.StartExpiredTokenCleanup(server.ExpiredTokenSweepInterval())

	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	log.Printf("🌉 doops-gateway starting on :%s", *port)
	// Print what the store actually opened. The MySQL backend reads
	// DOOPS_GATEWAY_MYSQL_DSN, so printing the SQLite path here pointed operators
	// at a database file the process never touched (D18).
	log.Printf("   database: %s", store.StoreLocation())
	if store.StoreBackend() == "mysql" {
		log.Printf("   data dir: %s", store.StoreDataDir())
	}
	if store.AuditStorageMode() == "file" {
		log.Printf("   audit storage: file (%s)", store.AuditDocDir())
	}
	log.Printf("   agent endpoint: ws://127.0.0.1:%s/v1/agent/connect", *port)
	log.Printf("   edge endpoint:  ws://127.0.0.1:%s/v1/edge/connect", *port)
	log.Printf("   client endpoint: ws://127.0.0.1:%s/v1/rpc?cluster=<cluster>&instance=<instance>", *port)
	log.Printf("   login token ttl=%s max-lifetime=%s", *loginTokenTTL, *loginTokenMaxLifetime)
	log.Printf("   agent enrollment: %s", agentEnrollmentPolicy(enrollPolicy))
	if enrollPolicy.ConflictWarning != "" {
		log.Printf("[gateway] WARNING: %s", enrollPolicy.ConflictWarning)
	}
	if *auditRetention > 0 {
		log.Printf("   audit retention: %s (runs every %s, receipt actor %s)", *auditRetention, server.AuditRetentionInterval(), server.AuditRetentionActor)
	} else {
		log.Printf("   audit retention: disabled (audit events are kept forever)")
	}
	log.Printf("   expired token cleanup: tokens expired more than %s ago, every %s", server.ExpiredTokenGrace, server.ExpiredTokenSweepInterval())
	if enrollPolicy.AllowOpenAgentRegistration {
		logOpenRegistrationNotice(hub, enrollPolicy.AllowUnenrolledCredentialDelivery)
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
		hub.StopWebhookDispatcher(server.HTTPShutdownTimeout())
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

	// The drain window above waits for in-flight operations; this budget is how
	// long the HTTP server itself gets to close connections once draining is over.
	shutdownHTTPServer(srv, server.HTTPShutdownTimeout())
	hub.CloseTunnels()
	log.Printf("[gateway] shutdown complete")
}

// shutdownHTTPServer closes the HTTP server within budget. It is deliberately
// separate from the drain window: draining waits for operations, this waits for
// connections. A hung keep-alive connection must not hold the process open for
// the whole drain window after the drain already finished.
func shutdownHTTPServer(srv *http.Server, budget time.Duration) {
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), budget)
	defer cancelShutdown()
	_ = srv.Shutdown(shutdownCtx)
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
		ReadHeaderTimeout: server.HTTPReadHeaderTimeout(),
		IdleTimeout:       server.HTTPIdleTimeout(),
		MaxHeaderBytes:    1 << 20,
	}
}

const (
	userPasswordFlag         = "password"
	userPasswordStdinFlag    = "password-stdin"
	userGeneratePasswordFlag = "generate-password"

	generatedPasswordLength = 24
)

// generatedPasswordAlphabet excludes look-alike characters (0/O, 1/l/I) and
// shell metacharacters that would change meaning when copy-pasted.
var generatedPasswordAlphabet = passwordAlphabet()

func passwordAlphabet() string {
	var alphabet strings.Builder
	for char := byte('a'); char <= 'z'; char++ {
		if char != 'l' {
			alphabet.WriteByte(char)
		}
	}
	for char := byte('A'); char <= 'Z'; char++ {
		if char != 'I' && char != 'O' {
			alphabet.WriteByte(char)
		}
	}
	for char := byte('2'); char <= '9'; char++ {
		alphabet.WriteByte(char)
	}
	alphabet.WriteString("!@#%^*-_=+")
	return alphabet.String()
}

func user(args []string) {
	if len(args) == 0 || (args[0] != "create" && args[0] != "passwd") {
		printUserUsage(os.Stderr)
		os.Exit(2)
	}
	if args[0] == "passwd" {
		userPasswd(args[1:])
		return
	}
	userCreate(args[1:])
}

func printUserUsage(w io.Writer) {
	fmt.Fprintf(w, "usage: doops-gateway user create -name <name> [-db path] [-%s | -%s]\n",
		userPasswordStdinFlag, userGeneratePasswordFlag)
	fmt.Fprintf(w, "       doops-gateway user passwd -name <name> (-%s | -%s) [-db path]\n",
		userPasswordStdinFlag, userGeneratePasswordFlag)
	fmt.Fprintf(w, "       -%s is refused: a password on argv is visible to every local user in 'ps' and is recorded in shell history; use -%s or -%s\n",
		userPasswordFlag, userPasswordStdinFlag, userGeneratePasswordFlag)
}

// registerUserPasswordFlags keeps the credential input on stdin, never on
// argv. The legacy -password flag stays registered only so the parse
// succeeds and the command can reject it with the reason and the replacement.
func registerUserPasswordFlags(fs *flag.FlagSet) {
	fs.String(userPasswordFlag, "", "Refused: a password on argv is visible in 'ps' and shell history; use -"+userPasswordStdinFlag+" or -"+userGeneratePasswordFlag)
	fs.Bool(userPasswordStdinFlag, false, "Read the login password from one stdin line; trailing CR/LF is removed")
	fs.Bool(userGeneratePasswordFlag, false, "Generate a strong random password, print it once, and store only its hash")
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// userPassword resolves the credential input for user create/passwd. It exits 2
// for every invalid combination, before the store is opened. The value is
// returned to the caller only for hashing and (for -generate-password)
// for the single one-time print; no error path writes it anywhere.
func userPassword(fs *flag.FlagSet, command string) (value string, generated bool) {
	argv := flagWasSet(fs, userPasswordFlag)
	stdin := flagWasSet(fs, userPasswordStdinFlag)
	generate := flagWasSet(fs, userGeneratePasswordFlag)

	if argv {
		if stdin || generate {
			conflict := userPasswordStdinFlag
			if generate {
				conflict = userGeneratePasswordFlag
			}
			fmt.Fprintf(os.Stderr, "doops-gateway %s: conflicting flags -%s and -%s: -%s is refused because a password on the command line is visible to every local user in 'ps' and is recorded in shell history; give only -%s or -%s\n",
				command, userPasswordFlag, conflict, userPasswordFlag, userPasswordStdinFlag, userGeneratePasswordFlag)
		} else {
			fmt.Fprintf(os.Stderr, "doops-gateway %s: -%s is refused: a password on the command line is visible to every local user in 'ps' and is recorded in shell history; use -%s (one line from stdin) or -%s (generated once, never stored in plaintext)\n",
				command, userPasswordFlag, userPasswordStdinFlag, userGeneratePasswordFlag)
		}
		os.Exit(2)
	}
	if stdin && generate {
		fmt.Fprintf(os.Stderr, "doops-gateway %s: conflicting flags -%s and -%s: give only one\n",
			command, userPasswordStdinFlag, userGeneratePasswordFlag)
		os.Exit(2)
	}
	if generate {
		return generateUserPassword(), true
	}
	if stdin {
		return readUserPassword(command), false
	}
	return "", false
}

func readUserPassword(command string) string {
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "doops-gateway %s: cannot read the password from stdin: %v\n", command, err)
		os.Exit(2)
	}
	password := strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(password) == "" {
		fmt.Fprintf(os.Stderr, "doops-gateway %s: no password on stdin: pipe exactly one line, for example: printf '%%s\\n' \"$PASSWORD\" | doops-gateway %s -%s\n",
			command, command, userPasswordStdinFlag)
		os.Exit(2)
	}
	return password
}

func generateUserPassword() string {
	for {
		candidate := make([]byte, generatedPasswordLength)
		for i := range candidate {
			index, err := rand.Int(rand.Reader, big.NewInt(int64(len(generatedPasswordAlphabet))))
			if err != nil {
				log.Fatalf("doops-gateway: cannot generate a password: %v", err)
			}
			candidate[i] = generatedPasswordAlphabet[index.Int64()]
		}
		if passwordHasRequiredClasses(candidate) {
			return string(candidate)
		}
	}
}

func passwordHasRequiredClasses(candidate []byte) bool {
	var lower, upper, digit, symbol bool
	for _, char := range candidate {
		switch {
		case char >= 'a' && char <= 'z':
			lower = true
		case char >= 'A' && char <= 'Z':
			upper = true
		case char >= '0' && char <= '9':
			digit = true
		default:
			symbol = true
		}
	}
	return lower && upper && digit && symbol
}

func printGeneratedPassword(password string) {
	fmt.Printf("password=%s\n", password)
	fmt.Println("warning=this password will not be shown again; store it now")
}

func userCreate(args []string) {
	fs := flag.NewFlagSet("user create", flag.ExitOnError)
	name := fs.String("name", "", "User name")
	registerUserPasswordFlags(fs)
	dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
	fs.Usage = func() {
		printUserUsage(fs.Output())
		fs.PrintDefaults()
	}
	fs.Parse(args)
	password, generated := userPassword(fs, "user create")
	if password == "" {
		fmt.Fprintf(os.Stderr, "doops-gateway user create: a password is required: give exactly one of -%s (one line from stdin) or -%s (generated once, never shown again)\n",
			userPasswordStdinFlag, userGeneratePasswordFlag)
		os.Exit(2)
	}
	store := mustStore(*dbPath)
	defer store.Close()
	u, err := store.CreateUserWithPassword(server.CreateUserRequest{Name: *name, Password: password})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("user_id=%s name=%s\n", u.ID, u.Name)
	if generated {
		printGeneratedPassword(password)
	} else {
		fmt.Println("password=enabled")
	}
}

func userPasswd(args []string) {
	fs := flag.NewFlagSet("user passwd", flag.ExitOnError)
	name := fs.String("name", "", "User name")
	registerUserPasswordFlags(fs)
	dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
	fs.Usage = func() {
		printUserUsage(fs.Output())
		fs.PrintDefaults()
	}
	fs.Parse(args)
	password, generated := userPassword(fs, "user passwd")
	if password == "" {
		fmt.Fprintf(os.Stderr, "doops-gateway user passwd: a new password is required: pass -%s or -%s\n",
			userPasswordStdinFlag, userGeneratePasswordFlag)
		os.Exit(2)
	}
	store := mustStore(*dbPath)
	defer store.Close()
	u, err := store.FindUserByName(*name)
	if err != nil {
		log.Fatal(err)
	}
	if err := store.SetUserPassword(u.ID, password); err != nil {
		log.Fatal(err)
	}
	if generated {
		printGeneratedPassword(password)
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
	if len(args) == 0 || (args[0] != "list" && args[0] != "purge" && args[0] != "export") {
		fmt.Fprintln(os.Stderr, "usage: doops-gateway audit list [-db path] [-limit N] [-user-id ID] [-cluster C] [-instance I] [-session S] [-action A] [-status X]")
		fmt.Fprintln(os.Stderr, "       doops-gateway audit purge -before <RFC3339> [-reclaim] [-db path]")
		fmt.Fprintln(os.Stderr, "       doops-gateway audit export (-output-dir <dir> | -output <file.jsonl.gz>) [-before <RFC3339> | -older-than <dur> | -all] [-cos-prefix <prefix>] [-purge -confirm-database <name>] [-reclaim]")
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		auditList(args[1:])
	case "purge":
		auditPurge(args[1:])
	case "export":
		auditExport(args[1:])
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
	reclaim := fs.Bool("reclaim", false, "Reclaim freed storage after purge (OPTIMIZE TABLE on MySQL; WAL checkpoint + VACUUM on SQLite)")
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
	if *reclaim {
		if err := store.ReclaimTables(context.Background(), "audit_events", "agent_resource_audit"); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("deleted=%d before=%s reclaimed=true\n", deleted, cutoff.UTC().Format(time.RFC3339))
		return
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

type gatewayEnrollmentPolicy struct {
	RequireAgentEnrollment            bool
	AllowOpenAgentRegistration        bool
	AllowUnenrolledCredentialDelivery bool
	ConflictWarning                   string
}

// envFlagEnabled accepts the conventional truthy spellings used by the
// Gateway's environment switches.
func envFlagEnabled(name string) bool {
	val, ok := parseEnvBoolFlag(os.Getenv(name))
	return ok && val
}

func parseEnvBoolFlag(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

// resolveGatewayEnrollmentPolicy computes the Gateway's agent enrollment and
// unenrolled credential policy from environment variables and explicit flags.
//
// Default behavior (when no flag or env override is set):
//   - Open registration is enabled for targets without a confirmed enrollment.
//   - Unenrolled targets can receive and use managed credentials.
//   - Targets that already confirmed an enrollment token remain enforced.
//
// Strict behavior (when DOOPS_REQUIRE_AGENT_ENROLLMENT=1, -require-agent-enrollment,
// DOOPS_ALLOW_OPEN_AGENT_REGISTRATION=0, or -allow-open-agent-registration=false
// is set):
//   - Every Agent must present a valid enrollment token for its exact target.
//   - Unenrolled credential delivery is always disabled.
func resolveGatewayEnrollmentPolicy(
	getenv func(string) string,
	explicitFlags map[string]bool,
	requireEnrollmentFlag bool,
	allowOpenFlag bool,
	allowUnenrolledCredFlag bool,
	requireEnrolledCredFlag bool,
) (gatewayEnrollmentPolicy, error) {
	if explicitFlags["require-agent-enrollment"] && explicitFlags["allow-open-agent-registration"] &&
		requireEnrollmentFlag == allowOpenFlag {
		return gatewayEnrollmentPolicy{}, errors.New("conflicting flags: -require-agent-enrollment and -allow-open-agent-registration disagree")
	}
	if explicitFlags["require-enrolled-credential-delivery"] && explicitFlags["allow-unenrolled-credential-delivery"] &&
		requireEnrolledCredFlag == allowUnenrolledCredFlag {
		return gatewayEnrollmentPolicy{}, errors.New("conflicting flags: -require-enrolled-credential-delivery and -allow-unenrolled-credential-delivery disagree")
	}

	requireEnrollment := false
	if raw := strings.TrimSpace(getenv("DOOPS_REQUIRE_AGENT_ENROLLMENT")); raw != "" {
		val, ok := parseEnvBoolFlag(raw)
		if !ok {
			return gatewayEnrollmentPolicy{}, fmt.Errorf("invalid DOOPS_REQUIRE_AGENT_ENROLLMENT value %q: use 1/true/yes/on or 0/false/no/off", raw)
		}
		requireEnrollment = val
	} else if raw := strings.TrimSpace(getenv("DOOPS_ALLOW_OPEN_AGENT_REGISTRATION")); raw != "" {
		if val, ok := parseEnvBoolFlag(raw); ok {
			requireEnrollment = !val
		}
	}
	if explicitFlags["require-agent-enrollment"] {
		requireEnrollment = requireEnrollmentFlag
	} else if explicitFlags["allow-open-agent-registration"] {
		requireEnrollment = !allowOpenFlag
	}

	if requireEnrollment {
		if explicitFlags["allow-unenrolled-credential-delivery"] && allowUnenrolledCredFlag {
			return gatewayEnrollmentPolicy{}, errors.New("-allow-unenrolled-credential-delivery cannot be combined with -require-agent-enrollment")
		}
		warning := ""
		if val, ok := parseEnvBoolFlag(getenv("DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY")); ok && val {
			warning = "DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY is ignored because agent enrollment is required"
		}
		return gatewayEnrollmentPolicy{
			RequireAgentEnrollment:            true,
			AllowOpenAgentRegistration:        false,
			AllowUnenrolledCredentialDelivery: false,
			ConflictWarning:                   warning,
		}, nil
	}

	allowUnenrolledCreds := true
	if raw := strings.TrimSpace(getenv("DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY")); raw != "" {
		val, ok := parseEnvBoolFlag(raw)
		if !ok {
			return gatewayEnrollmentPolicy{}, fmt.Errorf("invalid DOOPS_REQUIRE_ENROLLED_CREDENTIAL_DELIVERY value %q: use 1/true/yes/on or 0/false/no/off", raw)
		}
		allowUnenrolledCreds = !val
	} else if raw := strings.TrimSpace(getenv("DOOPS_ALLOW_UNENROLLED_CREDENTIAL_DELIVERY")); raw != "" {
		if val, ok := parseEnvBoolFlag(raw); ok {
			allowUnenrolledCreds = val
		}
	}
	if explicitFlags["require-enrolled-credential-delivery"] {
		allowUnenrolledCreds = !requireEnrolledCredFlag
	} else if explicitFlags["allow-unenrolled-credential-delivery"] {
		allowUnenrolledCreds = allowUnenrolledCredFlag
	}

	return gatewayEnrollmentPolicy{
		RequireAgentEnrollment:            false,
		AllowOpenAgentRegistration:        true,
		AllowUnenrolledCredentialDelivery: allowUnenrolledCreds,
	}, nil
}

// agentEnrollmentPolicy is the one-line startup summary of agent registration.
func agentEnrollmentPolicy(policy gatewayEnrollmentPolicy) string {
	if policy.RequireAgentEnrollment {
		return "enrollment token required for every Agent (-require-agent-enrollment)"
	}
	if policy.AllowUnenrolledCredentialDelivery {
		return "open registration enabled by default for targets without a confirmed enrollment (set DOOPS_REQUIRE_AGENT_ENROLLMENT=1 or -require-agent-enrollment to require enrollment tokens)"
	}
	return "open registration enabled for targets without a confirmed enrollment; credential delivery requires enrollment (-require-enrolled-credential-delivery)"
}

// logOpenRegistrationNotice reports the active open-registration mode once at
// startup and counts live sessions that are currently unenrolled. Only the
// target key is logged; enrollment tokens and token IDs are never printed.
func logOpenRegistrationNotice(hub *server.GatewayHub, allowUnenrolledCreds bool) {
	if allowUnenrolledCreds {
		log.Printf("[gateway] open agent registration is active for targets without a confirmed enrollment (unenrolled credential delivery enabled); targets with a confirmed enrollment remain protected. Set DOOPS_REQUIRE_AGENT_ENROLLMENT=1 or -require-agent-enrollment to require enrollment tokens for all Agents.")
	} else {
		log.Printf("[gateway] open agent registration is active for targets without a confirmed enrollment; unenrolled Agents receive no credentials. Set DOOPS_REQUIRE_AGENT_ENROLLMENT=1 or -require-agent-enrollment to require enrollment tokens for all Agents.")
	}
	unenrolled := 0
	for _, target := range hub.ListTargets() {
		if target.Enrolled {
			continue
		}
		unenrolled++
		log.Printf("[gateway] unenrolled target online: %s", target.Key)
	}
	if unenrolled > 0 {
		log.Printf("[gateway] %d live target(s) are not enrolled", unenrolled)
	}
}
