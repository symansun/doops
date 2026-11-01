package gatewaycmd

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
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
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", cmd)
		os.Exit(2)
	}
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.String("port", "42222", "Public doops-gateway port")
	dbPath := fs.String("db", defaultGatewayDB, "SQLite database path")
	loginTokenTTL := fs.Duration("login-token-ttl", 24*time.Hour, "Password-login token TTL")
	operationTimeout := fs.Duration("operation-timeout", 30*time.Minute, "Maximum runtime for one forwarded operation")
	// Retired limits stay parseable so existing unit files keep starting.
	retiredGlobalLimit := fs.Int("max-concurrent-operations", 0, "Retired; ignored")
	retiredUserLimit := fs.Int("max-concurrent-per-user", 0, "Retired; ignored")
	targetQueueTimeout := fs.Duration("target-queue-timeout", 10*time.Minute, "How long an operation waits for a busy target")
	maxQueuedPerTarget := fs.Int("max-queued-per-target", 10, "Maximum queued operations per target; negative disables queueing")
	// The issue scheduler was removed; the flag stays accepted so existing service units keep starting.
	_ = fs.Duration("scheduler-tick", 0, "Removed; accepted and ignored")
	edgeListen := fs.String("edge-listen", "", "TCP listen address for Edge public traffic; empty disables it")
	edgeProxyProtocol := fs.Bool("edge-proxy-protocol", true, "Require PROXY protocol on Edge listener (must be true behind nginx stream)")
	fs.Parse(args)

	if *retiredGlobalLimit != 0 || *retiredUserLimit != 0 {
		log.Printf("[gateway] -max-concurrent-operations and -max-concurrent-per-user are retired and ignored")
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

	retired, err := store.DeleteStaleOfflineAgents(time.Now().UTC(), server.StaleOfflineAgentRetention)
	if err != nil {
		log.Fatal(err)
	}
	if retired > 0 {
		log.Printf("[gateway] retired %d offline instance records older than %s", retired, server.StaleOfflineAgentRetention)
	}
	go reapStaleOfflineAgents(store)

	hub := server.NewGatewayHub(store, server.GatewayHubOptions{
		LoginTokenTTL:      *loginTokenTTL,
		OperationTimeout:   *operationTimeout,
		TargetQueueTimeout: *targetQueueTimeout,
		MaxQueuedPerTarget: *maxQueuedPerTarget,
	})

	mux := http.NewServeMux()
	hub.RegisterRoutes(mux)

	log.Printf("🌉 doops-gateway starting on :%s", *port)
	log.Printf("   database: %s", resolvedDBPath)
	log.Printf("   agent endpoint: ws://127.0.0.1:%s/v1/agent/connect", *port)
	log.Printf("   edge endpoint:  ws://127.0.0.1:%s/v1/edge/connect", *port)
	log.Printf("   client endpoint: ws://127.0.0.1:%s/v1/rpc?cluster=<cluster>&instance=<instance>", *port)
	if strings.TrimSpace(*edgeListen) != "" {
		listener, err := hub.StartEdgeListener(*edgeListen, *edgeProxyProtocol)
		if err != nil {
			log.Fatal(err)
		}
		defer listener.Close()
		log.Printf("   edge listen: %s proxy_protocol=%v", listener.Addr, *edgeProxyProtocol)
	}
	if err := http.ListenAndServe(":"+*port, mux); err != nil {
		log.Fatal(err)
	}
}

func reapStaleOfflineAgents(store *server.GatewayStore) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		retired, err := store.DeleteStaleOfflineAgents(time.Now().UTC(), server.StaleOfflineAgentRetention)
		if err != nil {
			log.Printf("[gateway] stale instance sweep failed: %v", err)
			continue
		}
		if retired > 0 {
			log.Printf("[gateway] retired %d offline instance records older than %s", retired, server.StaleOfflineAgentRetention)
		}
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
	before := fs.String("before", "", "Delete audit rows before this RFC3339 timestamp")
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
	deleted, err := store.DeleteAuditBefore(cutoff)
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
